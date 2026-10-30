package run

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"mirrordock/internal/config"
	"mirrordock/internal/database"
	mirrorfs "mirrordock/internal/mirror"
	"mirrordock/internal/syncer"
)

func TestRecoveryStopsRunsWhoseSourceChanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	text := "listen: 127.0.0.1\nport: 8080\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: run-fixture-password\nsources:\n" +
		"  - {id: moved, name: Moved, type: rsync, enabled: true, dest: moved-new, src1: 'rsync://localhost/one/'}\n" +
		"  - {id: off, name: Off, type: rsync, enabled: false, dest: off, src1: 'rsync://localhost/one/'}\n"
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
	create := func(source, dest, state string) int64 {
		t.Helper()
		snapshot := config.Source{ID: source, Name: source, Type: "rsync", Enabled: true, Dest: dest, Upstreams: map[string]string{"src1": "rsync://localhost/one/"}}
		r := &Run{SourceID: source, Type: "rsync", Dest: dest, State: state, BudgetMillis: 60000, ErrorCode: "service_interrupted", Snapshot: Snapshot{Source: snapshot}}
		if state == "queued" {
			r.ErrorCode = ""
		}
		id, err := store.CreateRun(t.Context(), database.CreateRunOptions{Source: source, State: state, Created: time.Now(), Value: r})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	removed := create("gone", "gone", "queued")
	removedPaused := create("gone-paused", "gone-paused", "paused")
	moved := create("moved", "moved-old", "running")
	disabled := create("off", "off", "queued")
	m, err := mirrorfs.New(t.Context(), mirrorfs.NewOptions{Root: filepath.Join(dir, "mirror"), Store: store, Sources: cfg.Current().Sources, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	var calls atomic.Int32
	e, err := New(t.Context(), NewOptions{Config: cfg, Store: store, Mirror: m, Runner: runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error {
		calls.Add(1)
		return nil
	}), Location: time.Local, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	e.Start(t.Context())
	t.Cleanup(func() { shutdownEngine(t, e) })
	for id, code := range map[int64]string{removed: sourceRemoved, removedPaused: sourceRemoved, moved: sourceChanged} {
		r := waitState(waitStateOptions{t: t, e: e, id: id, state: "cancelled"})
		if r.ErrorCode != code || r.EndedAt.IsZero() {
			t.Fatalf("run %d: %+v", id, r)
		}
	}
	r := waitState(waitStateOptions{t: t, e: e, id: disabled, state: "interrupted"})
	if r.ErrorCode != sourceDisabled {
		t.Fatal(r)
	}
	if err := e.Action(t.Context(), disabled, "resume"); !errors.Is(err, ErrSourceDisabled) {
		t.Fatal("disabled source resumed", err)
	}
	// Recovery has classified every source; none may reach an executor.
	if calls.Load() != 0 {
		t.Fatal("snapshot executed against changed configuration")
	}
	// Removed sources no longer reserve anything, so saving YAML is not blocked.
	c := cfg.Current()
	c.Sources[1].Enabled = true
	if err := e.ApplyConfig(t.Context(), ApplyConfigOptions{Config: c, Revision: cfg.View().Revision, Token: nil, Proxy: nil}); err != nil {
		t.Fatal(err)
	}
	if err := e.Action(t.Context(), disabled, "resume"); err != nil {
		t.Fatal("re-enabled source could not resume", err)
	}
	waitState(waitStateOptions{t: t, e: e, id: disabled, state: "failed"})
	if calls.Load() != 1 {
		t.Fatal("resumed run did not execute once", calls.Load())
	}
}

func TestDequeueHoldsRunWhenSourceDisabledAndRejectsTypeChange(t *testing.T) {
	t.Parallel()
	var admit atomic.Bool
	var calls atomic.Int32
	e, cfg, _, _ := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error {
		calls.Add(1)
		return nil
	}), options: []func(*Engine){func(e *Engine) {
		e.admitSync = func(int64) error {
			if !admit.Load() {
				return mirrorfs.ErrLowSpace
			}
			return nil
		}
	}}})
	r, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	retyped := cfg.Current()
	retyped.Sources[0].Type = "github_release"
	retyped.Sources[0].Upstreams = map[string]string{}
	retyped.Sources[0].GitHubRelease.Repository = "owner/repo"
	if err := e.ApplyConfig(t.Context(), ApplyConfigOptions{Config: retyped, Revision: cfg.View().Revision, Token: nil, Proxy: nil}); !errors.Is(err, ErrReservedSourceChange) {
		t.Fatal("type change accepted while queued", err)
	}
	c := cfg.Current()
	c.Sources[0].Enabled = false
	if err := e.ApplyConfig(t.Context(), ApplyConfigOptions{Config: c, Revision: cfg.View().Revision, Token: nil, Proxy: nil}); err != nil {
		t.Fatal(err)
	}
	admit.Store(true)
	r = waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "interrupted"})
	if r.ErrorCode != sourceDisabled || calls.Load() != 0 {
		t.Fatal("disabled source executed", r, calls.Load())
	}
	if err := e.Action(t.Context(), r.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
}

func TestRsyncRoundRetriesWhenEarlierSourceWasTransient(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	e, _, _, _ := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		n := calls.Add(1)
		switch {
		case n == 1:
			return &syncer.Failure{Code: "upstream_error", Message: "timeout", Retryable: true, SourceFailure: true}
		case n == 2:
			return &syncer.Failure{Code: "upstream_error", Message: "auth failed", SourceFailure: true}
		}
		return os.WriteFile(filepath.Join(task.Directory, "package.rpm"), []byte("package"), 0o644)
	}), options: []func(*Engine){func(e *Engine) { e.retryDelay = time.Millisecond }}})
	r, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	r = waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "succeeded"})
	if r.RetryCount != 1 || calls.Load() != 3 || r.CandidateIndex != 0 {
		t.Fatalf("transient first source did not retry the round: %+v", r)
	}
}

func TestRsyncPermanentRoundStillFails(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	e, _, _, _ := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error {
		calls.Add(1)
		return &syncer.Failure{Code: "upstream_error", Message: "auth failed", SourceFailure: true}
	}), options: []func(*Engine){func(e *Engine) { e.retryDelay = time.Millisecond }}})
	r, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	r = waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "failed"})
	if r.RetryCount != 0 || calls.Load() != 2 {
		t.Fatalf("permanent failures retried: %+v", r)
	}
}

func TestCapacityProbeFailureKeepsRunQueued(t *testing.T) {
	t.Parallel()
	var probeOK atomic.Bool
	e, _, _, _ := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		return os.WriteFile(filepath.Join(task.Directory, "package.rpm"), []byte("package"), 0o644)
	}), options: []func(*Engine){func(e *Engine) {
		admit := e.admitSync
		e.admitSync = func(id int64) error {
			if !probeOK.Load() {
				return errors.New("statfs fixture failure")
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
		if r, err = e.Get(t.Context(), r.ID); err != nil {
			t.Fatal(err)
		}
		if r.Progress.Phase == "waiting_space" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if r.State != "queued" || r.Progress.Phase != "waiting_space" || !r.StartedAt.IsZero() {
		t.Fatal("probe failure consumed the queued run", r)
	}
	probeOK.Store(true)
	waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "succeeded"})
}
