package run

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mirrordock/internal/database"
	"mirrordock/internal/syncer"
	"mirrordock/internal/timefmt"
)

func TestRetentionRemovesExpiredRunsAndLogs(t *testing.T) {
	t.Parallel()
	idle := runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error { return nil })
	e, cfg, store, m := testEngine(testEngineOptions{t: t, timeout: "1m", runner: idle})
	setRetention := func(days string) {
		t.Helper()
		text, err := os.ReadFile(cfg.File())
		if err != nil {
			t.Fatal(err)
		}
		head, tail, found := strings.Cut(string(text), "settings:\n")
		if !found {
			t.Fatal("fixture has no settings")
		}
		_, tail, _ = strings.Cut(tail, "  task_timeout:")
		updated := head + "settings:\n  retention_days: " + days + "\n  task_timeout:" + tail
		if err := os.WriteFile(cfg.File(), []byte(updated), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := e.Reload(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	// Retention is off while the fixtures are created, so the pass that runs
	// when the engine starts cannot remove them early.
	setRetention("0")
	created := e.now().AddDate(0, 0, -120)
	finished := func(state string) int64 {
		t.Helper()
		at := timefmt.From(created)
		r := &Run{SourceID: "test", Name: "Test", Type: "rsync", Dest: "test", State: state, CreatedAt: at, EndedAt: at}
		id, err := store.CreateRun(t.Context(), database.CreateRunOptions{Source: "test", State: state, Created: created, Value: r})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(e.logDir(id), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(e.logPath(id, 1), []byte("log"), 0o600); err != nil {
			t.Fatal(err)
		}
		return id
	}
	expired := finished("failed")
	seeding := finished("timed_out")
	latestSuccess := finished("succeeded")
	latest := finished("cancelled")
	if err := os.MkdirAll(filepath.Join(m.Work(seeding), "partial"), 0o700); err != nil {
		t.Fatal(err)
	}
	present := func(id int64) bool {
		t.Helper()
		_, logErr := os.Stat(e.logDir(id))
		var r Run
		runErr := store.Run(t.Context(), id, &r)
		switch {
		case runErr == nil && logErr == nil:
			return true
		case errors.Is(runErr, sql.ErrNoRows) && errors.Is(logErr, fs.ErrNotExist):
			return false
		}
		t.Fatalf("run %d: record %v but logs %v", id, runErr, logErr)
		return false
	}
	expect := func(step string, want map[int64]bool) {
		t.Helper()
		if _, err := e.prune(t.Context()); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		for id, kept := range want {
			if got := present(id); got != kept {
				t.Errorf("%s: run %d present %v, want %v", step, id, got, kept)
			}
		}
	}
	expect("retention off", map[int64]bool{expired: true, seeding: true, latestSuccess: true, latest: true})
	setRetention("90")
	expect("retention on", map[int64]bool{expired: false, seeding: true, latestSuccess: true, latest: true})
	if err := os.RemoveAll(m.Work(seeding)); err != nil {
		t.Fatal(err)
	}
	expect("work reclaimed", map[int64]bool{expired: false, seeding: false, latestSuccess: true, latest: true})
}
