package mirrors

import (
	"errors"
	"mirrordock/internal/config"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type keepFixture struct {
	SourceID  string    `json:"source_id"`
	Type      string    `json:"type"`
	Dest      string    `json:"dest"`
	State     string    `json:"state"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
	ErrorCode string    `json:"error_code"`
}

// endedRun prepares a run's work tree with one downloaded file, then records
// the terminal outcome the way the engine does.
func endedRun(t *testing.T, m *Manager, s config.Source, state, code string, ended time.Time) int64 {
	t.Helper()
	r := keepFixture{SourceID: s.ID, Type: s.Type, Dest: s.Dest, State: "running"}
	id, err := m.store.CreateRun(s.ID, r.State, time.Now().UTC().Format(time.RFC3339Nano), r)
	if err != nil {
		t.Fatal(err)
	}
	work, err := m.Prepare(t.Context(), s, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "downloaded.rpm"), []byte("partial progress"), 0644); err != nil {
		t.Fatal(err)
	}
	r.State, r.ErrorCode, r.EndedAt = state, code, ended
	if err := m.store.SaveRun(id, state, r); err != nil {
		t.Fatal(err)
	}
	return id
}

func exists(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Stat(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func TestUnfinishedWorkSeedsNextRunOfSameSource(t *testing.T) {
	for _, state := range []string{"cancelled", "failed", "timed_out"} {
		t.Run(state, func(t *testing.T) {
			m, s := testManager(t)
			ended := endedRun(t, m, s, state, "UPSTREAM_ERROR", time.Now().UTC())
			if err := m.CleanupWork(t.Context(), WorkPolicy{Sources: []config.Source{s}, Now: time.Now()}); err != nil {
				t.Fatal(err)
			}
			if !exists(t, filepath.Join(m.Work(ended), "downloaded.rpm")) {
				t.Fatal("unfinished work was not kept")
			}
			next, err := m.store.CreateRun(s.ID, "queued", time.Now().UTC().Format(time.RFC3339Nano), keepFixture{SourceID: s.ID, Type: s.Type, Dest: s.Dest, State: "queued"})
			if err != nil {
				t.Fatal(err)
			}
			work, reused, err := m.PrepareReuse(t.Context(), s, next)
			if err != nil || reused != ended {
				t.Fatal(reused, err)
			}
			if data, err := os.ReadFile(filepath.Join(work, "downloaded.rpm")); err != nil || string(data) != "partial progress" {
				t.Fatal("next run did not start from kept work", err)
			}
			if exists(t, m.Work(ended)) {
				t.Fatal("kept work was copied instead of moved")
			}
			// A resumed run keeps its own tree and never adopts again.
			if _, reused, err = m.PrepareReuse(t.Context(), s, next); err != nil || reused != 0 {
				t.Fatal(reused, err)
			}
		})
	}
}

func TestKeptWorkIsDiscardedWhenItCannotSeedTheSource(t *testing.T) {
	m, s := testManager(t)
	now := time.Now().UTC()
	disk := endedRun(t, m, s, "failed", "DISK_SPACE", now)
	expired := endedRun(t, m, s, "cancelled", "", now.Add(-keepWorkFor-time.Minute))
	succeeded := endedRun(t, m, s, "succeeded", "", now)
	older := endedRun(t, m, s, "failed", "UPSTREAM_ERROR", now)
	newest := endedRun(t, m, s, "timed_out", "TASK_TIMEOUT", now)
	other := config.Source{ID: "gone", Name: "Gone", Type: "rsync", Dest: "gone"}
	removed := endedRun(t, m, other, "cancelled", "", now)
	if err := m.CleanupWork(t.Context(), WorkPolicy{Sources: []config.Source{s}, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]int64{"low space": disk, "expired": expired, "succeeded": succeeded, "older": older, "removed source": removed} {
		if exists(t, m.Work(id)) {
			t.Fatal(name, "work kept")
		}
	}
	if !exists(t, m.Work(newest)) {
		t.Fatal("newest unfinished work removed")
	}
	retyped := s
	retyped.Type = "reposync"
	if err := m.CleanupWork(t.Context(), WorkPolicy{Sources: []config.Source{retyped}, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if exists(t, m.Work(newest)) {
		t.Fatal("work kept after the source changed type")
	}
	entries, err := os.ReadDir(filepath.Join(m.Root, ".mirrordock", "work"))
	if err != nil || len(entries) != 0 {
		t.Fatal("discarded work left behind", entries, err)
	}
}

func TestQueuedLowSpaceReclaimsOtherSourcesOldestFirst(t *testing.T) {
	m, s := testManager(t)
	second := config.Source{ID: "second", Name: "Second", Type: "rsync", Dest: "second"}
	third := config.Source{ID: "third", Name: "Third", Type: "rsync", Dest: "third"}
	sources := []config.Source{s, second, third}
	now := time.Now().UTC()
	waiting := endedRun(t, m, s, "failed", "UPSTREAM_ERROR", now.Add(-3*time.Hour))
	oldest := endedRun(t, m, second, "cancelled", "", now.Add(-2*time.Hour))
	newer := endedRun(t, m, third, "cancelled", "", now.Add(-time.Hour))
	available := uint64(1 << 30)
	m.space = func(string) (DiskSpace, error) { return DiskSpace{Total: 10 << 30, Available: available}, nil }
	if err := m.CleanupWork(t.Context(), WorkPolicy{Sources: sources, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if !exists(t, m.Work(oldest)) || !exists(t, m.Work(newer)) {
		t.Fatal("work reclaimed while no run waited for space")
	}
	reclaimed := 0
	m.space = func(string) (DiskSpace, error) {
		// Space recovers once one kept tree is gone.
		if !exists(t, m.Work(oldest)) {
			reclaimed++
			return DiskSpace{Total: 10 << 30, Available: 8 << 30}, nil
		}
		return DiskSpace{Total: 10 << 30, Available: available}, nil
	}
	if err := m.CleanupWork(t.Context(), WorkPolicy{Sources: sources, Waiting: []string{s.ID}, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if exists(t, m.Work(oldest)) || !exists(t, m.Work(newer)) || !exists(t, m.Work(waiting)) || reclaimed == 0 {
		t.Fatal("low space did not reclaim exactly the oldest other source")
	}
	m.space = func(string) (DiskSpace, error) { return DiskSpace{}, errors.New("probe failed") }
	if err := m.CleanupWork(t.Context(), WorkPolicy{Sources: sources, Waiting: []string{s.ID}, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if !exists(t, m.Work(newer)) {
		t.Fatal("capacity probe failure reclaimed work")
	}
}

func TestUndecodableRunDocumentDoesNotBlockCleanup(t *testing.T) {
	m, s := testManager(t)
	broken := endedRun(t, m, s, "failed", "UPSTREAM_ERROR", time.Now().UTC())
	if _, err := m.store.DB.Exec("UPDATE runs SET data=? WHERE id=?", []byte("{"), broken); err != nil {
		t.Fatal(err)
	}
	if err := m.CleanupWork(t.Context(), WorkPolicy{Sources: []config.Source{s}, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if exists(t, m.Work(broken)) {
		t.Fatal("terminal work with an undecodable document was kept")
	}
}
