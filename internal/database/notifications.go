package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"mirrordock/internal/timefmt"
)

const (
	recoverSendingQuery = `UPDATE notifications
 SET state=CASE WHEN attempts<3 THEN 'waiting' ELSE 'failed' END, next_at=0, updated_at=?,
     error='unconfirmed_after_restart'
 WHERE state='sending'`
	activationBoundaryQuery = `SELECT
 COALESCE((SELECT MAX(id) FROM runs), 0),
 COALESCE((SELECT MAX(sequence) FROM run_results), 0)`
	stopDisabledQuery = `UPDATE notifications SET state='stopped', updated_at=?, error='channel_disabled'
 WHERE channel=? AND state='waiting'`
	stopSuccessQuery = `UPDATE notifications SET state='stopped', updated_at=?, error='failure_only'
 WHERE channel=? AND state='waiting' AND failure=0`
	pendingResultsQuery = `SELECT sequence, data FROM run_results
 WHERE sequence>? ORDER BY sequence LIMIT ?`
	enqueueQuery = `INSERT OR IGNORE INTO notifications
 (channel, run_id, event, title, body, failure, state, next_at, updated_at, error)
 VALUES(?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`
	nextDueQuery = `SELECT id, channel, run_id, attempts, event, title, body FROM notifications
 WHERE state='waiting' AND next_at<=? ORDER BY id LIMIT 1`
	invalidChannelQuery = `UPDATE notifications SET state='failed', updated_at=?, error='invalid_channel'
 WHERE id=?`
	markSendingQuery = `UPDATE notifications SET state='sending', attempts=?, updated_at=?
 WHERE id=? AND state='waiting'`
	finishSendingQuery = `UPDATE notifications SET state=?, next_at=?, updated_at=?, error=?
 WHERE id=? AND state='sending'`
	recordCountQuery = "SELECT count(*) FROM notifications"
	recordPageQuery  = `SELECT id, channel, run_id, state, attempts, updated_at, error FROM notifications
 ORDER BY id DESC LIMIT ? OFFSET ?`
)

// NotificationDraft is the immutable message and initial queue state.
type NotificationDraft struct {
	Channel   string
	RunID     int64
	Event     string
	Title     string
	Body      string
	Failure   bool
	State     string
	UpdatedAt timefmt.Time
	Error     string
}

// PendingNotification is one due notification and its attempt count.
type PendingNotification struct {
	ID       int64
	Channel  string
	RunID    int64
	Attempts int
	Event    string
	Title    string
	Body     string
}

// NotificationRecord contains delivery facts without message bodies.
type NotificationRecord struct {
	ID        int64
	Channel   string
	RunID     int64
	State     string
	Attempts  int
	UpdatedAt timefmt.Time
	Error     string
}

// Result is one terminal run in completion order.
type Result struct {
	Sequence int64
	Document []byte
}

// RecoverNotifications marks sends interrupted by the last process as retryable or failed.
func (s *Store) RecoverNotifications(ctx context.Context, now timefmt.Time) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, recoverSendingQuery, now.String())
		return err
	})
}

// NotificationBoundary captures the run and result cursors in a single snapshot.
func (s *Store) NotificationBoundary(ctx context.Context) (int64, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	var cutoff, cursor int64
	err := s.reader.QueryRowContext(ctx, activationBoundaryQuery).Scan(&cutoff, &cursor)
	return cutoff, cursor, err
}

// StopNotifications removes waiting messages that no longer match channel settings.
func (s *Store) StopNotifications(ctx context.Context, draft NotificationDraft) error {
	query := stopDisabledQuery
	if draft.Failure {
		query = stopSuccessQuery
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, query, draft.UpdatedAt.String(), draft.Channel)
		return err
	})
}

// PendingResults reads a bounded page in durable completion order.
func (s *Store) PendingResults(ctx context.Context, cursor int64, limit int) ([]Result, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	rows, err := s.reader.QueryContext(ctx, pendingResultsQuery, cursor, min(PageSize, max(1, limit)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []Result{}
	for rows.Next() {
		var result Result
		if err := rows.Scan(&result.Sequence, &result.Document); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

// EnqueueNotification inserts a message once per channel and run.
func (s *Store) EnqueueNotification(ctx context.Context, draft NotificationDraft) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, enqueueQuery, draft.Channel, draft.RunID, draft.Event, draft.Title, draft.Body,
			draft.Failure, draft.State, draft.UpdatedAt.String(), draft.Error)
		return err
	})
}

// NextNotification returns the oldest due queue entry, or sql.ErrNoRows.
func (s *Store) NextNotification(ctx context.Context, now time.Time) (PendingNotification, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	var next PendingNotification
	err := s.reader.QueryRowContext(ctx, nextDueQuery, now.UnixMilli()).Scan(
		&next.ID, &next.Channel, &next.RunID, &next.Attempts, &next.Event, &next.Title, &next.Body)
	return next, err
}

// NotificationOutcome describes an attempt transition.
type NotificationOutcome struct {
	ID        int64
	Attempts  int
	State     string
	RetryAt   time.Time
	UpdatedAt timefmt.Time
	Error     string
}

// InvalidNotification fails a pending entry whose saved channel is invalid.
func (s *Store) InvalidNotification(ctx context.Context, outcome NotificationOutcome) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, invalidChannelQuery, outcome.UpdatedAt.String(), outcome.ID)
		return err
	})
}

// MarkNotificationSending durably starts one attempt before network I/O.
func (s *Store) MarkNotificationSending(ctx context.Context, outcome NotificationOutcome) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, markSendingQuery, outcome.Attempts, outcome.UpdatedAt.String(), outcome.ID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("notification is no longer waiting")
		}
		return nil
	})
}

// FinishNotification stores the outcome of a previously started attempt.
func (s *Store) FinishNotification(ctx context.Context, outcome NotificationOutcome) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		var retryAt int64
		if !outcome.RetryAt.IsZero() {
			retryAt = outcome.RetryAt.UnixMilli()
		}
		_, err := tx.ExecContext(ctx, finishSendingQuery, outcome.State, retryAt, outcome.UpdatedAt.String(), outcome.Error, outcome.ID)
		return err
	})
}

// NotificationRecords reads count and page from one read-only WAL snapshot.
func (s *Store) NotificationRecords(ctx context.Context, limit, offset int) ([]NotificationRecord, int, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	tx, err := s.reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback() // A read-only snapshot has no writes to commit.
	var total int
	if err := tx.QueryRowContext(ctx, recordCountQuery).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, recordPageQuery, min(100, max(1, limit)), max(0, offset))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	records := []NotificationRecord{}
	for rows.Next() {
		var record NotificationRecord
		var updated string
		if err := rows.Scan(&record.ID, &record.Channel, &record.RunID, &record.State, &record.Attempts, &updated, &record.Error); err != nil {
			return nil, 0, err
		}
		record.UpdatedAt, err = timefmt.Parse(updated)
		if err != nil {
			return nil, 0, err
		}
		records = append(records, record)
	}
	return records, total, rows.Err()
}
