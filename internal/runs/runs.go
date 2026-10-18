package runs

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"github.com/robfig/cron/v3"
	"io"
	"log/slog"
	"mirrordock/internal/config"
	"mirrordock/internal/database"
	"mirrordock/internal/mirrors"
	syncer "mirrordock/internal/sync"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Snapshot struct {
	Source   config.Source   `json:"source"`
	Settings config.Settings `json:"settings"`
}

type Attempt struct {
	ReceivedBytes *int64    `json:"received_bytes"`
	Number        int       `json:"number"`
	Candidate     string    `json:"candidate"`
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at,omitzero"`
	ErrorCode     string    `json:"error_code"`
	Error         string    `json:"error"`
	ExitCode      int       `json:"exit_code"`
	LogCleared    bool      `json:"log_cleared"`
}

type Event struct {
	At      time.Time `json:"at"`
	Message string    `json:"message"`
}

type Run struct {
	ID             int64           `json:"id"`
	SourceID       string          `json:"source_id"`
	Name           string          `json:"name"`
	Type           string          `json:"type"`
	Dest           string          `json:"dest"`
	State          string          `json:"state"`
	Trigger        string          `json:"trigger"`
	CreatedAt      time.Time       `json:"created_at"`
	StartedAt      time.Time       `json:"started_at,omitzero"`
	EndedAt        time.Time       `json:"ended_at,omitzero"`
	ElapsedSeconds float64         `json:"elapsed_seconds"`
	BudgetSeconds  float64         `json:"budget_seconds"`
	RetryCount     int             `json:"retry_count"`
	RetryAt        time.Time       `json:"retry_at,omitzero"`
	CandidateIndex int             `json:"candidate_index"`
	Progress       syncer.Progress `json:"progress"`
	Attempts       []Attempt       `json:"attempts"`
	Events         []Event         `json:"events"`
	ErrorCode      string          `json:"error_code"`
	Error          string          `json:"error"`
	Snapshot       Snapshot        `json:"snapshot"`
}

func (r *Run) SetID(id int64) { r.ID = id }

var terminalStates = []string{"succeeded", "failed", "timed_out", "cancelled"}

func Terminal(state string) bool { return slices.Contains(terminalStates, state) }

// reserved states keep the source's work directory and block new runs.
func reserved(state string) bool { return !Terminal(state) }

// active describes a run whose executor is currently running.
type active struct {
	cancel context.CancelCauseFunc
	start  time.Time
	// base is the elapsed budget consumed before this execution started.
	base float64
	// intent is the state requested by pause, cancel or shutdown.
	intent string
}

func (a *active) elapsed() float64 { return a.base + time.Since(a.start).Seconds() }

// Engine timing defaults.
const (
	checkpointInterval = 5 * time.Second
	cleanupInterval    = 5 * time.Minute
	cleanupTimeout     = 30 * time.Second
	defaultRetryDelay  = 5 * time.Second
	queryTimeout       = 5 * time.Second
	busyPollInterval   = time.Second
	idlePollInterval   = 500 * time.Millisecond
	maxRetryDoublings  = 4
	logReadLimit       = 128 << 10
)

type Engine struct {
	mu                 sync.Mutex
	logMu              sync.RWMutex
	clearMu            sync.Mutex
	clearing           map[int64]chan struct{}
	removeLog          func(string) error
	configMu           sync.Mutex
	cfg                *config.Manager
	store              *database.Store
	mirror             *mirrors.Manager
	runner             syncer.Runner
	data               string
	ctx                context.Context
	cancel             context.CancelFunc
	wg                 sync.WaitGroup
	pending            []*Run
	current            map[int64]*Run
	working            map[int64]*active
	wake               chan struct{}
	cleanupWake        chan struct{}
	admitSync          func(int64) error
	storageCheck       func() error
	checkpointInterval time.Duration
	cron               *cron.Cron
	retryDelay         time.Duration
	location           *time.Location
	scheduleIDs        map[string]cron.EntryID
	ConfigValidator    func(config.Config) error
	ConfigApplied      func() error
}

func New(
	cfg *config.Manager,
	store *database.Store,
	mirror *mirrors.Manager,
	runner syncer.Runner,
	location *time.Location,
) (*Engine, error) {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		cfg:                cfg,
		store:              store,
		mirror:             mirror,
		runner:             runner,
		data:               config.Resolve(cfg.File(), cfg.Startup().Path),
		ctx:                ctx,
		cancel:             cancel,
		current:            map[int64]*Run{},
		working:            map[int64]*active{},
		clearing:           map[int64]chan struct{}{},
		wake:               make(chan struct{}, 1),
		cleanupWake:        make(chan struct{}, 1),
		location:           location,
		retryDelay:         defaultRetryDelay,
		checkpointInterval: checkpointInterval,
		scheduleIDs:        map[string]cron.EntryID{},
		admitSync:          mirror.BeginSync,
		storageCheck:       mirror.CheckSpace,
		removeLog:          os.Remove,
	}
	if err := e.recover(); err != nil {
		cancel()
		return nil, err
	}
	e.cron = cron.New(cron.WithLocation(location), cron.WithChain(cron.Recover(cron.DefaultLogger)))
	if err := e.Schedules(); err != nil {
		cancel()
		return nil, err
	}
	return e, nil
}

// recover loads every unfinished run in ID pages and decides how it resumes
// after a restart.
func (e *Engine) recover() error {
	sources := e.cfg.Current().Sources
	var after int64
	for {
		records, err := e.store.RecoverableRuns(e.ctx, after)
		if err != nil {
			return err
		}
		for _, document := range records {
			r := &Run{}
			if err := json.Unmarshal(document, r); err != nil {
				return err
			}
			after = r.ID
			previousState := r.State
			e.recoverRun(r, sources)
			if r.State != previousState {
				if err := e.save(r); err != nil {
					return err
				}
			}
			if reserved(r.State) {
				e.current[r.ID] = r
			}
			if r.State == "queued" {
				e.pending = append(e.pending, r)
			}
		}
		if len(records) < database.PageSize {
			break
		}
	}
	slices.SortFunc(e.pending, func(a, b *Run) int { return cmp.Compare(a.ID, b.ID) })
	return nil
}

