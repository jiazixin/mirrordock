package database

import (
	"testing"
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
