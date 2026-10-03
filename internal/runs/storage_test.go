package runs

import (
	"context"
	"errors"
	"io"
	"mirrordock/internal/mirrors"
	syncer "mirrordock/internal/sync"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func waitWorkRemoved(t *testing.T, m *mirrors.Manager, id int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(m.Work(id)); errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("terminal work was not cleaned")
}

func TestSpaceWaitDoesNotSpendBudgetAndRecovers(t *testing.T) {
	var ready atomic.Bool
	var called atomic.Bool
	e, _, _, m := testEngine(t, "300ms", runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		called.Store(true)
		return os.WriteFile(filepath.Join(task.Directory, "package.rpm"), []byte("fixture"), 0644)
	}), func(e *Engine) {
		admit := e.admitSync
		e.admitSync = func(id int64) error {
			if !ready.Load() {
				return mirrors.ErrLowSpace
			}
			return admit(id)
		}
	})
	r, err := e.Enqueue("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, err = e.Get(r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Progress.Phase == "waiting_space" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if r.Progress.Phase != "waiting_space" {
		t.Fatal(r)
	}
	time.Sleep(350 * time.Millisecond)
	r, err = e.Get(r.ID)
	if err != nil || r.State != "queued" || r.ElapsedSeconds != 0 || !r.StartedAt.IsZero() || called.Load() {
		t.Fatal("space wait spent execution budget", r, err)
	}
	ready.Store(true)
	waitState(t, e, r.ID, "succeeded")
	if !called.Load() {
		t.Fatal("space recovery did not start executor")
	}
	waitWorkRemoved(t, m, r.ID)
}

func TestLowSpaceStopsExecutorAndKeepsPublication(t *testing.T) {
	started := make(chan struct{}, 1)
	var low atomic.Bool
	e, _, _, m := testEngine(t, "2s", runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		if err := os.WriteFile(filepath.Join(task.Directory, "package.rpm"), []byte("new"), 0644); err != nil {
			return err
		}
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}), func(e *Engine) {
		e.checkpointInterval = 20 * time.Millisecond
		e.storageCheck = func() error {
			if low.Load() {
				return mirrors.ErrLowSpace
			}
			return nil
		}
	})
	source := e.cfg.Current().Sources[0]
	work, err := m.Prepare(t.Context(), source, 999)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "package.rpm"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	old, err := m.Publish(t.Context(), source, 999)
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Enqueue("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}
	low.Store(true)
	r = waitState(t, e, r.ID, "failed")
	if r.ErrorCode != "DISK_SPACE" {
		t.Fatal("capacity cause lost", r)
	}
	if m.Publications()[0].Current.ID != old.Current.ID {
		t.Fatal("low-space task published")
	}
	data, err := os.ReadFile(filepath.Join(m.Root, source.Dest, "package.rpm"))
	if err != nil || string(data) != "old" {
		t.Fatal("live mirror changed", err)
	}
	waitWorkRemoved(t, m, r.ID)
}

func TestCancelPersistenceFailurePreservesResumableWork(t *testing.T) {
	started := make(chan struct{}, 1)
	e, _, store, m := testEngine(t, "2s", runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}))
	r, err := e.Enqueue("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err := e.Action(r.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, r.ID, "paused")
	if _, err := store.DB.Exec("CREATE TRIGGER reject_cancel BEFORE UPDATE ON runs WHEN NEW.state='cancelled' BEGIN SELECT RAISE(ABORT,'fixture'); END"); err != nil {
		t.Fatal(err)
	}
	if err := e.Action(r.ID, "cancel"); err == nil {
		t.Fatal("failed cancellation persisted as success")
	}
	if _, err := e.Enqueue("test", "manual"); err == nil {
		t.Fatal("failed cancellation released source")
	}
	if err := m.CleanupWork(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Work(r.ID)); err != nil {
		t.Fatal("uncommitted cancellation cleaned work", err)
	}
	if _, err := store.DB.Exec("DROP TRIGGER reject_cancel"); err != nil {
		t.Fatal(err)
	}
	if err := e.Action(r.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	waitWorkRemoved(t, m, r.ID)
}
