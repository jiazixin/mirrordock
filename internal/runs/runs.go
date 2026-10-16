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
func Terminal(state string) bool {
	return slices.Contains([]string{"succeeded", "failed", "timed_out", "cancelled"}, state)
}
func reserved(state string) bool {
	return !slices.Contains([]string{"succeeded", "failed", "timed_out", "cancelled"}, state)
}

type active struct {
	cancel context.CancelCauseFunc
	start  time.Time
	base   float64
	intent string
}
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

func New(cfg *config.Manager, store *database.Store, m *mirrors.Manager, runner syncer.Runner, location *time.Location) (*Engine, error) {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{cfg: cfg, store: store, mirror: m, runner: runner, data: config.Resolve(cfg.File(), cfg.Startup().Path), ctx: ctx, cancel: cancel, current: map[int64]*Run{}, working: map[int64]*active{}, wake: make(chan struct{}, 2), cleanupWake: make(chan struct{}, 1), location: location, retryDelay: 5 * time.Second, scheduleIDs: map[string]cron.EntryID{}}
	e.admitSync, e.storageCheck, e.checkpointInterval = m.BeginSync, m.CheckSpace, 5*time.Second
	e.clearing, e.removeLog = make(map[int64]chan struct{}), os.Remove

	sources := cfg.Current().Sources
	var after int64
	for {
		records, err := store.RecoverableRuns(ctx, after)
		if err != nil {
			cancel()
			return nil, err
		}
		for _, b := range records {
			var r Run
			if err := json.Unmarshal(b, &r); err != nil {
				cancel()
				return nil, err
			}
			after = r.ID
			previousState := r.State
			if r.State == "running" || r.State == "pausing" {
				if r.State == "pausing" {
					r.State = "paused"
				} else {
					r.State = "interrupted"
				}
				r.ErrorCode = "SERVICE_INTERRUPTED"
				r.Error = "服务中断；保留剩余预算"
				e.event(&r, r.Error)
				if len(r.Attempts) > 0 {
					r.Attempts[len(r.Attempts)-1].EndedAt = time.Now().UTC()
					r.Attempts[len(r.Attempts)-1].ErrorCode = r.ErrorCode
				}
			}
			conflict := sourceConflict(&r, sources)
			if conflict != "" {
				// YAML may change while stopped. Never run a snapshot against a
				// removed, retyped, moved or disabled source after restart.
				stopForSource(&r, conflict)
			}
			if r.State == "interrupted" && r.ErrorCode == "SERVICE_INTERRUPTED" && conflict == "" && r.ElapsedSeconds < r.BudgetSeconds {
				r.State = "queued"
				r.EndedAt = time.Time{}
				r.Error, r.ErrorCode = "", ""
				e.event(&r, "服务恢复，自动排队继续；沿用累计预算及重试次数")
			}
			if r.State == "interrupted" && r.ErrorCode == "SERVICE_INTERRUPTED" && r.ElapsedSeconds >= r.BudgetSeconds {
				r.State, r.ErrorCode, r.Error = "timed_out", "TASK_TIMEOUT", "累计执行时间达到任务上限"
				r.EndedAt = time.Now().UTC()
			}
			if r.State != previousState {
				if err := store.SaveRun(r.ID, r.State, r); err != nil {
					cancel()
					return nil, err
				}
			}
			if reserved(r.State) {
				e.current[r.ID] = &r
			}
			if r.State == "queued" {
				e.pending = append(e.pending, &r)
			}
		}
		if len(records) < 100 {
			break
		}
	}
	slices.SortFunc(e.pending, func(a, b *Run) int { return cmp.Compare(a.ID, b.ID) })
	e.cron = cron.New(cron.WithLocation(location), cron.WithChain(cron.Recover(cron.DefaultLogger)))
	if err := e.Schedules(); err != nil {
		cancel()
		return nil, err
	}
	return e, nil
}
func (e *Engine) Start() {
	e.cron.Start()
	for range 2 {
		e.wg.Go(e.worker)
	}
	e.wg.Go(func() {
		cleanup := time.NewTicker(5 * time.Minute)
		defer cleanup.Stop()
		for {
			var now time.Time
			select {
			case <-e.ctx.Done():
				return
			case now = <-cleanup.C:
			case <-e.cleanupWake:
				now = time.Now()
			}
			ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
			if err := e.mirror.CleanupWork(ctx); err != nil {
				slog.Error("clean terminal work", "error", err)
			}
			if err := e.mirror.CollectContext(ctx, now); err != nil {
				slog.Error("collect generations", "error", err)
			}
			cancel()
		}
	})
	// Capacity monitoring and persistence must not wait for a large directory
	// cleanup to finish.
	e.wg.Go(func() {
		ticker := time.NewTicker(e.checkpointInterval)
		defer ticker.Stop()
		for {
			select {
			case <-e.ctx.Done():
				return
			case <-ticker.C:
				e.mu.Lock()
				spaceErr := e.storageCheck()
				if spaceErr != nil && !errors.Is(spaceErr, mirrors.ErrLowSpace) {
					slog.Error("check mirror capacity", "error", spaceErr)
				}
				for id, a := range e.working {
					r := e.current[id]
					// Publication only renames the finished work tree on the same
					// filesystem; stopping it would discard a completed sync.
					if errors.Is(spaceErr, mirrors.ErrLowSpace) && r.Progress.Phase != "publish" {
						a.cancel(spaceErr)
					}
					r.ElapsedSeconds = a.base + time.Since(a.start).Seconds()
					if err := e.store.SaveRun(id, r.State, r); err != nil {
						slog.Error("save progress", "error", err)
					}
				}
				e.mu.Unlock()
			}
		}
	})
	e.requestCleanup()
}
func (e *Engine) Close() {
	e.cancel()
	<-e.cron.Stop().Done()
	e.mu.Lock()
	for _, a := range e.working {
		if a.intent == "" {
			a.intent = "interrupted"
		}
		a.cancel(errors.New("service shutdown"))
	}
	e.mu.Unlock()
	e.wg.Wait()
}
func (e *Engine) event(r *Run, message string) {
	r.Events = append(r.Events, Event{At: time.Now().UTC(), Message: message})
}
func (e *Engine) save(r *Run) error { return e.store.SaveRun(r.ID, r.State, r) }
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
		i := slices.IndexFunc(c.Sources, func(s config.Source) bool { return s.ID == r.SourceID })
		if i < 0 || c.Sources[i].Dest != r.Dest || c.Sources[i].Type != r.Type {
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
	i := slices.IndexFunc(sources, func(s config.Source) bool { return s.ID == r.SourceID })
	switch {
	case i < 0:
		return sourceRemoved
	case sources[i].Type != r.Type || sources[i].Dest != r.Dest:
		return sourceChanged
	case !sources[i].Enabled:
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
	i := slices.IndexFunc(c.Sources, func(s config.Source) bool { return s.ID == sourceID })
	if i < 0 {
		return nil, errors.New("源不存在")
	}
	s := c.Sources[i]
	if !s.Enabled {
		return nil, errors.New("源已禁用")
	}
	if e.busySource(s.ID) {
		return nil, errors.New("该源已有活动、暂停或中断任务")
	}
	budget, _ := time.ParseDuration(c.Settings.TaskTimeout)
	c.Settings.GitHub.Token = ""
	c.Settings.GitHub.Proxy = ""
	c.Settings.Notifications = nil
	r := &Run{SourceID: s.ID, Name: s.Name, Type: s.Type, Dest: s.Dest, State: "queued", Trigger: trigger, CreatedAt: time.Now().UTC(), BudgetSeconds: budget.Seconds(), Progress: syncer.Progress{Phase: "prepare"}, Snapshot: Snapshot{Source: publicSource(s), Settings: c.Settings}}
	e.event(r, "任务已加入队列")
	id, err := e.store.CreateRun(s.ID, r.State, r.CreatedAt.Format(time.RFC3339Nano), r)
	if err != nil {
		return nil, err
	}
	r.ID = id
	e.current[id] = r
	e.pending = append(e.pending, r)
	select {
	case e.wake <- struct{}{}:
	default:
	}
	return cloneRun(r), nil
}
func cloneRun(r *Run) *Run {
	b, _ := json.Marshal(r)
	var out Run
	_ = json.Unmarshal(b, &out)
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
		if a := e.working[id]; a != nil {
			a.intent = "cancelled"
			a.cancel(errors.New("cancelled"))
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
		select {
		case e.wake <- struct{}{}:
		default:
		}
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

func (e *Engine) requestCleanup() {
	select {
	case e.cleanupWake <- struct{}{}:
	default:
	}
}
func (e *Engine) Get(id int64) (*Run, error) {
	return e.GetContext(e.ctx, id)
}

func (e *Engine) GetContext(ctx context.Context, id int64) (*Run, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	e.mu.Lock()
	if r := e.current[id]; r != nil {
		out := cloneRun(r)
		if a := e.working[id]; a != nil {
			out.ElapsedSeconds = a.base + time.Since(a.start).Seconds()
		}
		e.mu.Unlock()
		return out, nil
	}
	e.mu.Unlock()
	var r Run
	if err := e.store.RunContext(ctx, id, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
func (e *Engine) List(state, source string, limit, offset int, filters ...database.RunFilter) ([]*Run, int, error) {
	return e.ListContext(e.ctx, state, source, limit, offset, filters...)
}

func (e *Engine) ListContext(ctx context.Context, state, source string, limit, offset int, filters ...database.RunFilter) ([]*Run, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
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

func (e *Engine) projectRuns(records [][]byte) ([]*Run, error) {
	out := []*Run{}
	for _, b := range records {
		var r Run
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		e.mu.Lock()
		if live := e.current[r.ID]; live != nil {
			r = *cloneRun(live)
			if a := e.working[r.ID]; a != nil {
				r.ElapsedSeconds = a.base + time.Since(a.start).Seconds()
			}
		}
		e.mu.Unlock()
		out = append(out, &r)
	}
	return out, nil
}
func (e *Engine) worker() {
	for {
		if e.ctx.Err() != nil {
			return
		}
		if e.mirror.Busy() {
			e.mu.Lock()
			for _, r := range e.pending {
				if r.State == "queued" && r.Progress.Phase != "waiting_downloads" {
					r.Progress.Phase = "waiting_downloads"
					e.event(r, "公开下载繁忙，保持排队；负载降低后自动执行")
					if err := e.save(r); err != nil {
						slog.Error("save download wait", "error", err)
					}
				}
			}
			e.mu.Unlock()
			select {
			case <-e.ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		e.mu.Lock()
		var r *Run
		var sources []config.Source
		for len(e.pending) > 0 {
			candidate := e.pending[0]
			e.pending = e.pending[1:]
			if candidate.State != "queued" || e.working[candidate.ID] != nil {
				continue
			}
			if sources == nil {
				sources = e.cfg.Current().Sources
			}
			if conflict := sourceConflict(candidate, sources); conflict != "" {
				e.stopQueued(candidate, conflict)
				continue
			}
			r = candidate
			break
		}
		if r == nil {
			e.mu.Unlock()
			select {
			case <-e.ctx.Done():
				return
			case <-e.wake:
			case <-time.After(500 * time.Millisecond):
			}
			continue
		}
		if admitErr := e.admitSync(r.ID); admitErr != nil {
			message := "镜像盘可用空间不足，保持排队；释放空间后自动继续"
			if !errors.Is(admitErr, mirrors.ErrLowSpace) {
				// A failed capacity probe blocks admission without consuming the
				// task: keep it queued, unlike a confirmed low-space condition.
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
			e.mu.Unlock()
			e.requestCleanup()
			select {
			case <-e.ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		remaining := r.BudgetSeconds - r.ElapsedSeconds
		if remaining <= 0 {
			r.State = "timed_out"
			r.EndedAt = time.Now().UTC()
			r.ErrorCode = "TASK_TIMEOUT"
			if err := e.save(r); err == nil {
				delete(e.current, r.ID)
				e.requestCleanup()
			} else {
				r.State = "interrupted"
				r.ErrorCode = "PERSISTENCE_FAILED"
				r.Error = "任务结果保存失败，任务目录仍保留"
			}
			e.mirror.EndSync(r.ID)
			e.mu.Unlock()
			continue
		}
		ctx, stopDeadline := context.WithTimeout(e.ctx, time.Duration(remaining*float64(time.Second)))
		ctx, cancel := context.WithCancelCause(ctx)
		a := &active{cancel: cancel, start: time.Now(), base: r.ElapsedSeconds}
		e.working[r.ID] = a
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
		e.mu.Unlock()
		err := e.execute(ctx, r.ID, snapshot)
		if cause := context.Cause(ctx); cause != nil && err != nil {
			err = cause
		}
		stopDeadline()
		cancel(nil)
		e.mirror.EndSync(r.ID)
		e.mu.Lock()
		r.ElapsedSeconds = a.base + time.Since(a.start).Seconds()
		switch {
		case err == nil:
			r.State = "succeeded"
			r.Progress.Phase = "complete"
			r.Progress.Percent = new(float64(100))
		case a.intent != "":
			r.State = a.intent
			if a.intent == "interrupted" {
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
			r.State = "failed"
			f := syncer.AsFailure(err)
			r.ErrorCode = f.Code
			r.Error = f.Message
		}
		if Terminal(r.State) {
			r.EndedAt = time.Now().UTC()
		}
		e.event(r, "任务状态: "+r.State)
		if err = e.save(r); err != nil {
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
		e.mu.Unlock()
	}
}
func (e *Engine) execute(ctx context.Context, id int64, snapshot Snapshot) error {
	s := snapshot.Source
	if err := e.mirror.CollectContext(ctx, time.Now()); err != nil {
		return err
	}
	work, err := e.mirror.Prepare(ctx, s, id)
	if err != nil {
		return err
	}
	candidates := s.Candidates()
	if s.Type == "github_release" {
		candidates = []string{s.GitHubRelease.Repository}
	}
	e.mu.Lock()
	first := e.current[id].CandidateIndex
	e.mu.Unlock()
	if first >= len(candidates) {
		first = 0
	}
	if len(candidates) == 0 {
		return errors.New("没有可用来源")
	}
	// Any transient candidate failure in a round justifies retrying the round,
	// even when the last mirror reported a permanent source-local error.
	roundRetryable := false
	for index := first; ; index++ {
		if err = e.waitRetry(ctx, id); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		candidate := candidates[index]
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
			return err
		}
		dir := filepath.Join(e.data, "logs", fmt.Sprint(id))
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		log, err := os.OpenFile(filepath.Join(dir, fmt.Sprint(attempt.Number)+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		githubSettings := e.cfg.Current().Settings.GitHub
		token := githubSettings.Token
		settings := snapshot.Settings
		settings.GitHub.Proxy = githubSettings.Proxy
		secrets := append([]string{token}, proxySecrets(githubSettings.Proxy)...)
		emit := func(p syncer.Progress) {
			if p.Phase != "transfer" && p.Phase != "complete" {
				p.Percent = nil
				p.ETA = nil
			}
			e.mu.Lock()
			defer e.mu.Unlock()
			if run := e.current[id]; run != nil {
				if p.ReceivedBytes != nil {
					run.Attempts[len(run.Attempts)-1].ReceivedBytes = new(*p.ReceivedBytes)
				}
				p.ReceivedBytes, p.ReceivedIncomplete = receivedBytes(run.Attempts)
				run.Progress = p
			}
		}
		writer := &redactedWriter{writer: log, secrets: secrets}
		err = e.runner.Run(ctx, syncer.Task{Source: s, Settings: settings, Directory: work, Candidate: candidate, Token: token}, emit, writer)
		closeErr := errors.Join(writer.Flush(), log.Close())
		if err == nil {
			err = closeErr
		}
		if err == nil {
			e.mu.Lock()
			r.Progress.Phase = "publish"
			r.Progress.Percent, r.Progress.ETA = nil, nil
			e.mu.Unlock()
			_, err = e.mirror.Publish(ctx, s, id)
			if _, ok := errors.AsType[*mirrors.ValidationError](err); ok {
				err = &syncer.Failure{Code: "UPSTREAM_INCONSISTENT", Message: err.Error(), Retryable: true, SourceFailure: true}
			}
		}
		e.mu.Lock()
		r = e.current[id]
		a := &r.Attempts[len(r.Attempts)-1]
		a.EndedAt = time.Now().UTC()
		if err != nil {
			f := syncer.AsFailure(err)
			a.ErrorCode = f.Code
			a.Error = redact(f.Message, secrets...)
			a.ExitCode = f.ExitCode
			if active := e.working[id]; active != nil && active.intent != "" {
				a.ErrorCode = strings.ToUpper(active.intent)
				a.Error = "执行已停止: " + active.intent
			}
		}
		saveErr := e.save(r)
		e.mu.Unlock()
		if saveErr != nil {
			return saveErr
		}
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		failure := syncer.AsFailure(err)
		failure.Message = redact(failure.Message, secrets...)
		roundRetryable = roundRetryable || failure.Retryable
		// Source-local permanent failures can still use another mirror.
		if s.Type == "rsync" && (failure.Retryable || failure.SourceFailure) && index+1 < len(candidates) {
			continue
		}
		if !failure.Retryable && !(s.Type == "rsync" && failure.SourceFailure && roundRetryable) {
			return failure
		}
		e.mu.Lock()
		r = e.current[id]
		if r.RetryCount >= int(snapshot.Settings.RetryCount) {
			e.mu.Unlock()
			if s.Type == "rsync" {
				return &syncer.Failure{Code: "SOURCE_EXHAUSTED", Message: fmt.Sprintf("所有 %d 个来源均失败，重试次数已用尽；最近原因: %s", len(candidates), failure.Message)}
			}
			return failure
		}
		r.RetryCount++
		delay := e.retryDelay * time.Duration(1<<min(r.RetryCount-1, 4))
		r.RetryAt = time.Now().UTC().Add(delay)
		if failure.RetryAt.After(r.RetryAt) {
			r.RetryAt = failure.RetryAt
			delay = time.Until(r.RetryAt)
		}
		r.CandidateIndex = 0
		roundRetryable = false
		received, incomplete := receivedBytes(r.Attempts)
		r.Progress = syncer.Progress{Phase: "retry", ReceivedBytes: received, ReceivedIncomplete: incomplete}
		e.event(r, fmt.Sprintf("网络或上游临时失败，%s 后自动重试（%d/%d）", delay, r.RetryCount, snapshot.Settings.RetryCount))
		err = e.save(r)
		e.mu.Unlock()
		if err != nil {
			return err
		}
		index = -1
	}
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
func proxySecrets(proxy string) []string {
	secrets := []string{proxy}
	if u, err := config.ParseProxyURL(proxy); err == nil && u != nil && u.User != nil {
		password, _ := u.User.Password()
		secrets = append(secrets, u.User.String(), password)
	}
	return secrets
}
func redact(v string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			v = strings.ReplaceAll(v, secret, "[REDACTED]")
		}
	}
	return syncer.CleanLog(v)
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
func (e *Engine) Log(id int64, attempt int, offset int64) (string, int64, error) {
	if offset < 0 {
		return "", 0, errors.New("offset 不能为负")
	}
	f, size, err := e.LogSnapshot(id, attempt)
	if errors.Is(err, errLogCleared) {
		return errLogCleared.Error(), offset, nil
	}
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return "", 0, err
	}
	b, err := io.ReadAll(io.LimitReader(f, min(128<<10, max(0, size-offset))))
	// Do not consume a partial UTF-8 rune at the byte budget or a live append.
	if len(b) > 0 {
		start := len(b) - 1
		for start > 0 && !utf8.RuneStart(b[start]) {
			start--
		}
		if !utf8.FullRune(b[start:]) {
			b = b[:start]
		}
	}
	return syncer.CleanLog(string(b)), offset + int64(len(b)), err
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
		for _, b := range records {
			var r Run
			if err = json.Unmarshal(b, &r); err != nil {
				return cleared, err
			}
			if r.ID <= after || r.ID > through {
				return cleared, errors.New("历史任务编号无效，日志清理已停止")
			}
			after = r.ID
			n, err := e.clearRunLogs(ctx, r)
			cleared += n
			if err != nil {
				return cleared, err
			}
		}
		if len(records) < 100 {
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
		p := filepath.Join(e.data, "logs", fmt.Sprint(r.ID), fmt.Sprint(i+1)+".log")
		if err = e.removeLog(p); err != nil && !errors.Is(err, os.ErrNotExist) {
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
