package run

import (
	"database/sql"
	"encoding/json/v2"
	"strings"
	"testing"
	"time"

	"mirrordock/internal/database"
	"mirrordock/internal/syncer"
	"mirrordock/internal/timefmt"
)

func TestAnalyticsCompleteHistoryAndSemantics(t *testing.T) {
	t.Parallel()
	store, err := database.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loc := time.FixedZone("fixture/+08", 8*3600)
	now := time.Date(2026, 10, 1, 12, 0, 0, 500000000, loc)
	midnight := time.Date(2026, 10, 1, 0, 0, 0, 0, loc)
	put := func(r Run) {
		t.Helper()
		if _, err := store.CreateRun(t.Context(), database.CreateRunOptions{Source: r.SourceID, State: r.State, Created: r.CreatedAt.Time(), Value: &r}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 125 {
		put(Run{SourceID: "removed", Name: "旧源", Type: "rsync", State: "succeeded", CreatedAt: timefmt.From(midnight.Add(time.Duration(i) * time.Nanosecond).UTC()), StartedAt: timefmt.From(midnight), ElapsedMillis: 2000, Progress: syncer.Progress{Bytes: 100, Success: 1}, CandidateIndex: 1, Attempts: []Attempt{{ErrorCode: "upstream_error"}, {}}})
	}
	put(Run{SourceID: "failed", Name: "失败源", Type: "rsync", State: "timed_out", ErrorCode: "task_timeout", CreatedAt: timefmt.From(now.Add(-time.Hour)), StartedAt: timefmt.From(midnight), ElapsedMillis: 5000, RetryCount: 2, Progress: syncer.Progress{Bytes: 900, Success: 9}})
	put(Run{SourceID: "failed", Type: "rsync", State: "cancelled", CreatedAt: timefmt.From(now), ElapsedMillis: 99000, Attempts: []Attempt{{ErrorCode: "cancelled"}}})
	put(Run{SourceID: "failed", Type: "rsync", State: "paused", CreatedAt: timefmt.From(now), ElapsedMillis: 99000, Attempts: []Attempt{{ErrorCode: "paused"}}})
	put(Run{SourceID: "future", Type: "rsync", State: "succeeded", CreatedAt: timefmt.From(now.Add(time.Millisecond))})
	put(Run{SourceID: "old", Type: "rsync", State: "succeeded", CreatedAt: timefmt.From(midnight.AddDate(0, 0, -6).Add(-time.Nanosecond))})
	put(Run{SourceID: "gh", Type: "github_release", State: "succeeded", CreatedAt: timefmt.From(now), StartedAt: timefmt.From(midnight), ElapsedMillis: 3000, Progress: syncer.Progress{Bytes: 1024, Files: 5, Success: 5, Skipped: 3}})
	a, err := store.Analyze(t.Context(), database.AnalyzeOptions{Kind: "rsync", Source: "", Days: 7, Now: now, Location: loc})
	if err != nil {
		t.Fatal(err)
	}
	s := a.Summary
	if s.Runs != 128 || s.Succeeded != 125 || s.Failed != 1 || s.Cancelled != 1 || s.Active != 1 || s.Bytes != 12500 || s.Completed != 125 || s.DurationSamples != 126 || s.ElapsedMillis != 255000 || s.FallbackSucceeded != 125 || s.Attempts != 252 || s.FailedAttempts != 125 || s.RetriedRuns != 1 {
		t.Fatalf("wrong summary: %+v", s)
	}
	if len(a.Days) != 7 || a.Days[6].Runs != 128 || a.Days[0].Runs != 0 || a.Days[6].Date != "2026-10-01" || len(a.Errors) != 1 || a.Errors[0].Code != "task_timeout" {
		t.Fatalf("wrong calendar/errors: %+v", a)
	}
	filtered, err := store.Analyze(t.Context(), database.AnalyzeOptions{Kind: "rsync", Source: "removed", Days: 7, Now: now, Location: loc})
	if err != nil || filtered.Summary.Runs != 125 || len(filtered.Sources) != 1 {
		t.Fatal(filtered, err)
	}
	gh, err := store.Analyze(t.Context(), database.AnalyzeOptions{Kind: "github_release", Source: "", Days: 7, Now: now, Location: loc})
	if err != nil || gh.Summary.Completed != 5 || gh.Summary.Reused != 3 || gh.Summary.Bytes != 1024 {
		t.Fatal(gh, err)
	}
	// Aggregate response cannot expose snapshot credentials, attempt messages or URLs.
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"snapshot", "attempts\":[", "upstreams", "token", "proxy", "error\":"} {
		if strings.Contains(string(b), key) {
			t.Fatal("unexpected sensitive field", key)
		}
	}
	empty, err := store.Analyze(t.Context(), database.AnalyzeOptions{Kind: "rsync", Source: "missing", Days: 90, Now: now, Location: loc})
	if err != nil || empty.Summary.Runs != 0 || len(empty.Days) != 90 || empty.Sources == nil || empty.Errors == nil {
		t.Fatal(empty, err)
	}
	for _, kind := range []string{"unknown", ""} {
		if _, err := store.Analyze(t.Context(), database.AnalyzeOptions{Kind: kind, Source: "", Days: 7, Now: now, Location: loc}); err == nil {
			t.Fatal("invalid type accepted")
		}
	}
	if _, err := store.Analyze(t.Context(), database.AnalyzeOptions{Kind: "rsync", Source: "", Days: 100, Now: now, Location: loc}); err == nil {
		t.Fatal("invalid days accepted")
	}
}

func TestAnalyticsReposyncFactsAndTypeIsolation(t *testing.T) {
	t.Parallel()
	store, err := database.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loc := time.FixedZone("fixture/+08", 8*3600)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, loc)
	put := func(r Run) {
		t.Helper()
		if _, err := store.CreateRun(t.Context(), database.CreateRunOptions{Source: r.SourceID, State: r.State, Created: r.CreatedAt.Time(), Value: &r}); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []string{"succeeded", "failed", "timed_out", "interrupted", "cancelled", "paused", "queued"} {
		r := Run{SourceID: "repo", Name: "RPM 历史源", Type: "reposync", State: state, CreatedAt: timefmt.From(now.Add(-time.Hour)), ElapsedMillis: 10000}
		if state != "queued" {
			r.StartedAt = r.CreatedAt
		}
		switch state {
		case "succeeded":
			r.RetryCount = 2
			r.Attempts = []Attempt{{ErrorCode: "upstream_error"}, {ErrorCode: "upstream_error"}, {}}
		case "failed", "timed_out":
			r.ErrorCode = "task_timeout"
			r.Attempts = []Attempt{{ErrorCode: "task_timeout"}}
		case "interrupted", "cancelled", "paused":
			r.Attempts = []Attempt{{ErrorCode: state}}
		}
		put(r)
	}
	put(Run{SourceID: "other-repo", Name: "另一 RPM 源", Type: "reposync", State: "succeeded", CreatedAt: timefmt.From(now.AddDate(0, 0, -1)), StartedAt: timefmt.From(now.AddDate(0, 0, -1)), ElapsedMillis: 20000})
	for _, kind := range []string{"rsync", "github_release"} {
		put(Run{SourceID: "repo", Type: kind, State: "succeeded", CreatedAt: timefmt.From(now)})
	}
	put(Run{SourceID: "old", Type: "reposync", State: "succeeded", CreatedAt: timefmt.From(now.AddDate(0, 0, -7))})
	put(Run{SourceID: "future", Type: "reposync", State: "succeeded", CreatedAt: timefmt.From(now.Add(time.Millisecond))})
	a, err := store.Analyze(t.Context(), database.AnalyzeOptions{Kind: "reposync", Source: "", Days: 7, Now: now, Location: loc})
	if err != nil {
		t.Fatal(err)
	}
	s := a.Summary
	if a.Type != "reposync" || s.Runs != 8 || s.Succeeded != 2 || s.Failed != 2 || s.Cancelled != 1 || s.Active != 3 || s.DurationSamples != 4 || s.ElapsedMillis != 50000 || s.RetriedRuns != 1 || s.Attempts != 8 || s.FailedAttempts != 4 {
		t.Fatalf("reposync summary: %+v", a)
	}
	if len(a.Sources) != 2 || a.Sources[0].Name != "RPM 历史源" || len(a.Errors) != 1 || a.Errors[0].Code != "task_timeout" || a.Errors[0].Count != 2 || a.Days[5].Runs != 1 || a.Days[6].Runs != 7 {
		t.Fatalf("reposync days/ranking/errors: %+v", a)
	}
	filtered, err := store.Analyze(t.Context(), database.AnalyzeOptions{Kind: "reposync", Source: "repo", Days: 7, Now: now, Location: loc})
	if err != nil || filtered.Summary.Runs != 7 || filtered.Summary.ElapsedMillis != 30000 || len(filtered.Sources) != 1 {
		t.Fatal(filtered, err)
	}
}

func TestAnalyticsDSTAndBoundedRanking(t *testing.T) {
	t.Parallel()
	store, err := database.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, loc)
	for i := range 25 {
		r := &Run{SourceID: strings.Repeat("s", i+1), Name: "source", Type: "rsync", State: "failed", ErrorCode: strings.Repeat("E", i+1), CreatedAt: timefmt.From(time.Date(2026, 3, 8, 23, 30, 0, 0, loc))}
		if _, err := store.CreateRun(t.Context(), database.CreateRunOptions{Source: r.SourceID, State: r.State, Created: r.CreatedAt.Time(), Value: r}); err != nil {
			t.Fatal(err)
		}
	}
	a, err := store.Analyze(t.Context(), database.AnalyzeOptions{Kind: "rsync", Source: "", Days: 7, Now: now, Location: loc})
	if err != nil {
		t.Fatal(err)
	}
	if a.Days[4].Date != "2026-03-08" || a.Days[4].Runs != 25 || len(a.Sources) != 20 || len(a.Errors) != 8 || a.Summary.DurationSamples != 0 {
		t.Fatalf("DST/limits: %+v", a)
	}
}

func BenchmarkAnalytics5000Runs(b *testing.B) {
	store, err := database.Open(b.Context(), b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	err = store.Write(b.Context(), func(tx *sql.Tx) error {
		for i := range 5000 {
			r := Run{SourceID: "fixture", Name: "Fixture", Type: "rsync", State: "succeeded", CreatedAt: timefmt.From(now.Add(-time.Duration(i) * time.Minute)), StartedAt: timefmt.From(now.Add(-time.Hour)), ElapsedMillis: 30000, Progress: syncer.Progress{Bytes: 1024}, Attempts: []Attempt{{}}}
			data, err := json.Marshal(r)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := tx.ExecContext(b.Context(), "INSERT INTO runs(source_id,state,created_at,data) VALUES(?,?,?,?)", r.SourceID, r.State, r.CreatedAt.String(), data); err != nil {
				b.Fatal(err)
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := store.Analyze(b.Context(), database.AnalyzeOptions{Kind: "rsync", Source: "", Days: 30, Now: now, Location: time.UTC}); err != nil {
			b.Fatal(err)
		}
	}
}
