package database

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"mirrordock/internal/timefmt"
)

// Run IDs grow with creation time, so the newest run created before the
// retention limit bounds every scan from above without reading newer history.
const lastRunBeforeQuery = `SELECT id FROM runs WHERE created_at<? ORDER BY created_at DESC, id DESC LIMIT 1`

// LastRunBefore returns the ID of the newest run created before limit, or 0
// when there is none.
func (s *Store) LastRunBefore(ctx context.Context, limit time.Time) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	var id int64
	err := s.reader.QueryRowContext(ctx, lastRunBeforeQuery, timefmt.From(limit).String()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// The latest run of a source and its latest success stay whatever their age:
// they are the status and the "last synchronized" time of a quiet mirror.
// NOT INDEXED keeps the scan on the primary key, as in logCleanupQuery; the
// two lookups per row seek the (source_id, id) and (source_id, state) indexes.
var expiredRunsQuery = fmt.Sprintf(`SELECT r.id FROM runs AS r NOT INDEXED
 WHERE r.id>? AND r.id<=? AND r.created_at<?
  AND r.state IN ('succeeded','failed','timed_out','cancelled')
  AND COALESCE(json_extract(r.data, '$.ended_at'), r.created_at)<?
  AND r.id<>(SELECT id FROM runs WHERE source_id=r.source_id ORDER BY id DESC LIMIT 1)
  AND r.id<>COALESCE(
   (SELECT id FROM runs WHERE source_id=r.source_id AND state='succeeded' ORDER BY id DESC LIMIT 1), 0)
 ORDER BY r.id LIMIT %d`, PageSize)

// ExpiredRuns returns the next page of finished run IDs in (After, Through]
// that were created and ended before Before. A shorter page than PageSize
// means the scan has reached Through.
func (s *Store) ExpiredRuns(ctx context.Context, params ExpiredRunsOptions) ([]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	limit := timefmt.From(params.Before).String()
	rows, err := s.reader.QueryContext(ctx, expiredRunsQuery, params.After, params.Through, limit, limit)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	err = forEachRow(ctx, rows, func(id *int64) []any { return []any{id} }, func(id int64) error {
		ids = append(ids, id)
		return nil
	})
	return ids, err
}

// DeleteRuns removes run documents and their entries in the result stream in
// one transaction. Callers remove the attempt logs afterwards.
func (s *Store) DeleteRuns(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		for _, statement := range []string{
			"DELETE FROM run_results WHERE id IN (SELECT value FROM json_each(?))",
			"DELETE FROM runs WHERE id IN (SELECT value FROM json_each(?))",
		} {
			if _, err := tx.ExecContext(ctx, statement, string(encoded)); err != nil {
				return err
			}
		}
		return nil
	})
}

// Delivery records are aged by their own last update, not by their run: the
// table has no run index, and record IDs grow with time, so the oldest rows
// are found first. Messages still waiting or being sent are never removed.
var expiredNotificationsQuery = fmt.Sprintf(`DELETE FROM notifications WHERE id IN (
 SELECT id FROM notifications
  WHERE updated_at<? AND state NOT IN ('waiting','sending')
  ORDER BY id LIMIT %d)`, PageSize)

// DeleteNotificationsBefore removes at most PageSize finished delivery
// records last updated before limit and returns how many were removed.
func (s *Store) DeleteNotificationsBefore(ctx context.Context, limit time.Time) (int, error) {
	var removed int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, expiredNotificationsQuery, timefmt.From(limit).String())
		if err != nil {
			return err
		}
		removed, err = result.RowsAffected()
		return err
	})
	return int(removed), err
}

// ExpiredRunsOptions groups the inputs to ExpiredRuns.
type ExpiredRunsOptions struct {
	After   int64
	Through int64
	Before  time.Time
}
