// Package database owns the SQLite schema and stores run records and service
// state.
package database

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"mirrordock/internal/timefmt"
)

// schemaVersion is stored in PRAGMA user_version. Any future change to the
// table layout must raise it. Unreleased schemas are rejected, never migrated.
const schemaVersion = 2

const schema = `
CREATE TABLE IF NOT EXISTS runs (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	source_id  TEXT NOT NULL,
	state      TEXT NOT NULL,
	created_at TEXT NOT NULL,
	data       BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS runs_source_state ON runs(source_id, state);
CREATE INDEX IF NOT EXISTS runs_source_id ON runs(source_id, id DESC);
CREATE INDEX IF NOT EXISTS runs_state_id ON runs(state, id DESC);
CREATE INDEX IF NOT EXISTS runs_created_at ON runs(created_at);

CREATE TABLE IF NOT EXISTS run_results (
	sequence INTEGER PRIMARY KEY AUTOINCREMENT,
	id       INTEGER NOT NULL UNIQUE,
	data     BLOB NOT NULL
);

CREATE TABLE IF NOT EXISTS publications (
	source_id TEXT PRIMARY KEY,
	data      BLOB NOT NULL
);

CREATE TABLE IF NOT EXISTS app_state (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS notifications (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	channel    TEXT NOT NULL,
	run_id     INTEGER NOT NULL,
	event      TEXT NOT NULL,
	title      TEXT NOT NULL,
	body       TEXT NOT NULL,
	failure    INTEGER NOT NULL,
	state      TEXT NOT NULL,
	attempts   INTEGER NOT NULL DEFAULT 0,
	next_at    INTEGER NOT NULL,
	updated_at TEXT NOT NULL,
	error      TEXT NOT NULL DEFAULT '',
	UNIQUE(channel, run_id)
);
CREATE INDEX IF NOT EXISTS notifications_due ON notifications(state, next_at, id);

CREATE TABLE IF NOT EXISTS idempotency_keys (
	key         TEXT PRIMARY KEY,
	fingerprint TEXT NOT NULL,
	status      INTEGER NOT NULL,
	body        BLOB NOT NULL,
	created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idempotency_keys_created ON idempotency_keys(created_at);
`

// Connection pool sizes. SQLite permits one writer at a time, so the writer
// pool holds a single connection; WAL lets the bounded reader pool keep a
// consistent snapshot while task facts continue to persist.
const (
	writerConnections = 1
	readerConnections = 4
)

// Store is the MirrorDock SQLite database with separate writer and reader
// pools.
type Store struct {
	db *sql.DB
	// reader isolates bounded management queries from the writer.
	reader *sql.DB
}

// Open opens or creates the database in dir and applies its schema.
func Open(ctx context.Context, dir string) (*Store, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	file, err := filepath.Abs(filepath.Join(dir, "data.sqlite"))
	if err != nil {
		return nil, err
	}
	options := url.Values{}
	options.Set("_txlock", "immediate")
	for _, pragma := range []string{"foreign_keys(1)", "journal_mode(WAL)", "synchronous(FULL)", "busy_timeout(5000)"} {
		options.Add("_pragma", pragma)
	}
	dsn := &url.URL{Scheme: "file", Path: file, RawQuery: options.Encode()}

	writer, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	writer.SetMaxOpenConns(writerConnections)
	writer.SetMaxIdleConns(writerConnections)
	if err := initialize(ctx, writer); err != nil {
		return nil, errors.Join(err, writer.Close())
	}
	// The WAL and shared-memory files hold the same facts as the database.
	if err := restrictPermissions(file); err != nil {
		return nil, errors.Join(err, writer.Close())
	}

	readOptions := options.Clone()
	readOptions.Del("_txlock")
	readOptions.Set("mode", "ro")
	readOptions.Add("_pragma", "query_only(1)")
	dsn.RawQuery = readOptions.Encode()
	reader, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, errors.Join(err, writer.Close())
	}
	reader.SetMaxOpenConns(readerConnections)
	reader.SetMaxIdleConns(readerConnections)
	if err := reader.PingContext(ctx); err != nil {
		return nil, errors.Join(err, reader.Close(), writer.Close())
	}
	return &Store{db: writer, reader: reader}, nil
}

