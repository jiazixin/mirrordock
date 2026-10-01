package runs

import (
	"context"
	"fmt"
	"io"
	"mirrordock/internal/config"
	"mirrordock/internal/database"
	"mirrordock/internal/mirrors"
	syncer "mirrordock/internal/sync"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type runnerFunc func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error

func TestLogSnapshotExcludesLaterWritesAndSurvivesCleanup(t *testing.T) {
	e, _, store, _ := testEngine(t, "2s", runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error {
		return nil
	}))
	r := &Run{SourceID: "snapshot-fixture", State: "succeeded", Attempts: []Attempt{{Number: 1}}}
	id, err := store.CreateRun(r.SourceID, r.State, time.Now().UTC().Format(time.RFC3339Nano), r)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.data, "logs", fmt.Sprint(id))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, "1.log")
	content := strings.Repeat("完整日志\n", 300000)
	if err := os.WriteFile(name, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	file, size, err := e.LogSnapshot(id, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	w, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := io.WriteString(w, "下载请求后的追加内容\n")
	closeErr := w.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal(writeErr, closeErr)
	}
	if count, err := e.ClearLogs(); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	got, err := io.ReadAll(io.LimitReader(file, size))
	if err != nil || string(got) != content || size != int64(len(content)) {
		t.Fatal("snapshot omitted content or included later writes", len(got), size, err)
	}
	if _, _, err := e.LogSnapshot(id, 1); err == nil {
		t.Fatal("cleared log was downloadable")
	}
	for _, attempt := range []int{0, 2} {
		if _, _, err := e.LogSnapshot(id, attempt); err == nil {
			t.Fatal("invalid attempt accepted", attempt)
		}
	}
	if _, _, err := e.LogSnapshot(id+1, 1); err == nil {
		t.Fatal("unknown run accepted")
	}
}

