package run

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"time"

	"mirrordock/internal/database"
)

// retentionInterval is how often finished runs past settings.retention_days
// are removed; the first pass runs when the engine starts.
const retentionInterval = time.Hour

// retentionLoop removes expired run records and their logs periodically.
func (e *Engine) retentionLoop(ctx context.Context) {
	ticker := time.NewTicker(retentionInterval)
	defer ticker.Stop()
	for {
		removed, err := e.prune(ctx)
		if err != nil && ctx.Err() == nil {
			e.logger.Error("run_retention_failed", "removed", removed, "error", err)
		} else if removed > 0 {
			e.logger.Info("run_retention_completed", "removed", removed)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// prune removes finished runs that were created and ended more than
// settings.retention_days ago, with their logs, and returns how many were
// removed. The latest run and the latest success of every source are kept,
// as are runs whose work directory still waits to seed the next run.
func (e *Engine) prune(ctx context.Context) (int, error) {
	days := int(e.cfg.Current().Settings.RetentionDays)
	if days == 0 {
		return 0, nil
	}
	limit := e.now().AddDate(0, 0, -days)
	through, err := e.store.LastRunBefore(ctx, limit)
	if err != nil {
		return 0, err
	}
	removed := 0
	var after int64
	for after < through {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		page := database.ExpiredRunsOptions{After: after, Through: through, Before: limit}
		count, last, err := e.prunePage(ctx, page)
		removed += count
		if err != nil {
			return removed, err
		}
		if last == 0 {
			break
		}
		after = last
	}
	return removed, nil
}

// prunePage removes one page of expired runs and returns how many it removed
// and the last run ID it examined, 0 after the final page. It excludes a
// manual log cleanup, which rewrites the documents of the same finished runs.
func (e *Engine) prunePage(ctx context.Context, page database.ExpiredRunsOptions) (removed int, last int64, err error) {
	e.clearMu.Lock()
	defer e.clearMu.Unlock()
	ids, err := e.store.ExpiredRuns(ctx, page)
	if err != nil || len(ids) == 0 {
		return 0, 0, err
	}
	if len(ids) == database.PageSize {
		last = ids[len(ids)-1]
	}
	expired := make([]int64, 0, len(ids))
	for _, id := range ids {
		if e.retained(id) {
			continue
		}
		expired = append(expired, id)
	}
	// Log readers hold logMu while they look a run up and open its file, so
	// they see either the run with its logs or neither.
	e.logMu.Lock()
	defer e.logMu.Unlock()
	if err := e.store.DeleteRuns(ctx, expired); err != nil {
		return 0, last, err
	}
	var failures []error
	for _, id := range expired {
		// The record is gone; a log directory that cannot be removed is only
		// reported, because no later pass would find it again.
		if err := os.RemoveAll(e.logDir(id)); err != nil {
			failures = append(failures, err)
		}
	}
	return len(expired), last, errors.Join(failures...)
}

// retained reports whether a finished run must outlive its retention: it
// is still held in memory, or its work directory exists and work cleanup
// decides from the run record whether that directory may be removed.
func (e *Engine) retained(id int64) bool {
	e.mu.Lock()
	live := e.current[id] != nil
	e.mu.Unlock()
	if live {
		return true
	}
	_, err := os.Lstat(e.mirror.Work(id))
	return !errors.Is(err, fs.ErrNotExist)
}