// initialize enables WAL, creates the tables of a new database and refuses a
// database written by any different schema.
func initialize(ctx context.Context, db *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return err
	}
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != 0 && version != schemaVersion {
		return fmt.Errorf("database schema version %d does not match the supported version %d; use a matching program version", version, schemaVersion)
	}
	if version == schemaVersion {
		return nil
	}
	return (&Store{db: db}).Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return fmt.Errorf("create database schema: %w", err)
		}
		_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", schemaVersion))
		return err
	})
}

func restrictPermissions(file string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		err := os.Chmod(file+suffix, 0o600)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Close closes both connection pools.
func (s *Store) Close() error { return errors.Join(s.reader.Close(), s.db.Close()) }

// State returns the stored service state value of key.
func (s *Store) State(ctx context.Context, key string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	var value string
	err := s.reader.QueryRowContext(ctx, "SELECT value FROM app_state WHERE key=?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// SetState stores the service state value of key.
func (s *Store) SetState(ctx context.Context, key, value string) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			"INSERT INTO app_state(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
			key, value,
		)
		return err
	})
}

// IdempotentResponse is the stored first response to an Idempotency-Key.
type IdempotentResponse struct {
	Fingerprint string
	Status      int
	Body        []byte
}

// IdempotentResponse returns the response stored for key since notBefore,
// or false when there is none.
func (s *Store) IdempotentResponse(ctx context.Context, key string, notBefore time.Time) (IdempotentResponse, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	var stored IdempotentResponse
	err := s.reader.QueryRowContext(ctx,
		"SELECT fingerprint, status, body FROM idempotency_keys WHERE key=? AND created_at>=?",
		key, notBefore.UnixMilli(),
	).Scan(&stored.Fingerprint, &stored.Status, &stored.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return stored, false, nil
	}
	return stored, err == nil, err
}

// SaveIdempotentResponse stores the first response to key and removes keys
// created before notBefore.
func (s *Store) SaveIdempotentResponse(ctx context.Context, params SaveIdempotentResponseOptions) error {
	key := params.Key
	response := params.Response
	now := params.Now
	notBefore := params.NotBefore

	return s.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM idempotency_keys WHERE created_at<?", notBefore.UnixMilli()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT OR REPLACE INTO idempotency_keys(key, fingerprint, status, body, created_at) VALUES(?, ?, ?, ?, ?)",
			key, response.Fingerprint, response.Status, response.Body, now.UnixMilli(),
		); err != nil {
			return err
		}
		return nil
	})
}

// CreateRun stores a new run document and returns its ID.
func (s *Store) CreateRun(ctx context.Context, params CreateRunOptions) (int64, error) {
	source := params.Source
	state := params.State
	created := timefmt.From(params.Created).String()
	value := params.Value
	request := params.Request

	var id int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		document, err := json.Marshal(value)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx,
			"INSERT INTO runs(source_id, state, created_at, data) VALUES(?, ?, ?, ?)",
			source, state, created, document,
		)
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return err
		}
		if setter, ok := value.(interface{ SetID(int64) }); ok {
			setter.SetID(id)
			document, err = json.Marshal(value)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE runs SET data=? WHERE id=?", document, id); err != nil {
				return err
			}
		}
		if terminal(state) {
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO run_results(id, data) VALUES(?, ?)", id, document); err != nil {
				return err
			}
		}
		if len(request) > 0 && request[0].Key != "" {
			identity := request[0]
			if _, err := tx.ExecContext(ctx, "DELETE FROM idempotency_keys WHERE created_at<?", identity.CreatedAt.Add(-IdempotencyWindow).UnixMilli()); err != nil {
				return err
			}
			// Status zero denotes a newly created run resource; the HTTP adapter
			// chooses its creation status. The bytes are the first immutable result.
			if _, err := tx.ExecContext(ctx, "INSERT INTO idempotency_keys(key,fingerprint,status,body,created_at) VALUES(?,?,0,?,?)",
				identity.Key, identity.Fingerprint, document, identity.CreatedAt.UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

func terminal(state string) bool {
	switch state {
	case "succeeded", "failed", "timed_out", "cancelled":
		return true
	}
	return false
}

// SaveRun replaces a run document. A terminal state also enters the
// append-only result stream read by notifications, in the same transaction.
func (s *Store) SaveRun(ctx context.Context, id int64, state string, value any) error {
	document, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "UPDATE runs SET state=?, data=? WHERE id=?", state, document, id)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			return fmt.Errorf("run %d missing", id)
		}
		if terminal(state) {
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO run_results(id, data) VALUES(?, ?)", id, document); err != nil {
				return err
			}
		}
		return nil
	})
}

