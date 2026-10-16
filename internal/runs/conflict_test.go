package runs

import (
	"context"
	"errors"
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

func TestRecoveryStopsRunsWhoseSourceChanged(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	text := "listen: 127.0.0.1\nport: 8080\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: run-fixture-password\nsources:\n" +
		"  - {id: moved, name: Moved, type: rsync, enabled: true, dest: moved-new, src1: 'rsync://localhost/one/'}\n" +
		"  - {id: off, name: Off, type: rsync, enabled: false, dest: off, src1: 'rsync://localhost/one/'}\n"
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
	create := func(source, dest, state string) int64 {
		t.Helper()
		snapshot := config.Source{ID: source, Name: source, Type: "rsync", Enabled: true, Dest: dest, Upstreams: map[string]string{"src1": "rsync://localhost/one/"}}
		r := &Run{SourceID: source, Type: "rsync", Dest: dest, State: state, BudgetSeconds: 60, ErrorCode: "SERVICE_INTERRUPTED", Snapshot: Snapshot{Source: snapshot}}
		if state == "queued" {
			r.ErrorCode = ""
		}
		id, err := store.CreateRun(source, state, time.Now().UTC().Format(time.RFC3339Nano), r)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	removed := create("gone", "gone", "queued")
	removedPaused := create("gone-paused", "gone-paused", "paused")
	moved := create("moved", "moved-old", "running")
	disabled := create("off", "off", "queued")
	m, err := mirrors.New(filepath.Join(dir, "mirror"), store, cfg.Current().Sources)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	var calls atomic.Int32
	e, err := New(cfg, store, m, runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error {
		calls.Add(1)
		return nil
	}), time.Local)
	if err != nil {
		t.Fatal(err)
	}
	e.Start()
	t.Cleanup(e.Close)
	for id, code := range map[int64]string{removed: sourceRemoved, removedPaused: sourceRemoved, moved: sourceChanged} {
		r := waitState(t, e, id, "cancelled")
		if r.ErrorCode != code || r.EndedAt.IsZero() {
			t.Fatalf("run %d: %+v", id, r)
		}
	}
	r := waitState(t, e, disabled, "interrupted")
	if r.ErrorCode != sourceDisabled {
		t.Fatal(r)
	}
	if err := e.Action(disabled, "resume"); err == nil || !strings.Contains(err.Error(), "源已禁用") {
		t.Fatal("disabled source resumed", err)
	}
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("snapshot executed against changed configuration")
	}
	// Removed sources no longer reserve anything, so saving YAML is not blocked.
	c := cfg.Current()
	c.Sources[1].Enabled = true
	if err := e.ApplyConfig(c, cfg.View().Revision, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.Action(disabled, "resume"); err != nil {
		t.Fatal("re-enabled source could not resume", err)
	}
	waitState(t, e, disabled, "failed")
	if calls.Load() != 1 {
		t.Fatal("resumed run did not execute once", calls.Load())
	}
}

func TestDequeueHoldsRunWhenSourceDisabledAndRejectsTypeChange(t *testing.T) {
	var admit atomic.Bool
	var calls atomic.Int32
	e, cfg, _, _ := testEngine(t, "2s", runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error {
		calls.Add(1)
		return nil
	}), func(e *Engine) {
		e.admitSync = func(int64) error {
			if !admit.Load() {
				return mirrors.ErrLowSpace
			}
			return nil
		}
	})
	r, err := e.Enqueue("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	retyped := cfg.Current()
	retyped.Sources[0].Type = "github_release"
	retyped.Sources[0].Upstreams = map[string]string{}
	retyped.Sources[0].GitHubRelease.Repository = "owner/repo"
	if err := e.ApplyConfig(retyped, cfg.View().Revision, nil, nil); err == nil || !strings.Contains(err.Error(), "类型") {
		t.Fatal("type change accepted while queued", err)
	}
	c := cfg.Current()
	c.Sources[0].Enabled = false
	if err := e.ApplyConfig(c, cfg.View().Revision, nil, nil); err != nil {
		t.Fatal(err)
	}
	admit.Store(true)
	r = waitState(t, e, r.ID, "interrupted")
	if r.ErrorCode != sourceDisabled || calls.Load() != 0 {
		t.Fatal("disabled source executed", r, calls.Load())
	}
	if err := e.Action(r.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
}

func TestRsyncRoundRetriesWhenEarlierSourceWasTransient(t *testing.T) {
	var calls atomic.Int32
	e, _, _, _ := testEngine(t, "2s", runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		n := calls.Add(1)
		switch {
		case n == 1:
			return &syncer.Failure{Code: "UPSTREAM_ERROR", Message: "timeout", Retryable: true, SourceFailure: true}
		case n == 2:
			return &syncer.Failure{Code: "UPSTREAM_ERROR", Message: "auth failed", SourceFailure: true}
		}
		return os.WriteFile(filepath.Join(task.Directory, "package.rpm"), []byte("package"), 0644)
	}), func(e *Engine) { e.retryDelay = time.Millisecond })
	r, err := e.Enqueue("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	r = waitState(t, e, r.ID, "succeeded")
	if r.RetryCount != 1 || calls.Load() != 3 || r.CandidateIndex != 0 {
		t.Fatalf("transient first source did not retry the round: %+v", r)
	}
}

func TestRsyncPermanentRoundStillFails(t *testing.T) {
	var calls atomic.Int32
	e, _, _, _ := testEngine(t, "2s", runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error {
		calls.Add(1)
		return &syncer.Failure{Code: "UPSTREAM_ERROR", Message: "auth failed", SourceFailure: true}
	}), func(e *Engine) { e.retryDelay = time.Millisecond })
	r, err := e.Enqueue("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	r = waitState(t, e, r.ID, "failed")
	if r.RetryCount != 0 || calls.Load() != 2 {
		t.Fatalf("permanent failures retried: %+v", r)
	}
}

func TestCapacityProbeFailureKeepsRunQueued(t *testing.T) {
	var probeOK atomic.Bool
	e, _, _, _ := testEngine(t, "2s", runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		return os.WriteFile(filepath.Join(task.Directory, "package.rpm"), []byte("package"), 0644)
	}), func(e *Engine) {
		admit := e.admitSync
		e.admitSync = func(id int64) error {
			if !probeOK.Load() {
				return errors.New("statfs fixture failure")
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
		if r, err = e.Get(r.ID); err != nil {
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
	waitState(t, e, r.ID, "succeeded")
}
