package runs

import (
	"context"
	"fmt"
	"io"
	syncer "mirrordock/internal/sync"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestPermanentUpstreamFallsBackWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	e, _, _, _ := testEngine(t, "2s", runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		calls.Add(1)
		if strings.HasSuffix(task.Candidate, "/one/") {
			return syncer.Classify(5, "@ERROR: Unknown module 'one'")
		}
		return os.WriteFile(filepath.Join(task.Directory, "file"), []byte("ok"), 0644)
	}))
	r, err := e.Enqueue("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	r = waitState(t, e, r.ID, "succeeded")
	if calls.Load() != 2 || r.RetryCount != 0 {
		t.Fatal(calls.Load(), r.RetryCount)
	}
}

func TestRepositoryValidationParticipatesInFallback(t *testing.T) {
	var calls atomic.Int32
	e, _, _, _ := testEngine(t, "2s", runnerFunc(func(ctx context.Context, task syncer.Task, emit func(syncer.Progress), log io.Writer) error {
		calls.Add(1)
		if strings.HasSuffix(task.Candidate, "/one/") {
			return nil
		} // empty/inconsistent first mirror
		return os.WriteFile(filepath.Join(task.Directory, "file"), []byte("ok"), 0644)
	}))
	r, err := e.Enqueue("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	r = waitState(t, e, r.ID, "succeeded")
	if calls.Load() != 2 || r.Attempts[0].ErrorCode != "UPSTREAM_INCONSISTENT" {
		t.Fatal(r)
	}
}

func TestStreamingRedactorEveryBoundary(t *testing.T) {
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
	e, _, _, _ := testEngine(t, "2s", runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error { return nil }))
	r := logFixture(t, e, "succeeded", 1)
	content := strings.Repeat("a", (128<<10)-1) + "中文尾部"
	name := filepath.Join(e.data, "logs", fmt.Sprint(r.ID), "1.log")
	if err := os.WriteFile(name, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	first, next, err := e.Log(r.ID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, end, err := e.Log(r.ID, 1, next)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(first) || !utf8.ValidString(second) || first+second != content || end != int64(len(content)) {
		t.Fatal(next, end)
	}
}

func TestRecoveryPagesAndPreservesPauseBudgetAndFailure(t *testing.T) {
	runner := runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error { return nil })
	e, cfg, store, m := testEngine(t, "30s", runner)
	e.Close()
	for i := range 105 {
		r := &Run{SourceID: fmt.Sprint("paused-", i), State: "paused", BudgetSeconds: 30}
		if _, err := store.CreateRun(r.SourceID, r.State, time.Now().UTC().Format(time.RFC3339Nano), r); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		state, code, kind, want string
		elapsed                 float64
		id                      int64
	}{
		{state: "running", kind: "rsync", want: "queued", elapsed: 7},
		{state: "pausing", kind: "rsync", want: "paused", elapsed: 7},
		{state: "interrupted", code: "PERSISTENCE_FAILED", kind: "rsync", want: "interrupted", elapsed: 7},
		{state: "interrupted", code: "SERVICE_INTERRUPTED", kind: "rsync", want: "timed_out", elapsed: 30},
		{state: "interrupted", code: "SERVICE_INTERRUPTED", kind: "reposync", want: "interrupted", elapsed: 7},
	}
	for i := range cases {
		c := &cases[i]
		r := &Run{SourceID: "test", Dest: "test", Type: c.kind, State: c.state, ErrorCode: c.code, ElapsedSeconds: c.elapsed, BudgetSeconds: 30, RetryCount: 2}
		var err error
		c.id, err = store.CreateRun(r.SourceID, r.State, time.Now().UTC().Format(time.RFC3339Nano), r)
		if err != nil {
			t.Fatal(err)
		}
	}
	restored, err := New(cfg, store, m, runner, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if len(restored.current) != 109 || len(restored.pending) != 1 {
		t.Fatal(len(restored.current), len(restored.pending))
	}
	for _, c := range cases {
		r, err := restored.Get(c.id)
		if err != nil || r.State != c.want || r.ElapsedSeconds != c.elapsed || r.RetryCount != 2 {
			t.Fatal(c, r, err)
		}
	}
}
