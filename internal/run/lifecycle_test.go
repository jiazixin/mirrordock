package run

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"mirrordock/internal/syncer"
)

// The service starts the engine with its signal context. A signal must not
// end executions by itself: until Shutdown records the interruption, a
// cancelled run would be stored as failed and never resume after the restart.
func TestStartContextCancellationLeavesRunsToShutdown(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 1)
	probe := make(chan struct{})
	afterSignal := make(chan error, 1)
	runner := runnerFunc(func(ctx context.Context, _ syncer.Task, _ func(syncer.Progress), _ io.Writer) error {
		started <- struct{}{}
		<-probe
		afterSignal <- ctx.Err()
		<-ctx.Done()
		return ctx.Err()
	})
	startCtx, signal := context.WithCancel(t.Context())
	defer signal()
	start := func(e *Engine) {
		if err := e.Start(startCtx); err != nil {
			t.Error(err)
		}
	}
	e, _, _, _ := testEngine(testEngineOptions{t: t, timeout: "1m", runner: runner, options: []func(*Engine){start}})
	r, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	// Cancellation reaches derived contexts before signal returns.
	signal()
	probe <- struct{}{}
	if err := <-afterSignal; err != nil {
		t.Fatalf("got executor context error %v after the start context ended, want it still running", err)
	}
	shutdownEngine(t, e)
	got, err := e.Get(t.Context(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "interrupted" || got.ErrorCode != codeServiceInterrupted {
		t.Fatalf("got state %q code %q, want interrupted %q", got.State, got.ErrorCode, codeServiceInterrupted)
	}
}

// Only a changed state directory or mirror root makes new runs unsafe before
// the restart; an edited log level or account must not stop synchronization.
func TestPendingRestartBlocksRunsOnlyForStorageChanges(t *testing.T) {
	t.Parallel()
	blocked := runnerFunc(func(ctx context.Context, _ syncer.Task, _ func(syncer.Progress), _ io.Writer) error {
		<-ctx.Done()
		return ctx.Err()
	})
	e, cfg, _, _ := testEngine(testEngineOptions{t: t, timeout: "1m", runner: blocked})
	rewrite := func(old, replacement string) {
		t.Helper()
		text, err := os.ReadFile(cfg.File())
		if err != nil {
			t.Fatal(err)
		}
		updated := strings.Replace(string(text), old, replacement, 1)
		if updated == string(text) {
			t.Fatalf("fixture has no %q", old)
		}
		if err := os.WriteFile(cfg.File(), []byte(updated), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := e.Reload(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	rewrite("port: 8080", "port: 8090\nlog:\n  level: debug")
	if !cfg.View().RestartRequired {
		t.Fatal("deployment change not reported as pending restart")
	}
	r, err := e.Enqueue(t.Context(), "test", "schedule")
	if err != nil {
		t.Fatalf("got %v for a log and port change, want the run queued", err)
	}
	if err := e.Action(t.Context(), r.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "cancelled"})
	rewrite("mirror_root: ./mirror", "mirror_root: ./moved-mirror")
	if _, err := e.Enqueue(t.Context(), "test", "schedule"); !errors.Is(err, ErrRestartRequired) {
		t.Fatalf("got %v for a moved mirror root, want %v", err, ErrRestartRequired)
	}
}
