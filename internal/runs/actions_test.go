package runs

import (
	"context"
	"errors"
	"io"
	"mirrordock/internal/mirrors"
	syncer "mirrordock/internal/sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFailedPauseCannotResumeBeforeExecutorStops(t *testing.T) {
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
	e, _, store, _ := testEngine(t, "20s", runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		started <- calls.Add(1)
		<-release
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("fixture completed")
	}))
	r, err := e.Enqueue("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	_, err = store.DB.Exec("CREATE TRIGGER reject_pause BEFORE UPDATE ON runs WHEN NEW.state='pausing' BEGIN SELECT RAISE(ABORT,'fixture'); END")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Action(r.ID, "pause"); err == nil {
		t.Fatal("fixture must reject pause persistence")
	}
	_, err = store.DB.Exec("DROP TRIGGER reject_pause")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Action(r.ID, "resume"); err == nil {
		t.Fatal("resume accepted before executor stopped")
	}
	select {
	case n := <-started:
		t.Fatalf("同一个任务在上一个执行器尚未退出时再次启动，调用数=%d", n)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	waitState(t, e, r.ID, "paused")
	if err = e.Action(r.ID, "resume"); err != nil {
		t.Fatal("resume after executor exit", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("resume did not start")
	}
	waitState(t, e, r.ID, "failed")
}

func TestResumePersistenceFailureDoesNotStartRunner(t *testing.T) {
	var calls atomic.Int32
	e, _, store, _ := testEngine(t, "20s", runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		calls.Add(1)
		<-ctx.Done()
		return ctx.Err()
	}), func(e *Engine) { e.admitSync = func(int64) error { return mirrors.ErrLowSpace } })
	r, err := e.Enqueue("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Action(r.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec("CREATE TRIGGER reject_resume BEFORE UPDATE ON runs WHEN NEW.state='queued' BEGIN SELECT RAISE(ABORT,'fixture'); END"); err != nil {
		t.Fatal(err)
	}
	if err := e.Action(r.ID, "resume"); err == nil {
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
	got, err := e.Get(r.ID)
	if err != nil || got.State != "interrupted" {
		t.Fatal(got, err)
	}
}
