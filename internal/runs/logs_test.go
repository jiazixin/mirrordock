package runs

import (
	"context"
	"errors"
	"fmt"
	"io"
	syncer "mirrordock/internal/sync"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func logFixture(t *testing.T, e *Engine, state string, attempts int) *Run {
	t.Helper()
	r := &Run{SourceID: "old-fixture", State: state, BudgetSeconds: 30, Attempts: make([]Attempt, attempts)}
	if _, err := e.store.CreateRun(r.SourceID, state, time.Now().UTC().Format(time.RFC3339Nano), r); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.dataDir, "logs", fmt.Sprint(r.ID))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for i := range attempts {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(i+1)+".log"), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestLogCleanupPreservesRecoverableLogs(t *testing.T) {
	e, _, _, _ := testEngine(t, "30s", runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error { return nil }))
	old := logFixture(t, e, "interrupted", 1)
	if n, err := e.ClearLogs(); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	got, err := e.Get(old.ID)
	if err != nil || got.Attempts[0].LogCleared {
		t.Fatal(got, err)
	}
	file, _, err := e.LogSnapshot(old.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
}

func TestLogCleanupCursorExcludesNewHistory(t *testing.T) {
	var added *Run
	e, _, _, _ := testEngine(t, "2s", runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error { return nil }))
	for range 205 {
		logFixture(t, e, "succeeded", 1)
	}
	e.removeLog = func(path string) error {
		if added == nil {
			added = logFixture(t, e, "succeeded", 1)
		}
		return os.Remove(path)
	}
	if n, err := e.ClearLogs(); n != 205 || err != nil {
		t.Fatal(n, err)
	}
	got, err := e.Get(added.ID)
	if err != nil || got.Attempts[0].LogCleared {
		t.Fatal("cleanup crossed original upper bound", got, err)
	}
	if n, err := e.ClearLogs(); n != 1 || err != nil {
		t.Fatal(n, err)
	}
}

func TestLogCleanupCancellationAndFailureCanRetry(t *testing.T) {
	for _, mode := range []string{"cancel", "remove", "save"} {
		t.Run(mode, func(t *testing.T) {
			e, _, store, _ := testEngine(t, "2s", runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error { return nil }))
			r := logFixture(t, e, "succeeded", 2)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "save" {
				if _, err := store.DB.Exec("CREATE TRIGGER reject_clear BEFORE UPDATE ON runs BEGIN SELECT RAISE(ABORT,'fixture'); END"); err != nil {
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
			n, err := e.ClearLogsContext(ctx)
			if err == nil {
				t.Fatal("failure hidden", n)
			}
			got, getErr := e.Get(r.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if mode == "save" {
				if n != 2 || got.Attempts[0].LogCleared {
					t.Fatal("failed save marked durable completion", n, got)
				}
				if _, err := store.DB.Exec("DROP TRIGGER reject_clear"); err != nil {
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
			if n, err := e.ClearLogs(); err != nil || n != want {
				t.Fatal(n, err)
			}
		})
	}
}

func TestLogCleanupKeepsOtherActionsResponsive(t *testing.T) {
	started, blocked, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	e, _, _, _ := testEngine(t, "30s", runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}), func(e *Engine) {
		e.removeLog = func(path string) error { close(blocked); <-release; return os.Remove(path) }
	})
	logFixture(t, e, "succeeded", 1)
	active, err := e.Enqueue("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	done := make(chan error, 1)
	go func() { _, err := e.ClearLogs(); done <- err }()
	<-blocked
	other := make(chan error, 1)
	go func() { other <- e.Action(active.ID, "pause") }()
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
	waitState(t, e, active.ID, "paused")
}
