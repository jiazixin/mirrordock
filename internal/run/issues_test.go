package run

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"mirrordock/internal/database"
	"mirrordock/internal/syncer"
)

func TestPermanentUpstreamFallsBackWithoutRetry(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	e, _, _, _ := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		calls.Add(1)
		if strings.HasSuffix(task.Candidate, "/one/") {
			return syncer.Classify(5, "@ERROR: Unknown module 'one'")
		}
		return os.WriteFile(filepath.Join(task.Directory, "file"), []byte("ok"), 0o644)
	})})
	r, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	r = waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "succeeded"})
	if calls.Load() != 2 || r.RetryCount != 0 {
		t.Fatal(calls.Load(), r.RetryCount)
	}
}

func TestRepositoryValidationParticipatesInFallback(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	e, _, _, _ := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		calls.Add(1)
		if strings.HasSuffix(task.Candidate, "/one/") {
			return nil
		} // empty/inconsistent first mirror
		return os.WriteFile(filepath.Join(task.Directory, "file"), []byte("ok"), 0o644)
	})})
	r, err := e.Enqueue(t.Context(), "test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	r = waitState(waitStateOptions{t: t, e: e, id: r.ID, state: "succeeded"})
	if calls.Load() != 2 || r.Attempts[0].ErrorCode != "upstream_inconsistent" {
		t.Fatal(r)
	}
}

func TestStreamingRedactorEveryBoundary(t *testing.T) {
	t.Parallel()
	input := "开始 abc-secret-xyz 结束\n"
	for split := 0; split <= len(input); split++ {
		var out strings.Builder
		w := &redactedWriter{writer: &out, secrets: []string{"abc-secret-xyz", "abc-secret"}}
		if _, err := w.Write([]byte(input[:split])); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(input[split:])); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		if out.String() != "开始 [REDACTED] 结束\n" {
			t.Fatal(split, out.String())
		}
	}
}

func TestLogChunksPreserveUTF8(t *testing.T) {
	t.Parallel()
	e, _, _, _ := testEngine(testEngineOptions{t: t, timeout: "2s", runner: runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error { return nil })})
	r := logFixture(logFixtureOptions{t: t, e: e, state: "succeeded", attempts: 1})
	content := strings.Repeat("a", (128<<10)-1) + "中文尾部"
	name := filepath.Join(e.dataDir, "logs", fmt.Sprint(r.ID), "1.log")
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	first, next, err := e.Log(t.Context(), r.ID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, end, err := e.Log(t.Context(), r.ID, 1, next)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(first) || !utf8.ValidString(second) || first+second != content || end != int64(len(content)) {
		t.Fatal(next, end)
	}
}

func TestRecoveryPagesAndPreservesPauseBudgetAndFailure(t *testing.T) {
	t.Parallel()
	runner := runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error { return nil })
	e, cfg, store, m := testEngine(testEngineOptions{t: t, timeout: "30s", runner: runner})
	shutdownEngine(t, e)
	for i := range 105 {
		r := &Run{SourceID: "test", Dest: "test", Type: "rsync", Name: fmt.Sprint("paused-", i), State: "paused", BudgetMillis: 30000}
		if _, err := store.CreateRun(t.Context(), database.CreateRunOptions{Source: r.SourceID, State: r.State, Created: time.Now(), Value: r}); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		state, code, kind, want string
		elapsed                 int64
		id                      int64
	}{
		{state: "running", kind: "rsync", want: "queued", elapsed: 7000},
		{state: "pausing", kind: "rsync", want: "paused", elapsed: 7000},
		{state: "interrupted", code: "persistence_failed", kind: "rsync", want: "interrupted", elapsed: 7000},
		{state: "interrupted", code: "service_interrupted", kind: "rsync", want: "timed_out", elapsed: 30000},
		// The configured source is rsync: a retyped snapshot is never resumed.
		{state: "interrupted", code: "service_interrupted", kind: "reposync", want: "cancelled", elapsed: 7000},
	}
	for i := range cases {
		c := &cases[i]
		r := &Run{SourceID: "test", Dest: "test", Type: c.kind, State: c.state, ErrorCode: c.code, ElapsedMillis: c.elapsed, BudgetMillis: 30000, RetryCount: 2}
		var err error
		c.id, err = store.CreateRun(t.Context(), database.CreateRunOptions{Source: r.SourceID, State: r.State, Created: time.Now(), Value: r})
		if err != nil {
			t.Fatal(err)
		}
	}
	restored, err := New(t.Context(), NewOptions{Config: cfg, Store: store, Mirror: m, Runner: runner, Location: time.Local, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownEngine(t, restored)
	if len(restored.current) != 108 || len(restored.pending) != 1 {
		t.Fatal(len(restored.current), len(restored.pending))
	}
	for _, c := range cases {
		r, err := restored.Get(t.Context(), c.id)
		if err != nil || r.State != c.want || r.ElapsedMillis != c.elapsed || r.RetryCount != 2 {
			t.Fatal(c, r, err)
		}
	}
}

func TestRedactDetailsRemovesSecretsFromText(t *testing.T) {
	t.Parallel()
	got := redactDetails(map[string]any{"detail": "proxy http://user:pw@proxy failed", "http_status": 407}, "user:pw")
	if got["detail"] != "proxy http://[REDACTED]@proxy failed" || got["http_status"] != 407 {
		t.Fatalf("got %v", got)
	}
	if redactDetails(nil, "secret") != nil {
		t.Fatal("got non-nil details for nil input")
	}
}