// recoverRun applies the restart rules to one unfinished run: an execution
// stopped by the restart continues with its remaining budget, unless the
// source changed in YAML meanwhile or the budget is spent.
func (e *Engine) recoverRun(r *Run, sources []config.Source) {
	if r.State == "running" || r.State == "pausing" {
		if r.State == "pausing" {
			r.State = "paused"
		} else {
			r.State = "interrupted"
		}
		r.ErrorCode = "SERVICE_INTERRUPTED"
		r.Error = "服务中断；保留剩余预算"
		e.event(r, r.Error)
		if len(r.Attempts) > 0 {
			last := &r.Attempts[len(r.Attempts)-1]
			last.EndedAt = time.Now().UTC()
			last.ErrorCode = r.ErrorCode
		}
	}
	conflict := sourceConflict(r, sources)
	if conflict != "" {
		// YAML may change while stopped. Never run a snapshot against a
		// removed, retyped, moved or disabled source after restart.
		stopForSource(r, conflict)
	}
	if r.State != "interrupted" || r.ErrorCode != "SERVICE_INTERRUPTED" {
		return
	}
	switch {
	case r.ElapsedSeconds >= r.BudgetSeconds:
		r.State = "timed_out"
		r.ErrorCode = "TASK_TIMEOUT"
		r.Error = "累计执行时间达到任务上限"
		r.EndedAt = time.Now().UTC()
	case conflict == "":
		r.State = "queued"
		r.EndedAt = time.Time{}
		r.Error, r.ErrorCode = "", ""
		e.event(r, "服务恢复，自动排队继续；沿用累计预算及重试次数")
	}
}

func (e *Engine) Start() {
	e.cron.Start()
	e.wg.Go(e.dispatch)
	e.wg.Go(e.cleanupLoop)
	// Capacity monitoring and persistence must not wait for a large directory
	// cleanup to finish.
	e.wg.Go(e.checkpointLoop)
	e.requestCleanup()
}

// cleanupLoop reclaims finished work trees and expired generations
// periodically and whenever a run leaves the reserved states.
func (e *Engine) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		var now time.Time
		select {
		case <-e.ctx.Done():
			return
		case now = <-ticker.C:
		case <-e.cleanupWake:
			now = time.Now()
		}
		ctx, cancel := context.WithTimeout(e.ctx, cleanupTimeout)
		if err := e.mirror.CleanupWork(ctx, e.workPolicy(now)); err != nil {
			slog.Error("clean terminal work", "error", err)
		}
		if err := e.mirror.CollectContext(ctx, now); err != nil {
			slog.Error("collect generations", "error", err)
		}
		cancel()
	}
}

// checkpointLoop persists the elapsed budget of running tasks and stops them
// when the mirror filesystem drops below its safety margin.
func (e *Engine) checkpointLoop() {
	ticker := time.NewTicker(e.checkpointInterval)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.checkpoint()
		}
	}
}

func (e *Engine) checkpoint() {
	e.mu.Lock()
	defer e.mu.Unlock()
	spaceErr := e.storageCheck()
	lowSpace := errors.Is(spaceErr, mirrors.ErrLowSpace)
	if spaceErr != nil && !lowSpace {
		slog.Error("check mirror capacity", "error", spaceErr)
	}
	for id, execution := range e.working {
		r := e.current[id]
		// Publication only renames the finished work tree on the same
		// filesystem; stopping it would discard a completed sync.
		if lowSpace && r.Progress.Phase != "publish" {
			execution.cancel(spaceErr)
		}
		r.ElapsedSeconds = execution.elapsed()
		if err := e.save(r); err != nil {
			slog.Error("save progress", "error", err)
		}
	}
}

func (e *Engine) Close() {
	e.cancel()
	<-e.cron.Stop().Done()
	e.mu.Lock()
	for _, execution := range e.working {
		if execution.intent == "" {
			execution.intent = "interrupted"
		}
		execution.cancel(errors.New("service shutdown"))
	}
	e.mu.Unlock()
	e.wg.Wait()
}

func (e *Engine) event(r *Run, message string) {
	r.Events = append(r.Events, Event{At: time.Now().UTC(), Message: message})
}

func (e *Engine) save(r *Run) error { return e.store.SaveRun(r.ID, r.State, r) }

// wakeDispatcher nudges the idle dispatcher without blocking.
func (e *Engine) wakeDispatcher() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) busySource(id string) bool {
	for _, r := range e.current {
		if r.SourceID == id && reserved(r.State) {
			return true
		}
	}
	return false
}

func (e *Engine) ValidateChange(c config.Config) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.validateChange(c)
}

func (e *Engine) validateChange(c config.Config) error {
	for _, r := range e.current {
		if !reserved(r.State) {
			continue
		}
		index := slices.IndexFunc(c.Sources, func(s config.Source) bool { return s.ID == r.SourceID })
		if index < 0 || c.Sources[index].Dest != r.Dest || c.Sources[index].Type != r.Type {
			return errors.New("活动、暂停或中断任务的源不能删除或变更类型、dest，请先取消任务")
		}
	}
	return nil
}

// Source conflict codes describe why a reserved run no longer matches YAML.
const (
	sourceRemoved  = "SOURCE_REMOVED"
	sourceChanged  = "SOURCE_CHANGED"
	sourceDisabled = "SOURCE_DISABLED"
)

func sourceConflict(r *Run, sources []config.Source) string {
	index := slices.IndexFunc(sources, func(s config.Source) bool { return s.ID == r.SourceID })
	switch {
	case index < 0:
		return sourceRemoved
	case sources[index].Type != r.Type || sources[index].Dest != r.Dest:
		return sourceChanged
	case !sources[index].Enabled:
		return sourceDisabled
	}
	return ""
}

