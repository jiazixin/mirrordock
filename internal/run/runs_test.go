package run

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mirrordock/internal/config"
	"mirrordock/internal/database"
	mirrorfs "mirrordock/internal/mirror"
	"mirrordock/internal/syncer"
)

type runnerFunc func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error

func TestLogSnapshotExcludesLaterWritesAndSurvivesCleanup(t *testing.T) {
	t.Parallel()
	e, _, store, _ := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error {
		return nil
	})})
	r := &Run{SourceID: "snapshot-fixture", State: "succeeded", Attempts: []Attempt{{Number: 1}}}
	id, err := store.CreateRun(t.Context(), database.CreateRunOptions{Source: r.SourceID, State: r.State, Created: time.Now(), Value: r})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.dataDir, "logs", fmt.Sprint(id))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, "1.log")
	content := strings.Repeat("完整日志\n", 300000)
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	file, size, err := e.LogSnapshot(t.Context(), id, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	w, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := io.WriteString(w, "下载请求后的追加内容\n")
	closeErr := w.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal(writeErr, closeErr)
	}
	if count, err := e.ClearLogs(t.Context()); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	got, err := io.ReadAll(io.LimitReader(file, size))
	if err != nil || string(got) != content || size != int64(len(content)) {
		t.Fatal("snapshot omitted content or included later writes", len(got), size, err)
	}
	if _, _, err := e.LogSnapshot(t.Context(), id, 1); err == nil {
		t.Fatal("cleared log was downloadable")
	}
	for _, attempt := range []int{0, 2} {
		if _, _, err := e.LogSnapshot(t.Context(), id, attempt); err == nil {
			t.Fatal("invalid attempt accepted", attempt)
		}
	}
	if _, _, err := e.LogSnapshot(t.Context(), id+1, 1); err == nil {
		t.Fatal("unknown run accepted")
	}
}

func (f runnerFunc) Run(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
	return f(ctx, task, emit, log)
}

func testEngine(params testEngineOptions) (*Engine, *config.Manager, *database.Store, *mirrorfs.Manager) {
	params.t.Helper()
	t := params.t
	timeout := params.timeout
	runner := params.runner
	options := params.options

	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	text := fmt.Sprintf("listen: 127.0.0.1\nport: 8080\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: run-fixture-password\nsettings:\n  task_timeout: %s\n  github:\n    token: top-secret\n    proxy: http://user:proxy-secret@127.0.0.1:8081\nsources:\n  - id: test\n    name: Test\n    type: rsync\n    enabled: true\n    dest: test\n    src1: rsync://localhost/one/\n    src2: rsync://localhost/two/\n", timeout)
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Open(config.LoadSource{Path: file, Explicit: true})
	if err != nil {
		t.Fatal(err)
	}
	store, err := database.Open(t.Context(), filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	m, err := mirrorfs.New(t.Context(), mirrorfs.NewOptions{Root: filepath.Join(dir, "mirror"), Store: store, Sources: cfg.Current().Sources, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	e, err := New(t.Context(), NewOptions{Config: cfg, Store: store, Mirror: m, Runner: runner, Location: time.Local, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range options {
		option(e)
	}
	e.Start(t.Context())
	t.Cleanup(func() { shutdownEngine(t, e) })
	return e, cfg, store, m
}

func waitState(params waitStateOptions) *Run {
	params.t.Helper()
	t := params.t
	e := params.e
	id := params.id
	state := params.state

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := e.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == state {
			return r
		}
		time.Sleep(2 * time.Millisecond)
	}
	r, _ := e.Get(t.Context(), id)
	t.Fatalf("wanted %s, got %+v", state, r)
	return nil
}

func TestFailoverUsesOneRunAndRedactsLogs(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	e, _, store, m := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		calls.Add(1)
		fmt.Fprintln(log, "token top-secret", task.Settings.GitHub.Proxy, "proxy-secret")
		if task.Settings.GitHub.Proxy == "" {
			t.Error("current proxy was not passed to executor")
		}
		if strings.HasSuffix(task.Candidate, "/one/") {
			return &syncer.Failure{Code: "rsync_network", Message: "network unavailable", Retryable: true, ExitCode: 10}
		}
		return os.WriteFile(filepath.Join(task.Directory, "package.rpm"), []byte("package"), 0o644)
	})})
	r, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	r = waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "succeeded"})
	if r.ID == 0 || len(r.Attempts) != 2 || calls.Load() != 2 || len(m.Publications()) != 1 {
		t.Fatalf("bad failover: %+v", r)
	}
	log, _, err := e.Log(t.Context(), r.ID, 1, 0)
	if err != nil || (strings.Contains(log, "top-secret") || strings.Contains(log, "proxy-secret")) || !strings.Contains(log, "[REDACTED]") {
		t.Fatal(log, err)
	}
	var saved Run
	if err = store.Run(t.Context(), r.ID, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ID != r.ID || (saved.Snapshot.Settings.GitHub.Token != "" || saved.Snapshot.Settings.GitHub.Proxy != "") {
		t.Fatal("invalid durable snapshot")
	}
	if n, err := e.ClearLogs(t.Context()); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	r, err = e.Get(t.Context(), r.ID)
	if err != nil || r.State != "succeeded" || !r.Attempts[0].LogCleared {
		t.Fatal(r, err)
	}
}