// collectDocuments reads every single-column BLOB row and closes rows.
func collectDocuments(ctx context.Context, rows *sql.Rows) ([][]byte, error) {
	documents := [][]byte{}
	err := forEachRow(ctx, rows, func(row *[]byte) []any { return []any{row} }, func(row []byte) error {
		documents = append(documents, row)
		return nil
	})
	return documents, err
}

// PageSize bounds each page returned by RecoverableRuns and LogCleanupPage.
// A shorter page means the caller has reached the end.
const PageSize = 100

var recoverableRunsQuery = fmt.Sprintf(`SELECT data FROM runs
 WHERE id>? AND state IN ('queued','running','pausing','paused','interrupted')
 ORDER BY id LIMIT %d`, PageSize)

// RecoverableRuns returns the next page of unfinished run documents with IDs
// after after.
func (s *Store) RecoverableRuns(ctx context.Context, after int64) ([][]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	rows, err := s.reader.QueryContext(ctx, recoverableRunsQuery, after)
	if err != nil {
		return nil, err
	}
	return collectDocuments(ctx, rows)
}

// Run decodes the document of run id into out.
func (s *Store) Run(ctx context.Context, id int64, out any) error {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	var document []byte
	if err := s.reader.QueryRowContext(ctx, "SELECT data FROM runs WHERE id=?", id).Scan(&document); err != nil {
		return err
	}
	return json.Unmarshal(document, out)
}

// LogCleanupPage uses a fixed upper bound and indexed cursor so new tasks do
// not move older rows between pages or extend an ongoing cleanup indefinitely.
// NOT INDEXED still permits the integer primary key: the state index would
// scan and sort the remaining terminal history separately on every page.
var logCleanupQuery = fmt.Sprintf(`SELECT data FROM runs NOT INDEXED
 WHERE id>? AND id<=? AND state IN ('succeeded','failed','timed_out','cancelled')
 ORDER BY id LIMIT %d`, PageSize)

// LogCleanupPage returns the next page of finished run documents with IDs in
// (after, through].
func (s *Store) LogCleanupPage(ctx context.Context, after, through int64) ([][]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	rows, err := s.reader.QueryContext(ctx, logCleanupQuery, after, through)
	if err != nil {
		return nil, err
	}
	return collectDocuments(ctx, rows)
}

// LastRunID returns the highest run ID, or 0 when there are no runs.
func (s *Store) LastRunID(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	var id int64
	err := s.reader.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM runs").Scan(&id)
	return id, err
}

// RunState reads the durable state column with the raw run document; the
// state column stays authoritative even if the document cannot be decoded.
func (s *Store) RunState(ctx context.Context, id int64) (string, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	var state string
	var document []byte
	err := s.reader.QueryRowContext(ctx, "SELECT state, data FROM runs WHERE id=?", id).Scan(&state, &document)
	return state, document, err
}

// The correlated primary-key lookup seeks the (source_id, id DESC) index once
// per requested source, without scanning unrelated history or making N queries.
const latestRunsQuery = `SELECT r.data FROM json_each(?) AS source
 JOIN runs r ON r.id=(SELECT id FROM runs WHERE source_id=source.value ORDER BY id DESC LIMIT 1)
 ORDER BY CAST(source.key AS INTEGER)`

const latestSuccessfulRunsQuery = `SELECT r.data FROM json_each(?) AS source
 JOIN runs r ON r.id=(
	SELECT id FROM runs WHERE source_id=source.value AND state='succeeded' ORDER BY id DESC LIMIT 1
 )
 ORDER BY CAST(source.key AS INTEGER)`

// LatestRuns returns the latest run document of each source.
func (s *Store) LatestRuns(ctx context.Context, sources []string) ([][]byte, error) {
	return s.latestRuns(ctx, sources, latestRunsQuery)
}

