package database

import (
	"context"
	"encoding/json/v2"
	"strings"
	"testing"
	"time"
)

func TestLogCleanupUsesBoundedPrimaryKeyPages(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for id := 1; id <= 230; id++ {
		state := "succeeded"
		if id%4 == 0 {
			state = "queued"
		}
		if _, err := s.CreateRun("fixture", state, "now", map[string]int{"id": id}); err != nil {
			t.Fatal(err)
		}
	}
	through, err := s.LastRunID(t.Context())
	if err != nil || through != 230 {
		t.Fatal(through, err)
	}
	if _, err := s.CreateRun("fixture", "succeeded", "now", map[string]int{"id": 231}); err != nil {
		t.Fatal(err)
	}
	var after int64
	count := 0
	for {
		page, err := s.LogCleanupPage(t.Context(), after, through)
		if err != nil || len(page) > 100 {
			t.Fatal(len(page), err)
		}
		for _, data := range page {
			var record struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			if record.ID <= after || record.ID > through || record.ID%4 == 0 {
				t.Fatal("invalid cursor result", record.ID, after)
			}
			after = record.ID
			count++
		}
		if len(page) < 100 {
			break
		}
	}
	if count != 173 {
		t.Fatal("history skipped or upper bound ignored", count)
	}
	rows, err := s.ReadDB.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+logCleanupQuery, 0, through)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Log(plan)
	if !strings.Contains(plan, "INTEGER PRIMARY KEY") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatal("cleanup rescans and sorts history", plan)
	}
}

func TestLatestRunsIncludeQuietSourcesBeyondHistoryPage(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.CreateRun("quiet", "paused", "2026-10-01T00:00:00Z", map[string]any{"source_id": "quiet", "state": "paused"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 125 {
		_, err = s.CreateRun("noisy", "succeeded", "2026-10-02T00:00:00Z", map[string]any{"source_id": "noisy", "sequence": i})
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.LatestRuns([]string{"quiet", "noisy", "missing"})
	if err != nil || len(rows) != 2 {
		t.Fatal(len(rows), err)
	}
	var first map[string]any
	if err := json.Unmarshal(rows[0], &first); err != nil || first["state"] != "paused" {
		t.Fatal(first, err)
	}
	var last struct {
		Sequence int `json:"sequence"`
	}
	if err := json.Unmarshal(rows[1], &last); err != nil || last.Sequence != 124 {
		t.Fatal(last, err)
	}
}

func TestDurableRecords(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateRun("alma", "queued", "now", map[string]any{"state": "queued"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveRun(id, "paused", map[string]any{"state": "paused", "elapsed": 12}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var got struct {
		State   string `json:"state"`
		Elapsed int    `json:"elapsed"`
	}
	if err = s.Run(id, &got); err != nil {
		t.Fatal(err)
	}
	if got.State != "paused" || got.Elapsed != 12 {
		t.Fatal(got)
	}
	_, n, err := s.ListRuns("paused", "alma", 20, 0)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestRunPaginationFiltersBeforeCounting(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := range 125 {
		trigger := "manual"
		if i%2 == 0 {
			trigger = "schedule"
		}
		if _, err := s.CreateRun("source", "succeeded", "2026-10-01T00:00:00Z", map[string]any{"trigger": trigger, "id": i + 1}); err != nil {
			t.Fatal(err)
		}
	}
	rows, total, err := s.ListRuns("succeeded", "source", 50, 100)
	if err != nil || total != 125 || len(rows) != 25 {
		t.Fatalf("last page: rows=%d total=%d err=%v", len(rows), total, err)
	}
	rows, total, err = s.ListRuns("succeeded", "source", 50, 50, RunFilter{From: "2026-09-30T00:00:00Z", Trigger: "manual"})
	if err != nil || total != 62 || len(rows) != 12 {
		t.Fatalf("filtered last page: rows=%d total=%d err=%v", len(rows), total, err)
	}
	rows, total, err = s.ListRuns("succeeded", "source", 50, 0, RunFilter{From: "2026-09-30T00:00:00Z", Until: "2026-10-01T00:00:00Z"})
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("exclusive upper bound: %d %d %v", len(rows), total, err)
	}
	rows, total, err = s.ListRuns("succeeded", "source", 50, 0, RunFilter{From: "2026-10-02T00:00:00Z"})
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("empty page: rows=%d total=%d err=%v", len(rows), total, err)
	}
}

func TestReadSnapshotDoesNotOccupyWriterAndCannotWrite(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tx, err := s.ReadDB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var before int
	if err := tx.QueryRow("SELECT COUNT(*) FROM runs").Scan(&before); err != nil {
		t.Fatal(err)
	}
	// A held read transaction must not occupy the writer connection or block WAL.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO runs(source_id,state,created_at,data) VALUES('fixture','queued','now','{}')"); err != nil {
		t.Fatal("reader blocked writes", err)
	}
	var during int
	if err := tx.QueryRow("SELECT COUNT(*) FROM runs").Scan(&during); err != nil || during != before {
		t.Fatal("inconsistent read snapshot", during, err)
	}
	if _, err := tx.Exec("DELETE FROM runs"); err == nil {
		t.Fatal("read-only pool allowed writes")
	}
}

func TestRunTimeBoundsHandleWholeAndFractionalSeconds(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, stamp := range []string{"2026-10-01T00:00:00Z", "2026-10-01T00:00:00.000000001Z", "2026-10-01T23:59:59.999999999Z", "2026-10-02T00:00:00Z", "2026-10-02T00:00:00.1Z"} {
		if _, err := s.CreateRun("fixture", "succeeded", stamp, map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}
	_, total, err := s.ListRuns("", "", 50, 0, RunFilter{From: "2026-10-01T00:00:00Z", Until: "2026-10-02T00:00:00Z"})
	if err != nil || total != 3 {
		t.Fatal(total, err)
	}
	_, total, err = s.ListRuns("", "", 50, 0, RunFilter{From: "2026-10-01T00:00:00.000000001Z", Until: "2026-10-02T00:00:00.1Z"})
	if err != nil || total != 3 {
		t.Fatal(total, err)
	}
}