func TestFailoverSharesTimeoutAndLocalErrorsDoNotSwitch(t *testing.T) {
	t.Parallel()
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprint(retry), func(t *testing.T) {
			// The first source fails halfway through a 400ms budget. Switching
			// sources persists the run twice (fsync), so leave 200ms for that on
			// slow CI disks. A shared budget times out near 0.4s; a reset one
			// would run the second source for another 0.4s, past 0.6s.
			var calls atomic.Int32
			e, _, _, _ := testEngine(testEngineOptions{t: t, timeout: "400ms", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
				if calls.Add(1) == 1 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(200 * time.Millisecond):
						return &syncer.Failure{Code: "TEST", Message: "test", Retryable: retry}
					}
				}
				<-ctx.Done()
				return ctx.Err()
			})})
			r, err := e.Enqueue(t.Context(), "test", "manual")
			if err != nil {
				t.Fatal(err)
			}
			state := "failed"
			if retry {
				state = "timed_out"
			}
			r = waitState(waitStateOptions{t: t, e: e, id: r.ID, state: state})
			if retry && (calls.Load() != 2 || r.ElapsedMillis > 500) {
				t.Fatalf("budget reset: %+v", r)
			}
			if !retry && calls.Load() != 1 {
				t.Fatal("switched after local failure")
			}
		})
	}
}

func TestPausePreservesBudgetAndReservesDestination(t *testing.T) {
	t.Parallel()
	clock, advance := advancingClock()
	started := make(chan struct{}, 4)
	e, cfg, _, _ := testEngine(testEngineOptions{t: t, timeout: "400ms", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		started <- struct{}{}
		fmt.Fprintln(log, "partial")
		<-ctx.Done()
		return ctx.Err()
	}), options: []func(*Engine){func(e *Engine) { e.now = clock }}})
	r, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	// Advance elapsed time before pausing without waiting on wall time.
	advance(80 * time.Millisecond)
	if err = e.Action(t.Context(), r.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	paused := waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "paused"})
	if _, err = e.Enqueue(t.Context(), "test", "manual"); err == nil {
		t.Fatal("paused source allowed second task")
	}
	c := cfg.Current()
	c.Sources = nil
	if err = e.ApplyConfig(t.Context(), ApplyConfigOptions{Config: c, Revision: cfg.View().Revision, Token: nil, Proxy: nil}); err == nil {
		t.Fatal("paused destination removed")
	}
	if n, err := e.ClearLogs(t.Context()); err != nil || n != 0 {
		t.Fatal("paused logs cleared", n, err)
	}
	// A paused run must not spend budget while the clock advances.
	advance(150 * time.Millisecond)
	still, _ := e.Get(t.Context(), r.ID)
	if still.ElapsedMillis != paused.ElapsedMillis {
		t.Fatal("pause consumed budget")
	}
	resumed := time.Now()
	if err = e.Action(t.Context(), r.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	<-started
	r = waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "timed_out"})
	if r.ElapsedMillis < 390 || time.Since(resumed) > 370*time.Millisecond || len(r.Attempts) != 2 {
		t.Fatalf("incorrect resumed budget: %+v", r)
	}
}

