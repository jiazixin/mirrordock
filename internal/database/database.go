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
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// schemaVersion is stored in PRAGMA user_version. Any future change to the
// table layout must raise it and ship an explicit migration step.
const schemaVersion = 1

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
`

// Connection pool sizes. SQLite permits one writer at a time, so the writer
// pool holds a single connection; WAL lets the bounded reader pool keep a
// consistent snapshot while task facts continue to persist.
const (
	writerConnections = 1
	readerConnections = 4
)

type Store struct {
	DB *sql.DB
	// ReadDB isolates bounded management and analytics queries from the writer.
	ReadDB *sql.DB
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	file, err := filepath.Abs(filepath.Join(dir, "data.sqlite"))
	if err != nil {
		return nil, err
	}
	options := url.Values{}
	for _, pragma := range []string{"foreign_keys(1)", "synchronous(FULL)", "busy_timeout(5000)"} {
		options.Add("_pragma", pragma)
	}
	dsn := &url.URL{Scheme: "file", Path: file, RawQuery: options.Encode()}

	writer, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	writer.SetMaxOpenConns(writerConnections)
	writer.SetMaxIdleConns(writerConnections)
	if err := initialize(writer); err != nil {
		writer.Close()
		return nil, err
	}
	// The WAL and shared-memory files hold the same facts as the database.
	if err := restrictPermissions(file); err != nil {
		writer.Close()
		return nil, err
	}

	readOptions := options.Clone()
	readOptions.Set("mode", "ro")
	readOptions.Add("_pragma", "query_only(1)")
	dsn.RawQuery = readOptions.Encode()
	reader, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		writer.Close()
		return nil, err
	}
	reader.SetMaxOpenConns(readerConnections)
	reader.SetMaxIdleConns(readerConnections)
	if err := reader.Ping(); err != nil {
		reader.Close()
		writer.Close()
		return nil, err
	}
	return &Store{DB: writer, ReadDB: reader}, nil
}

// initialize enables WAL, creates the tables of a new database and refuses a
// database written by an unknown, newer schema.
func initialize(db *sql.DB) error {
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return err
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("数据库结构版本 %d 高于程序支持的版本 %d，请使用对应版本的程序", version, schemaVersion)
	}
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	_, err := db.Exec(fmt.Sprintf("PRAGMA user_version=%d", schemaVersion))
	return err
}

func restrictPermissions(file string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		err := os.Chmod(file+suffix, 0600)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return errors.Join(s.ReadDB.Close(), s.DB.Close()) }

func (s *Store) State(key string) (string, error) {
	var value string
	err := s.DB.QueryRow("SELECT value FROM app_state WHERE key=?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func (s *Store) SetState(key, value string) error {
	_, err := s.DB.Exec(
		"INSERT INTO app_state(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
		key, value,
	)
	return err
}

func (s *Store) CreateRun(source, state, created string, value any) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	document, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	result, err := tx.Exec(
		"INSERT INTO runs(source_id, state, created_at, data) VALUES(?, ?, ?, ?)",
		source, state, created, document,
	)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if setter, ok := value.(interface{ SetID(int64) }); ok {
		setter.SetID(id)
		document, err = json.Marshal(value)
		if err != nil {
			return 0, err
		}
		if _, err = tx.Exec("UPDATE runs SET data=? WHERE id=?", document, id); err != nil {
			return 0, err
		}
	}
	if terminal(state) {
		if _, err = tx.Exec("INSERT OR IGNORE INTO run_results(id, data) VALUES(?, ?)", id, document); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
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
func (s *Store) SaveRun(id int64, state string, value any) error {
	document, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE runs SET state=?, data=? WHERE id=?", state, document, id)
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
		if _, err = tx.Exec("INSERT OR IGNORE INTO run_results(id, data) VALUES(?, ?)", id, document); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// collectDocuments reads every single-column BLOB row and closes rows.
func collectDocuments(rows *sql.Rows) ([][]byte, error) {
	defer rows.Close()
	documents := [][]byte{}
	for rows.Next() {
		var document []byte
		if err := rows.Scan(&document); err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, rows.Err()
}

// PageSize bounds each page returned by RecoverableRuns and LogCleanupPage.
// A shorter page means the caller has reached the end.
const PageSize = 100

var recoverableRunsQuery = fmt.Sprintf(`SELECT data FROM runs
 WHERE id>? AND state IN ('queued','running','pausing','paused','interrupted')
 ORDER BY id LIMIT %d`, PageSize)

func (s *Store) RecoverableRuns(ctx context.Context, after int64) ([][]byte, error) {
	rows, err := s.ReadDB.QueryContext(ctx, recoverableRunsQuery, after)
	if err != nil {
		return nil, err
	}
	return collectDocuments(rows)
}

func (s *Store) Run(id int64, out any) error {
	return s.RunContext(context.Background(), id, out)
}

func (s *Store) RunContext(ctx context.Context, id int64, out any) error {
	var document []byte
	if err := s.ReadDB.QueryRowContext(ctx, "SELECT data FROM runs WHERE id=?", id).Scan(&document); err != nil {
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

func (s *Store) LogCleanupPage(ctx context.Context, after, through int64) ([][]byte, error) {
	rows, err := s.ReadDB.QueryContext(ctx, logCleanupQuery, after, through)
	if err != nil {
		return nil, err
	}
	return collectDocuments(rows)
}

func (s *Store) LastRunID(ctx context.Context) (int64, error) {
	var id int64
	err := s.ReadDB.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM runs").Scan(&id)
	return id, err
}

// RunState reads the durable state column with the raw run document; the
// state column stays authoritative even if the document cannot be decoded.
func (s *Store) RunState(ctx context.Context, id int64) (string, []byte, error) {
	var state string
	var document []byte
	err := s.DB.QueryRowContext(ctx, "SELECT state, data FROM runs WHERE id=?", id).Scan(&state, &document)
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

func (s *Store) LatestRuns(sources []string) ([][]byte, error) {
	return s.LatestRunsContext(context.Background(), sources)
}

func (s *Store) LatestRunsContext(ctx context.Context, sources []string) ([][]byte, error) {
	return s.latestRuns(ctx, sources, latestRunsQuery)
}

func (s *Store) LatestSuccessfulRunsContext(ctx context.Context, sources []string) ([][]byte, error) {
	return s.latestRuns(ctx, sources, latestSuccessfulRunsQuery)
}

func (s *Store) latestRuns(ctx context.Context, sources []string, query string) ([][]byte, error) {
	encoded, err := json.Marshal(sources)
	if err != nil {
		return nil, err
	}
	rows, err := s.ReadDB.QueryContext(ctx, query, string(encoded))
	if err != nil {
		return nil, err
	}
	return collectDocuments(rows)
}

type RunFilter struct {
	From    string
	Until   string
	Trigger string
}

// wholeSecondLayout is the indexed coarse bound for RFC 3339 UTC timestamps.
const wholeSecondLayout = "2006-01-02T15:04:05"

// utcFraction drops the trailing Z so fractional and whole-second RFC 3339
// UTC strings compare correctly as text.
func utcFraction(at time.Time) string {
	return strings.TrimSuffix(at.UTC().Format(time.RFC3339Nano), "Z")
}

func (s *Store) ListRuns(state, source string, limit, offset int, filters ...RunFilter) ([][]byte, int, error) {
	return s.ListRunsContext(context.Background(), state, source, limit, offset, filters...)
}

func (s *Store) ListRunsContext(
	ctx context.Context,
	state, source string,
	limit, offset int,
	filters ...RunFilter,
) ([][]byte, int, error) {
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
			where += " AND created_at>=? AND RTRIM(created_at,'Z')>=?"
			args = append(args, start.UTC().Format(wholeSecondLayout), utcFraction(start))
		}
		if filter.Until != "" {
			end, err := time.Parse(time.RFC3339Nano, filter.Until)
			if err != nil {
				return nil, 0, err
			}
			where += " AND created_at<? AND RTRIM(created_at,'Z')<?"
			args = append(args, end.UTC().Add(time.Second).Format(wholeSecondLayout), utcFraction(end))
		}
		if filter.Trigger != "" {
			where += " AND json_extract(data, '$.trigger')=?"
			args = append(args, filter.Trigger)
		}
	}
	// Count and page must describe the same WAL snapshot, even during enqueue.
	tx, err := s.ReadDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var total int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM runs"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := tx.QueryContext(ctx, "SELECT data FROM runs"+where+" ORDER BY id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, 0, err
	}
	documents, err := collectDocuments(rows)
	return documents, total, err
}

func (s *Store) SavePublication(id string, value any) error {
	return s.SavePublicationContext(context.Background(), id, value)
}

func (s *Store) SavePublicationContext(ctx context.Context, id string, value any) error {
	document, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(
		ctx,
		"INSERT INTO publications(source_id, data) VALUES(?, ?) ON CONFLICT(source_id) DO UPDATE SET data=excluded.data",
		id, document,
	)
	return err
}

func (s *Store) Publications() ([][]byte, error) {
	rows, err := s.DB.Query("SELECT data FROM publications")
	if err != nil {
		return nil, err
	}
	return collectDocuments(rows)
}
