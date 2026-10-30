package run

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	mirrorfs "mirrordock/internal/mirror"
	"mirrordock/internal/syncer"
)

func TestFailedPauseCannotResumeBeforeExecutorStops(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	started := make(chan int32, 2)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	e, _, store, _ := testEngine(testEngineOptions{t: t, timeout: "20s", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		started <- calls.Add(1)
		<-release
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("fixture completed")
	})})
	r, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	_, err = fixtureExec(fixtureExecOptions{t: t, store: store, query: "CREATE TRIGGER reject_pause BEFORE UPDATE ON runs WHEN NEW.state='pausing' BEGIN SELECT RAISE(ABORT,'fixture'); END"})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Action(t.Context(), r.ID, "pause"); err == nil {
		t.Fatal("fixture must reject pause persistence")
	}
	_, err = fixtureExec(fixtureExecOptions{t: t, store: store, query: "DROP TRIGGER reject_pause"})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Action(t.Context(), r.ID, "resume"); err == nil {
		t.Fatal("resume accepted before executor stopped")
	}
	select {
	case n := <-started:
		t.Fatalf("同一个任务在上一个执行器尚未退出时再次启动，调用数=%d", n)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "paused"})
	if err = e.Action(t.Context(), r.ID, "resume"); err != nil {
		t.Fatal("resume after executor exit", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("resume did not start")
	}
	waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "failed"})
}

func TestResumePersistenceFailureDoesNotStartRunner(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	e, _, store, _ := testEngine(testEngineOptions{t: t, timeout: "20s", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		calls.Add(1)
		<-ctx.Done()
		return ctx.Err()
	}), options: []func(*Engine){func(e *Engine) { e.admitSync = func(int64) error { return mirrorfs.ErrLowSpace } }}})
	r, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Action(t.Context(), r.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureExec(fixtureExecOptions{t: t, store: store, query: "CREATE TRIGGER reject_resume BEFORE UPDATE ON runs WHEN NEW.state='queued' BEGIN SELECT RAISE(ABORT,'fixture'); END"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Action(t.Context(), r.ID, "resume"); err == nil {
		t.Fatal("failed resume was accepted")
	}
	e.mu.Lock()
	pending := len(e.pending)
	e.mu.Unlock()
	if pending > 1 {
		t.Fatal("failed resume added pending entry")
	}
	if calls.Load() != 0 {
		t.Fatal("failed resume started runner")
	}
	got, err := e.Get(t.Context(), r.ID)
	if err != nil || got.State != "interrupted" {
		t.Fatal(got, err)
	}
}