func TestRestartKeepsInterruptedRun(t *testing.T) {
	t.Parallel()
	clock, advance := advancingClock()
	started := make(chan struct{}, 1)
	runner := runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	e, cfg, store, m := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runner, options: []func(*Engine){func(e *Engine) { e.now = clock }}})
	r, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	// Advance elapsed time so restart has a measured budget to preserve.
	advance(30 * time.Millisecond)
	shutdownEngine(t, e)
	restored, err := New(t.Context(), NewOptions{Config: cfg, Store: store, Mirror: m, Runner: runner, Location: time.Local, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownEngine(t, restored)
	r, err = restored.Get(t.Context(), r.ID)
	if err != nil || r.State != "queued" || r.ElapsedMillis < 20 {
		t.Fatal(r, err)
	}
	if _, err = restored.Enqueue(t.Context(), "test", "manual"); err == nil {
		t.Fatal("interrupted destination not reserved")
	}
	if err = restored.Action(t.Context(), r.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticRetryAcrossExecutors(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"rsync", "reposync", "github_release"} {
		for _, count := range []int{0, 3} {
			for _, outcome := range []string{"exhaust", "recover", "permanent"} {
				recover := outcome == "recover"
				retryable := outcome != "permanent"
				t.Run(fmt.Sprintf("%s/%d/%s", kind, count, outcome), func(t *testing.T) {
					var calls atomic.Int32
					var candidates []string
					e, cfg, _, m := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
						n := calls.Add(1)
						candidates = append(candidates, task.Candidate)
						// The first two attempts must share the same work directory.
						partial := filepath.Join(task.Directory, ".part")
						if n == 1 {
							if err := os.WriteFile(partial, []byte("partial"), 0o600); err != nil {
								return err
							}
						} else if b, err := os.ReadFile(partial); err != nil || string(b) != "partial" {
							t.Error("retry discarded work", err)
						}
						if recover && n == 2 {
							if kind == "reposync" {
								if err := os.MkdirAll(filepath.Join(task.Directory, "repodata"), 0o755); err != nil {
									return err
								}
								primary := `<metadata><package><location href="package.rpm"/><size package="7"/></package></metadata>`
								if err := os.WriteFile(filepath.Join(task.Directory, "repodata", "primary.xml"), []byte(primary), 0o644); err != nil {
									return err
								}
								repomd := fmt.Sprintf(`<repomd><data type="primary"><location href="repodata/primary.xml"/><size>%d</size></data></repomd>`, len(primary))
								if err := os.WriteFile(filepath.Join(task.Directory, "repodata", "repomd.xml"), []byte(repomd), 0o644); err != nil {
									return err
								}
							}
							return os.WriteFile(filepath.Join(task.Directory, "package.rpm"), []byte("package"), 0o644)
						}
						return &syncer.Failure{Code: "NETWORK", Message: "top-secret proxy-secret failure", Retryable: retryable}
					}), options: []func(*Engine){func(e *Engine) { e.retryDelay = time.Millisecond }}})
					c := cfg.Current()
					c.Settings.RetryCount = config.RetryCount(count)
					c.Sources[0].Type = kind
					if kind == "reposync" {
						c.Sources[0].RepoSync = config.RepoSync{RepoID: "fixture", Releasever: "9", Basearch: "x86_64", GPGCheck: new(false)}
						c.Sources[0].Upstreams = map[string]string{"src1": "http://localhost/repo/"}
					}
					if kind == "github_release" {
						c.Sources[0].GitHubRelease.Repository = "o/r"
						c.Sources[0].Upstreams = nil
					}
					if err := e.ApplyConfig(t.Context(), ApplyConfigOptions{Config: c, Revision: cfg.View().Revision, Token: nil, Proxy: nil}); err != nil {
						t.Fatal(err)
					}
					r, err := e.Enqueue(t.Context(), "test", "manual")
					if err != nil {
						t.Fatal(err)
					}
					state := "failed"
					want := count + 1
					if kind == "rsync" {
						want *= 2
					}
					if !retryable {
						want = 1
					}
					if recover && want >= 2 {
						state = "succeeded"
						want = 2
					}
					r = waitState(waitStateOptions{t: t, e: e, id: r.ID, state: state})
					if calls.Load() != int32(want) || len(r.Attempts) != want {
						t.Fatalf("unexpected retry cap: %+v", r)
					}
					expectedRetries := count
					if !retryable {
						expectedRetries = 0
					}
					if state == "failed" && r.RetryCount != expectedRetries {
						t.Fatal("retry count", r.RetryCount)
					}
					for i, candidate := range candidates {
						if kind == "rsync" && candidate != c.Sources[0].Candidates()[i%2] {
							t.Fatal("bad fallback order", candidates)
						}
					}
					if state == "failed" && len(m.Publications()) != 0 {
						t.Fatal("published failed work")
					}
					b, _ := json.Marshal(r)
					if strings.Contains(string(b), "top-secret") || strings.Contains(string(b), "proxy-secret") {
						t.Fatal("retry leaked credentials")
					}
				})
			}
		}
	}
}

func waitRetryPhase(t *testing.T, e *Engine, id int64) *Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := e.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == "running" && r.Progress.Phase == "retry" {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("did not enter retry wait")
	return nil
}

