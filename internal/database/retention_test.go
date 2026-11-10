package database

import (
	"slices"
	"testing"
	"time"

	"mirrordock/internal/timefmt"
)

func TestExpiredRunsKeepLatestStateOfEverySource(t *testing.T) {
	t.Parallel()
	s, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	old, recent := now.AddDate(0, 0, -120), now.AddDate(0, 0, -10)
	type document struct {
		EndedAt timefmt.Time `json:"ended_at,omitzero"`
	}
	create := func(source, state string, created, ended time.Time) int64 {
		t.Helper()
		value := document{}
		if !ended.IsZero() {
			value.EndedAt = timefmt.From(ended)
		}
		id, err := s.CreateRun(t.Context(), CreateRunOptions{Source: source, State: state, Created: created, Value: value})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	expiredSuccess := create("busy", "succeeded", old, old)
	expiredFailure := create("busy", "failed", old, old)
	create("busy", "succeeded", old, old)      // latest success of busy
	create("busy", "paused", old, time.Time{}) // unfinished
	create("busy", "failed", old, recent)      // ended inside the window
	create("busy", "cancelled", old, old)      // latest run of busy
	create("quiet", "succeeded", old, old)     // latest success of quiet
	expiredQuiet := create("quiet", "timed_out", old, old)
	create("quiet", "failed", old, old)          // latest run of quiet
	create("fresh", "succeeded", recent, recent) // created inside the window
	create("fresh", "succeeded", recent, recent)

	limit := now.AddDate(0, 0, -90)
	through, err := s.LastRunBefore(t.Context(), limit)
	if err != nil || through != 9 {
		t.Fatalf("got last run %d, %v, want 9", through, err)
	}
	got, err := s.ExpiredRuns(t.Context(), ExpiredRunsOptions{Through: through, Before: limit})
	want := []int64{expiredSuccess, expiredFailure, expiredQuiet}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("got expired runs %v, %v, want %v", got, err, want)
	}
	if err := s.DeleteRuns(t.Context(), got); err != nil {
		t.Fatal(err)
	}
	var runs, results int
	if err := s.reader.QueryRowContext(t.Context(), "SELECT (SELECT count(*) FROM runs), (SELECT count(*) FROM run_results)").Scan(&runs, &results); err != nil {
		t.Fatal(err)
	}
	// Ten of the eleven runs are terminal; three of each table are removed.
	if runs != 8 || results != 7 {
		t.Fatalf("got %d runs and %d results left, want 8 and 7", runs, results)
	}
	if got, err := s.ExpiredRuns(t.Context(), ExpiredRunsOptions{Through: through, Before: limit}); err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v after removal, want nothing left to expire", got, err)
	}
	if id, err := s.LastRunBefore(t.Context(), old.AddDate(-1, 0, 0)); err != nil || id != 0 {
		t.Fatalf("got last run %d, %v before any run, want 0", id, err)
	}
}

func TestExpiredNotificationsSpareUnfinishedDeliveries(t *testing.T) {
	t.Parallel()
	s, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	old, recent := timefmt.From(now.AddDate(0, 0, -120)), timefmt.From(now.AddDate(0, 0, -10))
	const expired = PageSize + 20
	run := int64(0)
	enqueue := func(state string, updated timefmt.Time) {
		t.Helper()
		run++
		draft := NotificationDraft{Channel: "webhook", RunID: run, Event: "batch.finished", Title: "fixture", Body: "fixture", State: state, UpdatedAt: updated}
		if err := s.EnqueueNotification(t.Context(), draft); err != nil {
			t.Fatal(err)
		}
	}
	for range expired {
		enqueue("sent", old)
	}
	for _, state := range []string{"waiting", "sending"} {
		enqueue(state, old)
	}
	enqueue("failed", recent)
	limit := now.AddDate(0, 0, -90)
	for _, want := range []int{PageSize, expired - PageSize, 0} {
		if got, err := s.DeleteNotificationsBefore(t.Context(), limit); err != nil || got != want {
			t.Fatalf("got %d removed, %v, want %d", got, err, want)
		}
	}
	if _, total, err := s.NotificationRecords(t.Context(), 1, 0); err != nil || total != 3 {
		t.Fatalf("got %d records left, %v, want 3", total, err)
	}
}
