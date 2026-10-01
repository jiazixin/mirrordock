package database

import (
	"context"
	"testing"
	"time"
)

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