func TestRetryWaitHonorsBudgetPauseCancelAndRestart(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"timeout", "pause", "cancel", "restart"} {
		t.Run(action, func(t *testing.T) {
			clock, advance := advancingClock()
			var calls atomic.Int32
			runner := runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
				calls.Add(1)
				return &syncer.Failure{Code: "NETWORK", Message: "temporary failure", Retryable: true}
			})
			e, cfg, store, m := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runner, options: []func(*Engine){func(e *Engine) { e.now = clock }}})
			r, err := e.Enqueue(t.Context(), "test", "manual")
			if err != nil {
				t.Fatal(err)
			}
			waiting := waitRetryPhase(t, e, r.ID)
			if waiting.RetryCount != 1 || waiting.RetryAt.IsZero() {
				t.Fatal("retry not durable")
			}
			switch action {
			case "timeout":
				r = waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "timed_out"})
				if r.ElapsedMillis > 3000 || calls.Load() != 2 {
					t.Fatal("wait reset or exceeded budget", r)
				}
			case "cancel":
				if err = e.Action(t.Context(), r.ID, "cancel"); err != nil {
					t.Fatal(err)
				}
				waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "cancelled"})
				if calls.Load() != 2 {
					t.Fatal("cancel retried")
				}
			case "pause", "restart":
				if action == "pause" {
					if err = e.Action(t.Context(), r.ID, "pause"); err != nil {
						t.Fatal(err)
					}
					r = waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "paused"})
				} else {
					shutdownEngine(t, e)
					e, err = New(t.Context(), NewOptions{Config: cfg, Store: store, Mirror: m, Runner: runner, Location: time.Local, Logger: slog.New(slog.DiscardHandler)})
					if err != nil {
						t.Fatal(err)
					}
					e.now = clock
					t.Cleanup(func() { shutdownEngine(t, e) })
					r, err = e.Get(t.Context(), r.ID)
					if err != nil || r.State != "queued" {
						t.Fatal(r, err)
					}
				}
				if r.RetryCount != 1 || !r.RetryAt.Time().Equal(waiting.RetryAt.Time()) {
					t.Fatal("pause/restart lost retry state")
				}
				// Advancing the clock while paused expires retry time without spending budget.
				advance(6 * time.Second)
				if action == "restart" {
					e.Start(t.Context())
				} else if err = e.Action(t.Context(), r.ID, "resume"); err != nil {
					t.Fatal(err)
				}
				waitRetryPhase(t, e, r.ID)
				r = waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "timed_out"})
				if r.RetryCount != 2 || calls.Load() != 4 {
					t.Fatal("resume reset retry budget", r)
				}
			}
		})
	}
}

func TestReceivedBytesAccumulateFallbackAndPersist(t *testing.T) {
	t.Parallel()
	e, _, store, _ := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		n := int64(100)
		if strings.Contains(task.Candidate, "one") {
			emit(syncer.Progress{Phase: "transfer", Bytes: 100000, ReceivedBytes: new(n)})
			return &syncer.Failure{Code: "NETWORK", Retryable: true}
		}
		n = 25
		emit(syncer.Progress{Phase: "transfer", Bytes: 100000, ReceivedBytes: new(n)})
		n = 50
		emit(syncer.Progress{Phase: "verify", Bytes: 100000, ReceivedBytes: new(n)})
		return os.WriteFile(filepath.Join(task.Directory, "package.rpm"), []byte("package"), 0o644)
	})})
	run, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	got := waitState(waitStateOptions{t: t, e: e, id: run.ID, state: "succeeded"})
	if got.Progress.ReceivedBytes == nil || *got.Progress.ReceivedBytes != 150 || got.Progress.ReceivedIncomplete {
		t.Fatal(got.Progress)
	}
	var saved Run
	if err := store.Run(t.Context(), run.ID, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Progress.ReceivedBytes == nil || *saved.Progress.ReceivedBytes != 150 {
		t.Fatal(saved.Progress)
	}
	total, incomplete := receivedBytes([]Attempt{{}, {ReceivedBytes: new(int64(50))}})
	if total == nil || *total != 50 || !incomplete {
		t.Fatal(total, incomplete)
	}
	total, incomplete = receivedBytes([]Attempt{{ReceivedBytes: new(int64(0))}})
	if total == nil || *total != 0 || incomplete {
		t.Fatal(total, incomplete)
	}
}

// testEngineOptions groups the inputs to testEngine.
type testEngineOptions struct {
	t       *testing.T
	timeout string
	runner  syncer.Runner
	options []func(*Engine)
}

// waitStateOptions groups the inputs to waitState.
type waitStateOptions struct {
	t     *testing.T
	e     *Engine
	id    int64
	state string
}

// advancingClock preserves real timer progress and atomically advances wall time.
func advancingClock() (func() time.Time, func(time.Duration)) {
	var offset atomic.Int64
	return func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }, func(d time.Duration) { offset.Add(int64(d)) }
}
