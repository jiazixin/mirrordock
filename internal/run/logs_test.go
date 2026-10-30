package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mirrordock/internal/database"
	"mirrordock/internal/syncer"
)

func logFixture(params logFixtureOptions) *Run {
	params.t.Helper()
	t := params.t
	e := params.e
	state := params.state
	attempts := params.attempts

	r := &Run{SourceID: "old-fixture", State: state, BudgetMillis: 30000, Attempts: make([]Attempt, attempts)}
	if _, err := e.store.CreateRun(t.Context(), database.CreateRunOptions{Source: r.SourceID, State: state, Created: time.Now(), Value: r}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.dataDir, "logs", fmt.Sprint(r.ID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range attempts {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(i+1)+".log"), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestLogCleanupPreservesRecoverableLogs(t *testing.T) {
	t.Parallel()
	e, _, _, _ := testEngine(testEngineOptions{t: t, timeout: "30s", runner: runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error { return nil })})
	old := logFixture(logFixtureOptions{t: t, e: e, state: "interrupted", attempts: 1})
	if n, err := e.ClearLogs(t.Context()); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	got, err := e.Get(t.Context(), old.ID)
	if err != nil || got.Attempts[0].LogCleared {
		t.Fatal(got, err)
	}
	file, _, err := e.LogSnapshot(t.Context(), old.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
}

func TestLogCleanupCursorExcludesNewHistory(t *testing.T) {
	t.Parallel()
	var added *Run
	e, _, _, _ := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error { return nil })})
	for range 205 {
		logFixture(logFixtureOptions{t: t, e: e, state: "succeeded", attempts: 1})
	}
	e.removeLog = func(path string) error {
		if added == nil {
			added = logFixture(logFixtureOptions{t: t, e: e, state: "succeeded", attempts: 1})
		}
		return os.Remove(path)
	}
	if n, err := e.ClearLogs(t.Context()); n != 205 || err != nil {
		t.Fatal(n, err)
	}
	got, err := e.Get(t.Context(), added.ID)
	if err != nil || got.Attempts[0].LogCleared {
		t.Fatal("cleanup crossed original upper bound", got, err)
	}
	if n, err := e.ClearLogs(t.Context()); n != 1 || err != nil {
		t.Fatal(n, err)
	}
}

func TestLogCleanupCancellationAndFailureCanRetry(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"cancel", "remove", "save"} {
		t.Run(mode, func(t *testing.T) {
			e, _, store, _ := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error { return nil })})
			r := logFixture(logFixtureOptions{t: t, e: e, state: "succeeded", attempts: 2})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "save" {
				if _, err := fixtureExec(fixtureExecOptions{t: t, store: store, query: "CREATE TRIGGER reject_clear BEFORE UPDATE ON runs BEGIN SELECT RAISE(ABORT,'fixture'); END"}); err != nil {
					t.Fatal(err)
				}
			}
			e.removeLog = func(path string) error {
				if mode == "remove" && strings.HasSuffix(path, "2.log") {
					return errors.New("fixture removal failure")
				}
				err := os.Remove(path)
				if mode == "cancel" {
					cancel()
				}
				return err
			}
			n, err := e.ClearLogs(ctx)
			if err == nil {
				t.Fatal("failure hidden", n)
			}
			got, getErr := e.Get(t.Context(), r.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if mode == "save" {
				if n != 2 || got.Attempts[0].LogCleared {
					t.Fatal("failed save marked durable completion", n, got)
				}
				if _, err := fixtureExec(fixtureExecOptions{t: t, store: store, query: "DROP TRIGGER reject_clear"}); err != nil {
					t.Fatal(err)
				}
			} else if n != 1 || !got.Attempts[0].LogCleared || got.Attempts[1].LogCleared {
				t.Fatal("partial completion lost", n, got)
			}
			e.removeLog = os.Remove
			want := 1
			if mode == "save" {
				want = 2
			}
			if n, err := e.ClearLogs(t.Context()); err != nil || n != want {
				t.Fatal(n, err)
			}
		})
	}
}

func TestLogCleanupKeepsOtherActionsResponsive(t *testing.T) {
	t.Parallel()
	started, blocked, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	e, _, _, _ := testEngine(testEngineOptions{t: t, timeout: "30s", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}), options: []func(*Engine){func(e *Engine) {
		e.removeLog = func(path string) error { close(blocked); <-release; return os.Remove(path) }
	}}})
	logFixture(logFixtureOptions{t: t, e: e, state: "succeeded", attempts: 1})
	active, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	done := make(chan error, 1)
	go func() { _, err := e.ClearLogs(t.Context()); done <- err }()
	<-blocked
	other := make(chan error, 1)
	go func() { other <- e.Action(t.Context(), active.ID, "pause") }()
	select {
	case err := <-other:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("filesystem cleanup held the engine lock")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitState(waitStateOptions{t: t, e: e, id: active.ID, state: "paused"})
}

// logFixtureOptions groups the inputs to logFixture.
type logFixtureOptions struct {
	t        *testing.T
	e        *Engine
	state    string
	attempts int
}
