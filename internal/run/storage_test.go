package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	mirrorfs "mirrordock/internal/mirror"
	"mirrordock/internal/syncer"
)

func waitWorkRemoved(t *testing.T, m *mirrorfs.Manager, id int64) {
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
	t.Parallel()
	clock, advance := advancingClock()
	var ready atomic.Bool
	var called atomic.Bool
	e, _, _, m := testEngine(testEngineOptions{t: t, timeout: "5s", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		called.Store(true)
		return os.WriteFile(filepath.Join(task.Directory, "package.rpm"), []byte("fixture"), 0o644)
	}), options: []func(*Engine){func(e *Engine) {
		e.now = clock
		admit := e.admitSync
		e.admitSync = func(id int64) error {
			if !ready.Load() {
				return mirrorfs.ErrLowSpace
			}
			return admit(id)
		}
	}}})
	r, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, err = e.Get(t.Context(), r.ID)
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
	// Advance beyond the budget: queued work must not spend execution time.
	advance(6 * time.Second)
	r, err = e.Get(t.Context(), r.ID)
	if err != nil || r.State != "queued" || r.ElapsedMillis != 0 || !r.StartedAt.IsZero() || called.Load() {
		t.Fatal("space wait spent execution budget", r, err)
	}
	ready.Store(true)
	waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "succeeded"})
	if !called.Load() {
		t.Fatal("space recovery did not start executor")
	}
	waitWorkRemoved(t, m, r.ID)
}

func TestLowSpaceStopsExecutorAndKeepsPublication(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 1)
	var low atomic.Bool
	e, _, _, m := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		if err := os.WriteFile(filepath.Join(task.Directory, "package.rpm"), []byte("new"), 0o644); err != nil {
			return err
		}
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}), options: []func(*Engine){func(e *Engine) {
		e.checkpointInterval = 20 * time.Millisecond
		e.storageCheck = func() error {
			if low.Load() {
				return mirrorfs.ErrLowSpace
			}
			return nil
		}
	}}})
	source := e.cfg.Current().Sources[0]
	work, err := m.Prepare(t.Context(), source, 999)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "package.rpm"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := m.Publish(t.Context(), source, 999)
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}
	low.Store(true)
	r = waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "failed"})
	if r.ErrorCode != "disk_space" {
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
	t.Parallel()
	started := make(chan struct{}, 1)
	e, _, store, m := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})})
	r, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err := e.Action(t.Context(), r.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "paused"})
	if _, err := fixtureExec(fixtureExecOptions{t: t, store: store, query: "CREATE TRIGGER reject_cancel BEFORE UPDATE ON runs WHEN NEW.state='cancelled' BEGIN SELECT RAISE(ABORT,'fixture'); END"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Action(t.Context(), r.ID, "cancel"); err == nil {
		t.Fatal("failed cancellation persisted as success")
	}
	if _, err := e.Enqueue(t.Context(), "test", "manual"); err == nil {
		t.Fatal("failed cancellation released source")
	}
	if err := m.CleanupWork(t.Context(), e.workPolicy(time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Work(r.ID)); err != nil {
		t.Fatal("uncommitted cancellation cleaned work", err)
	}
	if _, err := fixtureExec(fixtureExecOptions{t: t, store: store, query: "DROP TRIGGER reject_cancel"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Action(t.Context(), r.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "cancelled"})
}

func TestCancelledWorkSeedsNextRun(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	var resumed atomic.Bool
	e, _, _, m := testEngine(testEngineOptions{t: t, timeout: "5s", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		if calls.Add(1) == 1 {
			if err := os.WriteFile(filepath.Join(task.Directory, "downloaded.rpm"), []byte("fixture"), 0o644); err != nil {
				return err
			}
			started <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		}
		data, err := os.ReadFile(filepath.Join(task.Directory, "downloaded.rpm"))
		resumed.Store(err == nil && string(data) == "fixture")
		return nil
	})})
	first, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err := e.Action(t.Context(), first.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	waitState(waitStateOptions{t: t, e: e, id: first.ID, state: "cancelled"})
	if err := m.CleanupWork(t.Context(), e.workPolicy(time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.Work(first.ID), "downloaded.rpm")); err != nil {
		t.Fatal("cancelled work was not kept", err)
	}
	second, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	r := waitState(waitStateOptions{t: t, e: e, id: second.ID, state: "succeeded"})
	if !resumed.Load() {
		t.Fatal("next run did not start from cancelled work")
	}
	if !slices.ContainsFunc(r.Events, func(event Event) bool {
		return event.Code == eventReusedWork && fmt.Sprint(event.Details["run"]) == fmt.Sprint(first.ID)
	}) {
		t.Fatal("reuse not recorded", r.Events)
	}
	waitWorkRemoved(t, m, first.ID)
	waitWorkRemoved(t, m, second.ID)
}
