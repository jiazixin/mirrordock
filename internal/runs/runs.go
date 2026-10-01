package runs

import (
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
)

type Snapshot struct {
	Source   config.Source   `json:"source"`
	Settings config.Settings `json:"settings"`
}
type Attempt struct {
	Number     int       `json:"number"`
	Candidate  string    `json:"candidate"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at,omitzero"`
	ErrorCode  string    `json:"error_code"`
	Error      string    `json:"error"`
	ExitCode   int       `json:"exit_code"`
	LogCleared bool      `json:"log_cleared"`
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
	return slices.Contains([]string{"succeeded", "failed", "timed_out", "cancelled", "interrupted"}, state)
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
	mu              sync.Mutex
	configMu        sync.Mutex
	cfg             *config.Manager
	store           *database.Store
	mirror          *mirrors.Manager
	runner          syncer.Runner
	data            string
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	pending         []*Run
	current         map[int64]*Run
	working         map[int64]*active
	wake            chan struct{}
	cron            *cron.Cron
	retryDelay      time.Duration
	location        *time.Location
	scheduleIDs     map[string]cron.EntryID
	ConfigValidator func(config.Config) error
	ConfigApplied   func() error
}

func New(cfg *config.Manager, store *database.Store, m *mirrors.Manager, runner syncer.Runner, location *time.Location) (*Engine, error) {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{cfg: cfg, store: store, mirror: m, runner: runner, data: config.Resolve(cfg.File(), cfg.Startup().Path), ctx: ctx, cancel: cancel, current: map[int64]*Run{}, working: map[int64]*active{}, wake: make(chan struct{}, 2), location: location, retryDelay: 5 * time.Second, scheduleIDs: map[string]cron.EntryID{}}
	for _, state := range []string{"queued", "running", "pausing", "paused", "interrupted"} {
		records, _, err := store.ListRuns(state, "", 10000, 0)
		if err != nil {
			return nil, err
		}
		for _, b := range records {
			var r Run
			if err = json.Unmarshal(b, &r); err != nil {
				return nil, err
			}
			if r.State == "running" || r.State == "pausing" {
				r.State = "interrupted"
				r.ErrorCode = "SERVICE_INTERRUPTED"
				r.Error = "服务中断；可继续使用剩余预算"
				e.event(&r, r.Error)
				if len(r.Attempts) > 0 {
					r.Attempts[len(r.Attempts)-1].EndedAt = time.Now().UTC()
					r.Attempts[len(r.Attempts)-1].ErrorCode = r.ErrorCode
				}
				if err = store.SaveRun(r.ID, r.State, r); err != nil {
					return nil, err
				}
			}
			e.current[r.ID] = &r
			if r.State == "queued" {
				e.pending = append(e.pending, &r)
			}
		}
	}
	slices.SortFunc(e.pending, func(a, b *Run) int { return int(a.ID - b.ID) })
	e.cron = cron.New(cron.WithLocation(location), cron.WithChain(cron.Recover(cron.DefaultLogger)))
	if err := e.Schedules(); err != nil {
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
		cleanup := time.NewTicker(time.Hour)
		defer cleanup.Stop()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-e.ctx.Done():
				return
			case now := <-cleanup.C:
				if err := e.mirror.CollectContext(e.ctx, now); err != nil {
					slog.Error("collect generations", "error", err)
				}
			case <-ticker.C:
				e.mu.Lock()
				for id, a := range e.working {
					r := e.current[id]
					r.ElapsedSeconds = a.base + time.Since(a.start).Seconds()
					if err := e.store.SaveRun(id, r.State, r); err != nil {
						slog.Error("save progress", "error", err)
					}
				}
				e.mu.Unlock()
			}
		}
	})
}
func (e *Engine) Close() {
	e.cancel()
	e.cron.Stop()
	e.mu.Lock()
	for _, a := range e.working {
		a.intent = "interrupted"
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
		if i < 0 || c.Sources[i].Dest != r.Dest {
			return errors.New("活动、暂停或中断任务的源不能删除或变更 dest，请先取消任务")
		}
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
	e.mirror.Restrict(c.Sources)
	return e.Schedules()
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
	if err == nil && e.ConfigApplied != nil {
		err = e.ConfigApplied()
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	e.mirror.Restrict(e.cfg.Current().Sources)
	return e.Schedules()
}
func (e *Engine) Enqueue(sourceID, trigger string) (*Run, error) {
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
	r := &Run{SourceID: s.ID, Name: s.Name, Type: s.Type, Dest: s.Dest, State: "queued", Trigger: trigger, CreatedAt: time.Now().UTC(), BudgetSeconds: budget.Seconds(), Progress: syncer.Progress{Phase: "prepare"}, Snapshot: Snapshot{Source: s, Settings: c.Settings}}
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
	e.mu.Lock()
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
		if r.State != "paused" && r.State != "interrupted" {
			return errors.New("当前任务不可继续")
		}
		if r.ElapsedSeconds >= r.BudgetSeconds {
			return errors.New("累计预算已耗尽")
		}
		r.State = "queued"
		r.Error = ""
		r.ErrorCode = ""
		e.pending = append(e.pending, r)
		select {
		case e.wake <- struct{}{}:
		default:
		}
	case "cancel":
		if a := e.working[id]; a != nil {
			a.intent = "cancelled"
			a.cancel(errors.New("cancelled"))
		} else {
			r.State = "cancelled"
			r.EndedAt = time.Now().UTC()
			delete(e.current, id)
		}
	default:
		return errors.New("未知任务操作")
	}
	e.event(r, "操作: "+action)
	return e.save(r)
}
func (e *Engine) Get(id int64) (*Run, error) {
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
	if err := e.store.Run(id, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
func (e *Engine) List(state, source string, limit, offset int, filters ...database.RunFilter) ([]*Run, int, error) {
	records, total, err := e.store.ListRuns(state, source, limit, offset, filters...)
	if err != nil {
		return nil, 0, err
	}
	out := []*Run{}
	for _, b := range records {
		var r Run
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, 0, err
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
	return out, total, nil
}
func (e *Engine) worker() {
	for {
		if e.ctx.Err() != nil {
			return
		}
		if e.mirror.Busy() {
			select {
			case <-e.ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		e.mu.Lock()
		var r *Run
		for len(e.pending) > 0 {
			candidate := e.pending[0]
			e.pending = e.pending[1:]
			if candidate.State == "queued" {
				r = candidate
				break
			}
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
		remaining := r.BudgetSeconds - r.ElapsedSeconds
		if remaining <= 0 {
			r.State = "timed_out"
			r.EndedAt = time.Now().UTC()
			r.ErrorCode = "TASK_TIMEOUT"
			_ = e.save(r)
			delete(e.current, r.ID)
			e.mu.Unlock()
			continue
		}
		ctx, stopDeadline := context.WithTimeout(e.ctx, time.Duration(remaining*float64(time.Second)))
		ctx, cancel := context.WithCancelCause(ctx)
		a := &active{cancel: cancel, start: time.Now(), base: r.ElapsedSeconds}
		e.working[r.ID] = a
		r.State = "running"
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
		stopDeadline()
		cancel(nil)
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
		}
		e.mu.Unlock()
	}
}
func (e *Engine) execute(ctx context.Context, id int64, snapshot Snapshot) error {
	s := snapshot.Source
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
				run.Progress = p
			}
		}
		err = e.runner.Run(ctx, syncer.Task{Source: s, Settings: settings, Directory: work, Candidate: candidate, Token: token}, emit, &redactedWriter{writer: log, secrets: secrets})
		closeErr := log.Close()
		if err == nil {
			err = closeErr
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
			e.mu.Lock()
			progress := r.Progress
			e.mu.Unlock()
			progress.Phase = "publish"
			emit(progress)
			_, err = e.mirror.Publish(ctx, s, id)
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		failure := syncer.AsFailure(err)
		failure.Message = redact(failure.Message, secrets...)
		if !failure.Retryable {
			return failure
		}
		// Try Rsync fallbacks before spending another retry round.
		if s.Type == "rsync" && index+1 < len(candidates) {
			continue
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
		r.CandidateIndex = 0
		r.Progress = syncer.Progress{Phase: "retry"}
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

type redactedWriter struct {
	writer  io.Writer
	secrets []string
}

func (w *redactedWriter) Write(p []byte) (int, error) {
	_, err := io.WriteString(w.writer, redact(string(p), w.secrets...))
	return len(p), err
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
	r, err := e.Get(id)
	if err != nil {
		return "", 0, err
	}
	if attempt < 1 || attempt > len(r.Attempts) {
		return "", 0, errors.New("尝试编号不存在")
	}
	if r.Attempts[attempt-1].LogCleared {
		return "日志已清理；状态和错误摘要仍保留。", offset, nil
	}
	f, err := os.Open(filepath.Join(e.data, "logs", fmt.Sprint(id), fmt.Sprint(attempt)+".log"))
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	if offset < 0 {
		return "", 0, errors.New("offset 不能为负")
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return "", 0, err
	}
	b, err := io.ReadAll(io.LimitReader(f, 128<<10))
	return syncer.CleanLog(string(b)), offset + int64(len(b)), err
}

// LogSnapshot opens the selected attempt and fixes its download length before
// releasing the cleanup lock. Logs are already sanitized by redactedWriter.
// The caller owns the file and must limit reads to size to exclude later writes.
func (e *Engine) LogSnapshot(id int64, attempt int) (file *os.File, size int64, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var r Run
	if err = e.store.Run(id, &r); err != nil {
		return nil, 0, err
	}
	if attempt < 1 || attempt > len(r.Attempts) {
		return nil, 0, errors.New("尝试编号不存在")
	}
	if r.Attempts[attempt-1].LogCleared {
		return nil, 0, errors.New("日志已清理；状态和错误摘要仍保留。")
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
	e.mu.Lock()
	defer e.mu.Unlock()
	cleared := 0
	offset := 0
	for {
		records, total, err := e.store.ListRuns("", "", 100, offset)
		if err != nil {
			return cleared, err
		}
		for _, b := range records {
			var r Run
			if err = json.Unmarshal(b, &r); err != nil {
				return cleared, err
			}
			if !Terminal(r.State) {
				continue
			}
			if _, ok := e.working[r.ID]; ok {
				continue
			}
			changed := false
			for i := range r.Attempts {
				if r.Attempts[i].LogCleared {
					continue
				}
				p := filepath.Join(e.data, "logs", fmt.Sprint(r.ID), fmt.Sprint(i+1)+".log")
				if err = os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
					return cleared, err
				}
				r.Attempts[i].LogCleared = true
				changed = true
				cleared++
			}
			if changed {
				if err = e.save(&r); err != nil {
					return cleared, err
				}
				if current := e.current[r.ID]; current != nil {
					current.Attempts = r.Attempts
				}
			}
		}
		offset += len(records)
		if offset >= total || len(records) == 0 {
			break
		}
	}
	return cleared, nil
}

// NotificationChange serializes activation with new task creation and YAML edits.
func (e *Engine) NotificationChange(operation func() error) error {
	e.configMu.Lock()
	defer e.configMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	return operation()
}
