package mirrors

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"mirrordock/internal/config"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Unfinished work of a cancelled, failed or timed-out run seeds the next run
// of the same source, so a long sync does not restart from the last
// publication. Only prepared trees are kept, at most one per source. A
// low-space failure is not kept: continuing it would need the same space.
const keepWorkFor = 7 * 24 * time.Hour

const discardPrefix = ".discard-"

// WorkPolicy carries the current configuration into work cleanup.
type WorkPolicy struct {
	Sources []config.Source
	// Sources whose queued runs wait for disk space. While any run waits,
	// kept work of other sources is reclaimed oldest first; theirs is kept.
	Waiting []string
	Now     time.Time
}

type workRun struct {
	SourceID  string    `json:"source_id"`
	Type      string    `json:"type"`
	Dest      string    `json:"dest"`
	EndedAt   time.Time `json:"ended_at"`
	ErrorCode string    `json:"error_code"`
	state     string
}

type workDir struct {
	id  int64
	run workRun
}

func (r workRun) keepable(s config.Source, now time.Time) bool {
	switch r.state {
	case "failed", "timed_out", "cancelled":
	default:
		return false
	}
	return r.ErrorCode != "DISK_SPACE" && !r.EndedAt.IsZero() && now.Sub(r.EndedAt) < keepWorkFor &&
		r.SourceID == s.ID && r.Type == s.Type && r.Dest == s.Dest
}

func isPrepared(work string) bool {
	st, err := os.Lstat(filepath.Join(work, ".mirrordock-prepared"))
	return err == nil && st.Mode().IsRegular()
}

// workDirs lists numbered work directories with their persisted run facts.
// Directories without a run row are unknown and never returned.
func (m *Manager) workDirs(ctx context.Context) ([]workDir, error) {
	entries, err := os.ReadDir(filepath.Join(m.Root, ".mirrordock", "work"))
	if err != nil {
		return nil, err
	}
	out := []workDir{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, err := strconv.ParseInt(entry.Name(), 10, 64)
		if err != nil || id <= 0 || !entry.IsDir() {
			continue
		}
		state, data, err := m.store.RunState(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var run workRun
		// An undecodable document only makes the tree ineligible for reuse.
		if json.Unmarshal(data, &run) != nil {
			run = workRun{}
		}
		run.state = state
		out = append(out, workDir{id, run})
	}
	return out, nil
}

// adoptWork moves the newest kept tree of s into the new run's absent work
// directory. Older kept trees of s are set aside for cleanup.
func (m *Manager) adoptWork(ctx context.Context, s config.Source, id int64) (int64, error) {
	dirs, err := m.workDirs(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	seeds := []int64{}
	for _, d := range dirs {
		if d.id < id && d.run.keepable(s, now) && isPrepared(m.Work(d.id)) {
			seeds = append(seeds, d.id)
		}
	}
	if len(seeds) == 0 {
		return 0, nil
	}
	slices.Sort(seeds)
	seed := seeds[len(seeds)-1]
	m.workMu.Lock()
	defer m.workMu.Unlock()
	// Cleanup may have reclaimed the seed after it was listed.
	if err := os.Rename(m.Work(seed), m.Work(id)); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	for _, old := range seeds[:len(seeds)-1] {
		if _, err := m.discardLocked(old); err != nil {
			return seed, err
		}
	}
	return seed, syncDir(filepath.Dir(m.Work(id)))
}

// discardLocked renames a work directory out of the numbered namespace so
// adoption can never see a half-removed tree. Requires workMu.
func (m *Manager) discardLocked(id int64) (string, error) {
	target := filepath.Join(m.Root, ".mirrordock", "work", fmt.Sprintf("%s%d-%d", discardPrefix, id, time.Now().UnixNano()))
	if err := os.Rename(m.Work(id), target); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	return target, nil
}

func (m *Manager) removeWork(ctx context.Context, id int64) error {
	m.workMu.Lock()
	target, err := m.discardLocked(id)
	m.workMu.Unlock()
	if err != nil || target == "" {
		return err
	}
	return removeGeneration(ctx, target)
}

func (m *Manager) removeDiscarded(ctx context.Context) error {
	dir := filepath.Join(m.Root, ".mirrordock", "work")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), discardPrefix) {
			if err := removeGeneration(ctx, filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// CleanupWork consults persisted facts, including resumable interrupted runs.
// Unknown directories are preserved, never guessed to be disposable.
func (m *Manager) CleanupWork(ctx context.Context, policy WorkPolicy) error {
	if err := m.removeDiscarded(ctx); err != nil {
		return err
	}
	dirs, err := m.workDirs(ctx)
	if err != nil {
		return err
	}
	sources := map[string]config.Source{}
	for _, s := range policy.Sources {
		sources[s.ID] = s
	}
	kept := map[string]workDir{}
	drop := []int64{}
	for _, d := range dirs {
		switch d.run.state {
		case "succeeded", "failed", "timed_out", "cancelled":
		default:
			continue
		}
		s, ok := sources[d.run.SourceID]
		if !ok || !d.run.keepable(s, policy.Now) || !isPrepared(m.Work(d.id)) {
			drop = append(drop, d.id)
			continue
		}
		if current, ok := kept[s.ID]; ok {
			drop = append(drop, min(current.id, d.id))
			if current.id > d.id {
				continue
			}
		}
		kept[s.ID] = d
	}
	for _, id := range drop {
		if err := m.removeWork(ctx, id); err != nil {
			return err
		}
	}
	if len(policy.Waiting) == 0 {
		return nil
	}
	reclaim := slices.DeleteFunc(slices.Collect(maps.Values(kept)), func(d workDir) bool {
		return slices.Contains(policy.Waiting, d.run.SourceID)
	})
	slices.SortFunc(reclaim, func(a, b workDir) int { return a.run.EndedAt.Compare(b.run.EndedAt) })
	for _, d := range reclaim {
		// A failed capacity probe is not evidence of low space: keep the work.
		if short, err := m.admissionShort(); err != nil || !short {
			return nil
		}
		slog.Info("reclaim kept work for queued runs", "run", d.id, "source", d.run.SourceID)
		if err := m.removeWork(ctx, d.id); err != nil {
			return err
		}
	}
	return nil
}