// stopForSource cancels runs whose source identity is gone. A disabled source
// keeps its queued run resumable after it is enabled again.
func stopForSource(r *Run, conflict string) {
	switch conflict {
	case sourceRemoved, sourceChanged:
		if conflict == sourceRemoved {
			r.Error = "源已从配置删除，任务已自动取消"
		} else {
			r.Error = "源类型或 dest 已变更，任务已自动取消"
		}
		r.State, r.ErrorCode = "cancelled", conflict
		r.EndedAt = time.Now().UTC()
	case sourceDisabled:
		if r.State != "queued" {
			return
		}
		r.State, r.ErrorCode = "interrupted", conflict
		r.Error = "源已禁用；启用后可继续，或取消任务"
	default:
		return
	}
	r.Events = append(r.Events, Event{At: time.Now().UTC(), Message: r.Error})
}

func resumeConflict(conflict string) error {
	switch conflict {
	case sourceRemoved:
		return errors.New("源已从配置删除，不能继续；请取消任务")
	case sourceChanged:
		return errors.New("源类型或 dest 已变更，不能继续；请取消任务")
	case sourceDisabled:
		return errors.New("源已禁用，启用后再继续")
	}
	return nil
}

func (e *Engine) ApplyConfig(c config.Config, revision string, token, proxy *string) error {
	e.configMu.Lock()
	defer e.configMu.Unlock()
	e.mu.Lock()
	if err := e.validateChange(c); err != nil {
		e.mu.Unlock()
		return err
	}
	err := e.cfg.Save(c, revision, token, proxy)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	// A raised concurrency limit may admit queued runs immediately.
	e.wakeDispatcher()
	reconcileErr := e.mirror.Reconcile(e.ctx, e.cfg.Current().Sources)
	if reconcileErr != nil {
		reconcileErr = fmt.Errorf("配置已保存，但发布目录接入失败，请修复后重新载入配置: %w", reconcileErr)
	}
	return partialApply(errors.Join(reconcileErr, e.Schedules()))
}

// PartialApplyError means YAML was already saved or loaded, but a follow-up
// step failed. Callers must adopt the new revision and show the warning.
type PartialApplyError struct{ Err error }

func (e *PartialApplyError) Error() string { return e.Err.Error() }

func (e *PartialApplyError) Unwrap() error { return e.Err }

func partialApply(err error) error {
	if err == nil {
		return nil
	}
	return &PartialApplyError{Err: err}
}