// LatestSuccessfulRuns returns the latest successful run document of
// each source.
func (s *Store) LatestSuccessfulRuns(ctx context.Context, sources []string) ([][]byte, error) {
	return s.latestRuns(ctx, sources, latestSuccessfulRunsQuery)
}

func (s *Store) latestRuns(ctx context.Context, sources []string, query string) ([][]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	encoded, err := json.Marshal(sources)
	if err != nil {
		return nil, err
	}
	rows, err := s.reader.QueryContext(ctx, query, string(encoded))
	if err != nil {
		return nil, err
	}
	return collectDocuments(ctx, rows)
}

// RunFilter narrows ListRuns by creation time range and trigger.
type RunFilter struct {
	From    string
	Until   string
	Trigger string
}

// ListRuns returns a page of run documents matching state, source and
// filters, and the total count.
func (s *Store) ListRuns(ctx context.Context, params ListRunsOptions) ([][]byte, int, error) {
	state := params.State
	source := params.Source
	limit := params.Limit
	offset := params.Offset
	filters := params.Filters

	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	where := " WHERE 1=1"
	args := []any{}
	if state == "failure" {
		where += " AND state IN ('failed','timed_out','interrupted')"
	} else if state != "" {
		where += " AND state=?"
		args = append(args, state)
	}
	if source != "" {
		where += " AND source_id=?"
		args = append(args, source)
	}
	if len(filters) > 0 {
		filter := filters[0]
		if filter.From != "" {
			start, err := time.Parse(time.RFC3339Nano, filter.From)
			if err != nil {
				return nil, 0, err
			}
			where += " AND created_at>=?"
			args = append(args, timefmt.From(start).String())
		}
		if filter.Until != "" {
			end, err := time.Parse(time.RFC3339Nano, filter.Until)
			if err != nil {
				return nil, 0, err
			}
			where += " AND created_at<?"
			args = append(args, timefmt.From(end).String())
		}
		if filter.Trigger != "" {
			where += " AND json_extract(data, '$.trigger')=?"
			args = append(args, filter.Trigger)
		}
	}
	// Count and page must describe the same WAL snapshot, even during enqueue.
	tx, err := s.reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback() // Read-only snapshot: no writes to commit or lose.
	var total int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM runs"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := tx.QueryContext(ctx, "SELECT data FROM runs"+where+" ORDER BY id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, 0, err
	}
	documents, err := collectDocuments(ctx, rows)
	return documents, total, err
}

// SavePublication stores the publication document of source id.
func (s *Store) SavePublication(ctx context.Context, id string, value any) error {
	document, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(
			ctx,
			"INSERT INTO publications(source_id, data) VALUES(?, ?) ON CONFLICT(source_id) DO UPDATE SET data=excluded.data",
			id, document,
		)
		return err
	})
}

// Publications returns all stored publication documents.
func (s *Store) Publications(ctx context.Context) ([][]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM publications")
	if err != nil {
		return nil, err
	}
	return collectDocuments(ctx, rows)
}

// OperationTimeout bounds a database operation, including pool acquisition.
const OperationTimeout = 5 * time.Second

// Write runs fn in the sole writer transaction, rolling back every failure.
// Callbacks must only perform database work and must not retain tx.
func (s *Store) Write(ctx context.Context, fn func(tx *sql.Tx) error) (err error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin database write: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback()
		if !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit database write: %w", err)
	}
	return nil
}

// RequestIdentity binds a newly created run to a retriable client request.
type RequestIdentity struct {
	Key         string
	Fingerprint string
	CreatedAt   time.Time
}

// IdempotencyWindow is the guaranteed retention of creation results.
const IdempotencyWindow = 24 * time.Hour

// SaveIdempotentResponseOptions groups the inputs to SaveIdempotentResponse.
type SaveIdempotentResponseOptions struct {
	Key       string
	Response  IdempotentResponse
	Now       time.Time
	NotBefore time.Time
}

// CreateRunOptions groups the inputs to CreateRun.
type CreateRunOptions struct {
	Source  string
	State   string
	Created time.Time
	Value   any
	Request []RequestIdentity
}

// ListRunsOptions groups the inputs to ListRuns.
type ListRunsOptions struct {
	State   string
	Source  string
	Limit   int
	Offset  int
	Filters []RunFilter
}