func (f runnerFunc) Run(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
	return f(ctx, task, emit, log)
}
func testEngine(t *testing.T, timeout string, runner syncer.Runner) (*Engine, *config.Manager, *database.Store, *mirrors.Manager) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	text := fmt.Sprintf("listen: 127.0.0.1\nport: 8080\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: run-fixture-password\nsettings:\n  task_timeout: %s\n  github:\n    token: top-secret\n    proxy: http://user:proxy-secret@127.0.0.1:8081\nsources:\n  - id: test\n    name: Test\n    type: rsync\n    enabled: true\n    dest: test\n    src1: rsync://localhost/one/\n    src2: rsync://localhost/two/\n", timeout)
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	store, err := database.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	m, err := mirrors.New(filepath.Join(dir, "mirror"), store, cfg.Current().Sources)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	e, err := New(cfg, store, m, runner, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	e.Start()
	t.Cleanup(e.Close)
	return e, cfg, store, m
}
func waitState(t *testing.T, e *Engine, id int64, state string) *Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := e.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == state {
			return r
		}
		time.Sleep(2 * time.Millisecond)
	}
	r, _ := e.Get(id)
	t.Fatalf("wanted %s, got %+v", state, r)
	return nil
}
func TestFailoverUsesOneRunAndRedactsLogs(t *testing.T) {
	var calls atomic.Int32
	e, _, store, m := testEngine(t, "2s", runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		calls.Add(1)
		fmt.Fprintln(log, "token top-secret", task.Settings.GitHub.Proxy, "proxy-secret")
		if task.Settings.GitHub.Proxy == "" {
			t.Error("current proxy was not passed to executor")
		}
		if strings.HasSuffix(task.Candidate, "/one/") {
			return &syncer.Failure{Code: "RSYNC_NETWORK", Message: "network unavailable", Retryable: true, ExitCode: 10}
		}
		return os.WriteFile(filepath.Join(task.Directory, "package.rpm"), []byte("package"), 0644)
	}))
	r, err := e.Enqueue("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	r = waitState(t, e, r.ID, "succeeded")
	if r.ID == 0 || len(r.Attempts) != 2 || calls.Load() != 2 || len(m.Publications()) != 1 {
		t.Fatalf("bad failover: %+v", r)
	}
	log, _, err := e.Log(r.ID, 1, 0)
	if err != nil || (strings.Contains(log, "top-secret") || strings.Contains(log, "proxy-secret")) || !strings.Contains(log, "[REDACTED]") {
		t.Fatal(log, err)
	}
	var saved Run
	if err = store.Run(r.ID, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ID != r.ID || (saved.Snapshot.Settings.GitHub.Token != "" || saved.Snapshot.Settings.GitHub.Proxy != "") {
		t.Fatal("invalid durable snapshot")
	}
	if n, err := e.ClearLogs(); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	r, err = e.Get(r.ID)
	if err != nil || r.State != "succeeded" || !r.Attempts[0].LogCleared {
		t.Fatal(r, err)
	}
}
func TestFailoverSharesTimeoutAndLocalErrorsDoNotSwitch(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprint(retry), func(t *testing.T) {
			var calls atomic.Int32
			e, _, _, _ := testEngine(t, "120ms", runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
				if calls.Add(1) == 1 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(65 * time.Millisecond):
						return &syncer.Failure{Code: "TEST", Message: "test", Retryable: retry}
					}
				}
				<-ctx.Done()
				return ctx.Err()
			}))
			r, err := e.Enqueue("test", "manual")
			if err != nil {
				t.Fatal(err)
			}
			state := "failed"
			if retry {
				state = "timed_out"
			}
			r = waitState(t, e, r.ID, state)
			if retry && (calls.Load() != 2 || r.ElapsedSeconds > .19) {
				t.Fatalf("budget reset: %+v", r)
			}
			if !retry && calls.Load() != 1 {
				t.Fatal("switched after local failure")
			}
		})
	}
}
func TestPausePreservesBudgetAndReservesDestination(t *testing.T) {
	started := make(chan struct{}, 4)
	e, cfg, _, _ := testEngine(t, "400ms", runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		started <- struct{}{}
		fmt.Fprintln(log, "partial")
		<-ctx.Done()
		return ctx.Err()
	}))
	r, err := e.Enqueue("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	time.Sleep(80 * time.Millisecond)
	if err = e.Action(r.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	paused := waitState(t, e, r.ID, "paused")
	if _, err = e.Enqueue("test", "manual"); err == nil {
		t.Fatal("paused source allowed second task")
	}
	c := cfg.Current()
	c.Sources = nil
	if err = e.ApplyConfig(c, cfg.View().Revision, nil, nil); err == nil {
		t.Fatal("paused destination removed")
	}
	if n, err := e.ClearLogs(); err != nil || n != 0 {
		t.Fatal("paused logs cleared", n, err)
	}
	time.Sleep(150 * time.Millisecond)
	still, _ := e.Get(r.ID)
	if still.ElapsedSeconds != paused.ElapsedSeconds {
		t.Fatal("pause consumed budget")
	}
	resumed := time.Now()
	if err = e.Action(r.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	<-started
	r = waitState(t, e, r.ID, "timed_out")
	if r.ElapsedSeconds < .39 || time.Since(resumed) > 370*time.Millisecond || len(r.Attempts) != 2 {
		t.Fatalf("incorrect resumed budget: %+v", r)
	}
}
func TestRestartKeepsInterruptedRun(t *testing.T) {
	started := make(chan struct{}, 1)
	runner := runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	e, cfg, store, m := testEngine(t, "2s", runner)
	r, err := e.Enqueue("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	time.Sleep(30 * time.Millisecond)
	e.Close()
	restored, err := New(cfg, store, m, runner, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	r, err = restored.Get(r.ID)
	if err != nil || r.State != "interrupted" || r.ElapsedSeconds < .02 {
		t.Fatal(r, err)
	}
	if _, err = restored.Enqueue("test", "manual"); err == nil {
		t.Fatal("interrupted destination not reserved")
	}
	if err = restored.Action(r.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
}