func (e *Engine) Reload() error {
	e.configMu.Lock()
	defer e.configMu.Unlock()
	e.mu.Lock()
	err := e.cfg.ReloadChecked(func(c config.Config) error {
		if err := e.validateChange(c); err != nil {
			return err
		}
		if e.ConfigValidator != nil {
			return e.ConfigValidator(c)
		}
		return nil
	})
	var appliedErr error
	if err == nil && e.ConfigApplied != nil {
		appliedErr = e.ConfigApplied()
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	e.wakeDispatcher()
	reconcileErr := e.mirror.Reconcile(e.ctx, e.cfg.Current().Sources)
	if reconcileErr != nil {
		reconcileErr = fmt.Errorf("配置已载入，但发布目录接入失败，请修复后重新载入配置: %w", reconcileErr)
	}
	return partialApply(errors.Join(appliedErr, reconcileErr, e.Schedules()))
}

func (e *Engine) Enqueue(sourceID, trigger string) (*Run, error) {
	e.configMu.Lock()
	defer e.configMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cfg.View().RestartRequired {
		return nil, errors.New("监听或数据目录配置已修改，请先重启")
	}
	c := e.cfg.Current()
	index := slices.IndexFunc(c.Sources, func(s config.Source) bool { return s.ID == sourceID })
	if index < 0 {
		return nil, errors.New("源不存在")
	}
	source := c.Sources[index]
	if !source.Enabled {
		return nil, errors.New("源已禁用")
	}
	if e.busySource(source.ID) {
		return nil, errors.New("该源已有活动、暂停或中断任务")
	}
	budget, _ := time.ParseDuration(c.Settings.TaskTimeout)
	// The snapshot is a durable run fact: credentials are read again at
	// execution time and never stored with the run.
	settings := c.Settings
	settings.GitHub.Token = ""
	settings.GitHub.Proxy = ""
	settings.Notifications = nil
	r := &Run{
		SourceID:      source.ID,
		Name:          source.Name,
		Type:          source.Type,
		Dest:          source.Dest,
		State:         "queued",
		Trigger:       trigger,
		CreatedAt:     time.Now().UTC(),
		BudgetSeconds: budget.Seconds(),
		Progress:      syncer.Progress{Phase: "prepare"},
		Snapshot:      Snapshot{Source: publicSource(source), Settings: settings},
	}
	e.event(r, "任务已加入队列")
	id, err := e.store.CreateRun(source.ID, r.State, r.CreatedAt.Format(time.RFC3339Nano), r)
	if err != nil {
		return nil, err
	}
	r.ID = id
	e.current[id] = r
	e.pending = append(e.pending, r)
	e.wakeDispatcher()
	return cloneRun(r), nil
}

func cloneRun(r *Run) *Run {
	encoded, _ := json.Marshal(r)
	var out Run
	_ = json.Unmarshal(encoded, &out)
	return &out
}

func (e *Engine) Action(id int64, action string) error {
	for {
		e.mu.Lock()
		done := e.clearing[id]
		if done == nil {
			break
		}
		e.mu.Unlock()
		select {
		case <-done:
		case <-e.ctx.Done():
			return e.ctx.Err()
		}
	}
	defer e.mu.Unlock()
	r := e.current[id]
	if r == nil {
		return errors.New("仅可操作活动或中断任务")
	}
	switch action {
	case "pause":
		if r.State == "queued" {
			r.State = "paused"
		} else if r.State == "running" {
			r.State = "pausing"
			e.working[id].intent = "paused"
			e.working[id].cancel(errors.New("paused"))
		} else {
			return errors.New("当前任务不可暂停")
		}
	case "resume":
		if e.working[id] != nil {
			return errors.New("执行器正在停止，请等待退出后继续")
		}
		if r.State != "paused" && r.State != "interrupted" {
			return errors.New("当前任务不可继续")
		}
		if r.ElapsedSeconds >= r.BudgetSeconds {
			return errors.New("累计预算已耗尽")
		}
		if err := resumeConflict(sourceConflict(r, e.cfg.Current().Sources)); err != nil {
			return err
		}
		r.State = "queued"
		r.Error = ""
		r.ErrorCode = ""
	case "cancel":
		if execution := e.working[id]; execution != nil {
			execution.intent = "cancelled"
			execution.cancel(errors.New("cancelled"))
		} else {
			r.State = "cancelled"
			r.EndedAt = time.Now().UTC()
		}
	default:
		return errors.New("未知任务操作")
	}
	e.event(r, "操作: "+action)
	if err := e.save(r); err != nil {
		if e.working[id] == nil {
			r.State = "interrupted"
		}
		r.ErrorCode = "PERSISTENCE_FAILED"
		r.Error = "操作结果保存失败，任务目录仍保留；修复存储后继续或取消"
		return err
	}
	if action == "resume" {
		e.pending = append(e.pending, r)
		e.wakeDispatcher()
	}
	if !reserved(r.State) && e.working[id] == nil {
		delete(e.current, id)
		e.requestCleanup()
	}
	return nil
}

// stopQueued persists a source conflict found at dequeue time. Callers hold e.mu.
func (e *Engine) stopQueued(r *Run, conflict string) {
	previous := *r
	stopForSource(r, conflict)
	if err := e.save(r); err != nil {
		slog.Error("save source conflict", "run", r.ID, "error", err)
		*r = previous
		r.State, r.ErrorCode = "interrupted", "PERSISTENCE_FAILED"
		r.Error = "任务状态保存失败；修复存储后继续或取消"
		return
	}
	if !reserved(r.State) {
		delete(e.current, r.ID)
		e.requestCleanup()
	}
}

func (e *Engine) workPolicy(now time.Time) mirrors.WorkPolicy {
	e.mu.Lock()
	defer e.mu.Unlock()
	policy := mirrors.WorkPolicy{Sources: e.cfg.Current().Sources, Now: now}
	for _, r := range e.current {
		if r.State == "queued" && r.Progress.Phase == "waiting_space" {
			policy.Waiting = append(policy.Waiting, r.SourceID)
		}
	}
	return policy
}

func (e *Engine) requestCleanup() {
	select {
	case e.cleanupWake <- struct{}{}:
	default:
	}
}

func (e *Engine) Get(id int64) (*Run, error) {
	return e.GetContext(e.ctx, id)
}

// liveCopy returns a copy of a reserved run with its current elapsed budget,
// or nil when the run is not held in memory. Callers hold e.mu.
func (e *Engine) liveCopy(id int64) *Run {
	r := e.current[id]
	if r == nil {
		return nil
	}
	out := cloneRun(r)
	if execution := e.working[id]; execution != nil {
		out.ElapsedSeconds = execution.elapsed()
	}
	return out
}

func (e *Engine) GetContext(ctx context.Context, id int64) (*Run, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	e.mu.Lock()
	live := e.liveCopy(id)
	e.mu.Unlock()
	if live != nil {
		return live, nil
	}
	var r Run
	if err := e.store.RunContext(ctx, id, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (e *Engine) List(state, source string, limit, offset int, filters ...database.RunFilter) ([]*Run, int, error) {
	return e.ListContext(e.ctx, state, source, limit, offset, filters...)
}

func (e *Engine) ListContext(
	ctx context.Context,
	state, source string,
	limit, offset int,
	filters ...database.RunFilter,
) ([]*Run, int, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	records, total, err := e.store.ListRunsContext(ctx, state, source, limit, offset, filters...)
	if err != nil {
		return nil, 0, err
	}
	out, err := e.projectRuns(records)
	return out, total, err
}

// Latest includes old active tasks and quiet sources even after many newer runs.
func (e *Engine) Latest(ctx context.Context) ([]*Run, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	sources := e.cfg.Current().Sources
	ids := make([]string, len(sources))
	for i, source := range sources {
		ids[i] = source.ID
	}
	records, err := e.store.LatestRunsContext(ctx, ids)
	if err != nil {
		return nil, err
	}
	out, err := e.projectRuns(records)
	slices.SortFunc(out, func(a, b *Run) int { return cmp.Compare(b.ID, a.ID) })
	return out, err
}

// projectRuns decodes stored runs and replaces reserved ones with their
// newer in-memory state.
func (e *Engine) projectRuns(records [][]byte) ([]*Run, error) {
	out := []*Run{}
	for _, document := range records {
		r := &Run{}
		if err := json.Unmarshal(document, r); err != nil {
			return nil, err
		}
		e.mu.Lock()
		if live := e.liveCopy(r.ID); live != nil {
			r = live
		}
		e.mu.Unlock()
		out = append(out, r)
	}
	return out, nil
}

// dispatch starts queued runs while their source type has a free slot. Each
// started run executes in its own goroutine; types never share slots.
func (e *Engine) dispatch() {
	for e.ctx.Err() == nil {
		if e.mirror.Busy() {
			e.markWaitingForDownloads()
			if !e.sleep(busyPollInterval) {
				return
			}
			continue
		}
		e.mu.Lock()
		// Close cancels the context before interrupting working runs under
		// e.mu; never start a run it can no longer see.
		if e.ctx.Err() != nil {
			e.mu.Unlock()
			return
		}
		r := e.dequeueLocked()
		if r == nil {
			e.mu.Unlock()
			select {
			case <-e.ctx.Done():
				return
			case <-e.wake:
			case <-time.After(idlePollInterval):
			}
			continue
		}
		if admitErr := e.admitSync(r.ID); admitErr != nil {
			e.waitForSpaceLocked(r, admitErr)
			e.mu.Unlock()
			e.requestCleanup()
			if !e.sleep(busyPollInterval) {
				return
			}
			continue
		}
		if r.BudgetSeconds-r.ElapsedSeconds <= 0 {
			e.expireLocked(r)
			e.mirror.EndSync(r.ID)
			e.mu.Unlock()
			continue
		}
		e.startLocked(r)
		e.mu.Unlock()
	}
}

// sleep waits for d and reports false when the engine is closing.
func (e *Engine) sleep(d time.Duration) bool {
	select {
	case <-e.ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// markWaitingForDownloads keeps queued runs queued while public downloads
// use most of the file capacity.
func (e *Engine) markWaitingForDownloads() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range e.pending {
		if r.State != "queued" || r.Progress.Phase == "waiting_downloads" {
			continue
		}
		r.Progress.Phase = "waiting_downloads"
		e.event(r, "公开下载繁忙，保持排队；负载降低后自动执行")
		if err := e.save(r); err != nil {
			slog.Error("save download wait", "error", err)
		}
	}
}

// runningLocked counts executions per source type. Callers hold e.mu.
func (e *Engine) runningLocked() map[string]int {
	counts := map[string]int{}
	for id := range e.working {
		counts[e.current[id].Type]++
	}
	return counts
}

// dequeueLocked removes and returns the oldest runnable queued run. Runs
// whose source changed in YAML are stopped instead. Callers hold e.mu.
func (e *Engine) dequeueLocked() *Run {
	if len(e.pending) == 0 {
		return nil
	}
	current := e.cfg.Current()
	running := e.runningLocked()
	kept := e.pending[:0]
	var chosen *Run
	for i, candidate := range e.pending {
		if chosen != nil {
			kept = append(kept, e.pending[i:]...)
			break
		}
		if candidate.State != "queued" || e.working[candidate.ID] != nil {
			continue
		}
		if conflict := sourceConflict(candidate, current.Sources); conflict != "" {
			e.stopQueued(candidate, conflict)
			continue
		}
		if running[candidate.Type] >= current.Settings.Concurrency.Limit(candidate.Type) {
			kept = append(kept, candidate)
			continue
		}
		chosen = candidate
	}
	clear(e.pending[len(kept):])
	e.pending = kept
	return chosen
}

// waitForSpaceLocked requeues r when admission was refused. A failed
// capacity probe blocks admission without consuming the task, like a
// confirmed low-space condition. Callers hold e.mu.
func (e *Engine) waitForSpaceLocked(r *Run, admitErr error) {
	message := "镜像盘可用空间不足，保持排队；释放空间后自动继续"
	if !errors.Is(admitErr, mirrors.ErrLowSpace) {
		slog.Error("check mirror capacity before admission", "run", r.ID, "error", admitErr)
		message = "镜像盘容量探测失败，保持排队；恢复后自动继续"
	}
	if r.Progress.Phase != "waiting_space" {
		r.Progress.Phase = "waiting_space"
		e.event(r, message)
		if err := e.save(r); err != nil {
			slog.Error("save space wait", "error", err)
		}
	}
	e.pending = append(e.pending, r)
}

// expireLocked ends a queued run whose budget was spent before this
// execution. Callers hold e.mu.
func (e *Engine) expireLocked(r *Run) {
	r.State = "timed_out"
	r.EndedAt = time.Now().UTC()
	r.ErrorCode = "TASK_TIMEOUT"
	if err := e.save(r); err != nil {
		r.State = "interrupted"
		r.ErrorCode = "PERSISTENCE_FAILED"
		r.Error = "任务结果保存失败，任务目录仍保留"
		return
	}
	delete(e.current, r.ID)
	e.requestCleanup()
}

// startLocked marks an admitted run as working and executes it in a new
// goroutine within its remaining budget. Callers hold e.mu, so the slot is
// taken before the dispatcher looks at the queue again.
func (e *Engine) startLocked(r *Run) {
	remaining := time.Duration((r.BudgetSeconds - r.ElapsedSeconds) * float64(time.Second))
	ctx, stopDeadline := context.WithTimeout(e.ctx, remaining)
	ctx, cancel := context.WithCancelCause(ctx)
	execution := &active{cancel: cancel, start: time.Now(), base: r.ElapsedSeconds}
	e.working[r.ID] = execution
	r.State = "running"
	r.Progress.Phase = "prepare"
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now().UTC()
	}
	e.event(r, "开始执行")
	if err := e.save(r); err != nil {
		slog.Error("save run", "error", err)
		cancel(err)
	}
	snapshot := r.Snapshot
	e.wg.Go(func() {
		err := e.execute(ctx, r.ID, snapshot)
		if cause := context.Cause(ctx); cause != nil && err != nil {
			err = cause
		}
		stopDeadline()
		cancel(nil)
		e.finish(r, execution, err)
	})
}

// finish persists the outcome of an execution and frees its slot.
func (e *Engine) finish(r *Run, execution *active, err error) {
	e.mirror.EndSync(r.ID)
	defer e.wakeDispatcher()

	e.mu.Lock()
	defer e.mu.Unlock()
	r.ElapsedSeconds = execution.elapsed()
	e.applyOutcome(r, execution.intent, err)
	if Terminal(r.State) {
		r.EndedAt = time.Now().UTC()
	}
	e.event(r, "任务状态: "+r.State)
	if err := e.save(r); err != nil {
		slog.Error("finalize run", "error", err)
		// Keep the destination reserved and the failure visible when storage
		// cannot persist a terminal result. Never silently drop the live run.
		r.State = "interrupted"
		r.ErrorCode = "PERSISTENCE_FAILED"
		r.Error = "任务结果保存失败，请修复数据库存储后继续或取消"
	}
	delete(e.working, r.ID)
	if !reserved(r.State) {
		delete(e.current, r.ID)
		e.requestCleanup()
	}
}

// applyOutcome maps an execution result to the run state. A pause, cancel
// or shutdown request (intent) takes precedence over the executor error.
func (e *Engine) applyOutcome(r *Run, intent string, err error) {
	switch {
	case err == nil:
		r.State = "succeeded"
		r.Progress.Phase = "complete"
		r.Progress.Percent = new(float64(100))
	case intent != "":
		r.State = intent
		if intent == "interrupted" {
			r.ErrorCode = "SERVICE_INTERRUPTED"
			r.Error = "服务已停止，可使用剩余预算继续"
		}
	case errors.Is(err, context.DeadlineExceeded):
		r.State = "timed_out"
		r.ErrorCode = "TASK_TIMEOUT"
		r.Error = "累计执行时间达到任务上限"
	case errors.Is(err, mirrors.ErrLowSpace):
		r.State = "failed"
		r.ErrorCode = "DISK_SPACE"
		r.Error = "镜像盘可用空间低于安全余量；任务已停止，上一发布代保持可用"
	case e.ctx.Err() != nil:
		r.State = "interrupted"
		r.ErrorCode = "SERVICE_INTERRUPTED"
		r.Error = "服务已停止"
	default:
		failure := syncer.AsFailure(err)
		r.State = "failed"
		r.ErrorCode = failure.Code
		r.Error = failure.Message
	}
}

// execute prepares the work tree and tries the source's candidates in
// rounds, publishing after the first successful attempt. Transient failures
// retry the whole round with exponential delay until RetryCount is spent.
func (e *Engine) execute(ctx context.Context, id int64, snapshot Snapshot) error {
	source := snapshot.Source
	if err := e.mirror.CollectContext(ctx, time.Now()); err != nil {
		return err
	}
	work, reused, err := e.mirror.PrepareReuse(ctx, source, id)
	if err != nil {
		return err
	}
	if reused > 0 {
		e.mu.Lock()
		e.event(e.current[id], fmt.Sprintf("复用任务 #%d 保留的未完成同步目录", reused))
		err = e.save(e.current[id])
		e.mu.Unlock()
		if err != nil {
			return err
		}
	}
	candidates := source.Candidates()
	if source.Type == "github_release" {
		candidates = []string{source.GitHubRelease.Repository}
	}
	if len(candidates) == 0 {
		return errors.New("没有可用来源")
	}
	e.mu.Lock()
	first := e.current[id].CandidateIndex
	e.mu.Unlock()
	if first >= len(candidates) {
		first = 0
	}
	// Any transient candidate failure in a round justifies retrying the round,
	// even when the last mirror reported a permanent source-local error.
	roundRetryable := false
	for index := first; ; index++ {
		if err := e.waitRetry(ctx, id); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		attemptErr, secrets, err := e.attempt(ctx, id, snapshot, work, index, candidates[index])
		if err != nil {
			return err
		}
		if attemptErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		failure := syncer.AsFailure(attemptErr)
		failure.Message = redact(failure.Message, secrets...)
		roundRetryable = roundRetryable || failure.Retryable
		rsync := source.Type == "rsync"
		// Source-local permanent failures can still use another mirror.
		if rsync && (failure.Retryable || failure.SourceFailure) && index+1 < len(candidates) {
			continue
		}
		if !failure.Retryable && !(rsync && failure.SourceFailure && roundRetryable) {
			return failure
		}
		if err := e.scheduleRetry(id, snapshot, failure, len(candidates)); err != nil {
			return err
		}
		roundRetryable = false
		index = -1
	}
}

// attempt runs the executor once against candidate and publishes the work
// tree on success. attemptErr is the executor or publication failure; err
// reports a local persistence failure that ends the run.
func (e *Engine) attempt(
	ctx context.Context,
	id int64,
	snapshot Snapshot,
	work string,
	index int,
	candidate string,
) (attemptErr error, secrets []string, err error) {
	source := snapshot.Source
	e.mu.Lock()
	r := e.current[id]
	r.CandidateIndex = index
	r.Progress.Phase = "scan"
	r.Progress.Percent = nil
	r.Progress.ETA = nil
	attempt := Attempt{Number: len(r.Attempts) + 1, Candidate: candidate, StartedAt: time.Now().UTC()}
	r.Attempts = append(r.Attempts, attempt)
	r.Progress.ReceivedBytes, r.Progress.ReceivedIncomplete = receivedBytes(r.Attempts)
	e.event(r, "尝试来源: "+candidate)
	err = e.save(r)
	e.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}

	log, err := e.openAttemptLog(id, attempt.Number)
	if err != nil {
		return nil, nil, err
	}
	// Credentials are read at execution time; snapshots never contain them.
	github := e.cfg.Current().Settings.GitHub
	settings := snapshot.Settings
	settings.GitHub.Proxy = github.Proxy
	secrets = append([]string{github.Token}, proxySecrets(github.Proxy)...)
	writer := &redactedWriter{writer: log, secrets: secrets}
	task := syncer.Task{Source: source, Settings: settings, Directory: work, Candidate: candidate, Token: github.Token}
	attemptErr = e.runner.Run(ctx, task, e.progressReporter(id), writer)
	closeErr := errors.Join(writer.Flush(), log.Close())
	if attemptErr == nil {
		attemptErr = closeErr
	}
	if attemptErr == nil {
		attemptErr = e.publish(ctx, source, id)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	r = e.current[id]
	last := &r.Attempts[len(r.Attempts)-1]
	last.EndedAt = time.Now().UTC()
	if attemptErr != nil {
		failure := syncer.AsFailure(attemptErr)
		last.ErrorCode = failure.Code
		last.Error = redact(failure.Message, secrets...)
		last.ExitCode = failure.ExitCode
		if execution := e.working[id]; execution != nil && execution.intent != "" {
			last.ErrorCode = strings.ToUpper(execution.intent)
			last.Error = "执行已停止: " + execution.intent
		}
	}
	return attemptErr, secrets, e.save(r)
}

func (e *Engine) openAttemptLog(id int64, attempt int) (*os.File, error) {
	dir := filepath.Join(e.data, "logs", fmt.Sprint(id))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	name := filepath.Join(dir, fmt.Sprint(attempt)+".log")
	return os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
}

// progressReporter stores executor progress on the live run. Percent and
// ETA are only meaningful while transferring.
func (e *Engine) progressReporter(id int64) func(syncer.Progress) {
	return func(progress syncer.Progress) {
		if progress.Phase != "transfer" && progress.Phase != "complete" {
			progress.Percent = nil
			progress.ETA = nil
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		r := e.current[id]
		if r == nil {
			return
		}
		if progress.ReceivedBytes != nil {
			r.Attempts[len(r.Attempts)-1].ReceivedBytes = new(*progress.ReceivedBytes)
		}
		progress.ReceivedBytes, progress.ReceivedIncomplete = receivedBytes(r.Attempts)
		r.Progress = progress
	}
}

// publish swaps the finished work tree in. Inconsistent repository data is an
// upstream failure that may succeed on retry or from another mirror.
func (e *Engine) publish(ctx context.Context, source config.Source, id int64) error {
	e.mu.Lock()
	r := e.current[id]
	r.Progress.Phase = "publish"
	r.Progress.Percent, r.Progress.ETA = nil, nil
	e.mu.Unlock()
	_, err := e.mirror.Publish(ctx, source, id)
	if _, inconsistent := errors.AsType[*mirrors.ValidationError](err); inconsistent {
		return &syncer.Failure{
			Code:          "UPSTREAM_INCONSISTENT",
			Message:       err.Error(),
			Retryable:     true,
			SourceFailure: true,
		}
	}
	return err
}

// scheduleRetry records the next retry round or returns the final failure
// when the retry allowance is spent.
func (e *Engine) scheduleRetry(id int64, snapshot Snapshot, failure *syncer.Failure, candidates int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.current[id]
	allowed := int(snapshot.Settings.RetryCount)
	if r.RetryCount >= allowed {
		if snapshot.Source.Type == "rsync" {
			return &syncer.Failure{
				Code:    "SOURCE_EXHAUSTED",
				Message: fmt.Sprintf("所有 %d 个来源均失败，重试次数已用尽；最近原因: %s", candidates, failure.Message),
			}
		}
		return failure
	}
	r.RetryCount++
	// 5, 10, 20, 40 and then at most 80 seconds with the default base delay.
	delay := e.retryDelay * time.Duration(1<<min(r.RetryCount-1, maxRetryDoublings))
	r.RetryAt = time.Now().UTC().Add(delay)
	if failure.RetryAt.After(r.RetryAt) {
		// An upstream rate limit names its own reset time.
		r.RetryAt = failure.RetryAt
		delay = time.Until(r.RetryAt)
	}
	r.CandidateIndex = 0
	received, incomplete := receivedBytes(r.Attempts)
	r.Progress = syncer.Progress{Phase: "retry", ReceivedBytes: received, ReceivedIncomplete: incomplete}
	e.event(r, fmt.Sprintf("网络或上游临时失败，%s 后自动重试（%d/%d）", delay, r.RetryCount, allowed))
	return e.save(r)
}

func (e *Engine) waitRetry(ctx context.Context, id int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	at := e.current[id].RetryAt
	e.mu.Unlock()
	if at.IsZero() {
		return nil
	}
	timer := time.NewTimer(max(0, time.Until(at)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.current[id].RetryAt = time.Time{}
	return e.save(e.current[id])
}

// proxySecrets lists the proxy URL and its credentials for log redaction.
func proxySecrets(proxy string) []string {
	secrets := []string{proxy}
	if proxyURL, err := config.ParseProxyURL(proxy); err == nil && proxyURL != nil && proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		secrets = append(secrets, proxyURL.User.String(), password)
	}
	return secrets
}

// redact replaces every secret in text and strips control characters.
func redact(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	return syncer.CleanLog(text)
}

// Keep possible secret prefixes between writes, even when the producer splits
// a credential across chunks. Pending storage is bounded by the longest secret.
type redactedWriter struct {
	writer  io.Writer
	secrets []string
	pending []byte
}

func (w *redactedWriter) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	return len(p), w.flush(false)
}

func (w *redactedWriter) Flush() error { return w.flush(true) }

func (w *redactedWriter) flush(final bool) error {
	var out strings.Builder
	i := 0
	text := string(w.pending)
	for i < len(w.pending) {
		rest := text[i:]
		matched, partial := "", false
		for _, secret := range w.secrets {
			if secret == "" {
				continue
			}
			if strings.HasPrefix(rest, secret) && len(secret) > len(matched) {
				matched = secret
			}
			if !final && len(rest) < len(secret) && strings.HasPrefix(secret, rest) {
				partial = true
			}
		}
		if partial {
			break
		}
		if matched != "" {
			out.WriteString("[REDACTED]")
			i += len(matched)
			continue
		}
		if !final && !utf8.FullRune(w.pending[i:]) {
			break
		}
		_, n := utf8.DecodeRune(w.pending[i:])
		out.Write(w.pending[i : i+n])
		i += n
	}
	if _, err := io.WriteString(w.writer, syncer.CleanLog(out.String())); err != nil {
		return err
	}
	w.pending = append(w.pending[:0], w.pending[i:]...)
	return nil
}

func (e *Engine) Schedules() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, id := range e.scheduleIDs {
		e.cron.Remove(id)
	}
	clear(e.scheduleIDs)
	for _, s := range e.cfg.Current().Sources {
		if !s.Enabled || !s.Schedule.Enabled {
			continue
		}
		entry, err := e.cron.AddFunc(s.Schedule.Cron, func() {
			if _, err := e.Enqueue(s.ID, "schedule"); err != nil {
				slog.Info("schedule skipped", "source", s.ID, "reason", err)
			}
		})
		if err != nil {
			return err
		}
		e.scheduleIDs[s.ID] = entry
	}
	return nil
}

func (e *Engine) ScheduleInfo() map[string]time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]time.Time{}
	for id, entry := range e.scheduleIDs {
		out[id] = e.cron.Entry(entry).Next
	}
	return out
}

// Log reads at most logReadLimit bytes of an attempt log from offset and
// returns the text with the offset of the next read.
func (e *Engine) Log(id int64, attempt int, offset int64) (string, int64, error) {
	if offset < 0 {
		return "", 0, errors.New("offset 不能为负")
	}
	file, size, err := e.LogSnapshot(id, attempt)
	if errors.Is(err, errLogCleared) {
		return errLogCleared.Error(), offset, nil
	}
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return "", 0, err
	}
	chunk, err := io.ReadAll(io.LimitReader(file, min(logReadLimit, max(0, size-offset))))
	// Do not consume a partial UTF-8 rune at the byte budget or a live append.
	if len(chunk) > 0 {
		start := len(chunk) - 1
		for start > 0 && !utf8.RuneStart(chunk[start]) {
			start--
		}
		if !utf8.FullRune(chunk[start:]) {
			chunk = chunk[:start]
		}
	}
	return syncer.CleanLog(string(chunk)), offset + int64(len(chunk)), err
}

var errLogCleared = errors.New("日志已清理；状态和错误摘要仍保留。")

// LogSnapshot opens the selected attempt and fixes its download length before
// releasing the cleanup lock. Logs are already sanitized by redactedWriter.
// The caller owns the file and must limit reads to size to exclude later writes.
func (e *Engine) LogSnapshot(id int64, attempt int) (file *os.File, size int64, err error) {
	e.logMu.RLock()
	defer e.logMu.RUnlock()
	var r Run
	if err = e.store.Run(id, &r); err != nil {
		return nil, 0, err
	}
	if attempt < 1 || attempt > len(r.Attempts) {
		return nil, 0, errors.New("尝试编号不存在")
	}
	if r.Attempts[attempt-1].LogCleared {
		return nil, 0, errLogCleared
	}
	file, err = os.Open(filepath.Join(e.data, "logs", fmt.Sprint(id), fmt.Sprint(attempt)+".log"))
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}

func (e *Engine) ClearLogs() (int, error) {
	return e.ClearLogsContext(e.ctx)
}

func (e *Engine) ClearLogsContext(ctx context.Context) (int, error) {
	if !e.clearMu.TryLock() {
		return 0, errors.New("已有日志清理正在进行")
	}
	defer e.clearMu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.ctx, cancel)
	defer cancel()
	defer stop()
	cleared := 0
	through, err := e.store.LastRunID(ctx)
	if err != nil {
		return 0, err
	}
	var after int64
	for {
		records, err := e.store.LogCleanupPage(ctx, after, through)
		if err != nil {
			return cleared, err
		}
		for _, document := range records {
			var r Run
			if err = json.Unmarshal(document, &r); err != nil {
				return cleared, err
			}
			if r.ID <= after || r.ID > through {
				return cleared, errors.New("历史任务编号无效，日志清理已停止")
			}
			after = r.ID
			removed, err := e.clearRunLogs(ctx, r)
			cleared += removed
			if err != nil {
				return cleared, err
			}
		}
		if len(records) < database.PageSize {
			break
		}
	}
	return cleared, nil
}

func (e *Engine) clearRunLogs(ctx context.Context, r Run) (cleared int, err error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	e.logMu.Lock()
	defer e.logMu.Unlock()
	e.mu.Lock()
	if current := e.current[r.ID]; current != nil {
		r = *cloneRun(current)
	}
	if !Terminal(r.State) || e.working[r.ID] != nil {
		e.mu.Unlock()
		return 0, nil
	}
	done := make(chan struct{})
	e.clearing[r.ID] = done
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		if err == nil {
			if current := e.current[r.ID]; current != nil {
				current.Attempts = r.Attempts
			}
		}
		delete(e.clearing, r.ID)
		close(done)
		e.mu.Unlock()
	}()
	for i := range r.Attempts {
		if err = ctx.Err(); err != nil {
			break
		}
		if r.Attempts[i].LogCleared {
			continue
		}
		name := filepath.Join(e.data, "logs", fmt.Sprint(r.ID), fmt.Sprint(i+1)+".log")
		if err = e.removeLog(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			break
		}
		err = nil
		r.Attempts[i].LogCleared = true
		cleared++
	}
	if cleared != 0 {
		// Persist completed removals even when cancellation or a later removal
		// fails. Missing files can be marked again after a persistence failure.
		saveErr := e.save(&r)
		if saveErr != nil {
			return cleared, errors.Join(err, saveErr)
		}
		e.mu.Lock()
		if current := e.current[r.ID]; current != nil {
			current.Attempts = r.Attempts
		}
		e.mu.Unlock()
	}
	return cleared, err
}

// NotificationChange serializes activation with new task creation and YAML edits.
func (e *Engine) NotificationChange(operation func() error) error {
	e.configMu.Lock()
	defer e.configMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	return operation()
}

// receivedBytes totals only explicitly reported attempt counters. An unknown
// attempt must remain visible as incomplete, including after pause or restart.
func receivedBytes(attempts []Attempt) (*int64, bool) {
	var total *int64
	incomplete := false
	for _, attempt := range attempts {
		if attempt.ReceivedBytes == nil {
			incomplete = true
			continue
		}
		if total == nil {
			total = new(int64(0))
		}
		*total += *attempt.ReceivedBytes
	}
	return total, incomplete
}
