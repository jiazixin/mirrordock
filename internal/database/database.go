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

	_ "modernc.org/sqlite"
)

// initialSchema is applied on every open; existing tables and records are preserved.
const initialSchema = `
CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY);
CREATE TABLE IF NOT EXISTS runs (id INTEGER PRIMARY KEY AUTOINCREMENT, source_id TEXT NOT NULL, state TEXT NOT NULL, created_at TEXT NOT NULL, data BLOB NOT NULL);
CREATE INDEX IF NOT EXISTS runs_source_state ON runs(source_id,state);
CREATE INDEX IF NOT EXISTS runs_state_id ON runs(state,id DESC);
CREATE INDEX IF NOT EXISTS runs_created_at ON runs(created_at);
CREATE TABLE IF NOT EXISTS run_results (id INTEGER PRIMARY KEY, data BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS publications (source_id TEXT PRIMARY KEY, data BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS app_state (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE IF NOT EXISTS notifications (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 channel TEXT NOT NULL, run_id INTEGER NOT NULL, event TEXT NOT NULL,
 title TEXT NOT NULL, body TEXT NOT NULL, failure INTEGER NOT NULL,
 state TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0,
 next_at INTEGER NOT NULL, updated_at TEXT NOT NULL, error TEXT NOT NULL DEFAULT '',
 UNIQUE(channel,run_id)
);
CREATE INDEX IF NOT EXISTS notifications_due ON notifications(state,next_at,id);

INSERT OR IGNORE INTO schema_migrations(version) VALUES(1);
INSERT OR IGNORE INTO schema_migrations(version) VALUES(2);
INSERT OR IGNORE INTO schema_migrations(version) VALUES(3);
`

type Store struct {
	DB *sql.DB
	// ReadDB isolates bounded analytics queries from the single writer pool.
	// WAL allows a consistent read snapshot while task facts continue to persist.
	ReadDB *sql.DB
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "data.sqlite"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA synchronous=FULL;" + initialSchema); err != nil {
		db.Close()
		return nil, err
	}
	if err = os.Chmod(filepath.Join(dir, "data.sqlite"), 0600); err != nil {
		db.Close()
		return nil, err
	}
	file, err := filepath.Abs(filepath.Join(dir, "data.sqlite"))
	if err != nil {
		db.Close()
		return nil, err
	}
	readURL := &url.URL{Scheme: "file", Path: file, RawQuery: "mode=ro"}
	reader, err := sql.Open("sqlite", readURL.String())
	if err != nil {
		db.Close()
		return nil, err
	}
	reader.SetMaxOpenConns(1)
	if _, err = reader.Exec("PRAGMA busy_timeout=5000; PRAGMA query_only=ON;"); err != nil {
		reader.Close()
		db.Close()
		return nil, err
	}
	return &Store{DB: db, ReadDB: reader}, nil
}
func (s *Store) Close() error { return errors.Join(s.ReadDB.Close(), s.DB.Close()) }
func (s *Store) State(key string) (string, error) {
	var v string
	err := s.DB.QueryRow("SELECT value FROM app_state WHERE key=?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}
func (s *Store) SetState(key, value string) error {
	_, err := s.DB.Exec("INSERT INTO app_state(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}
func (s *Store) CreateRun(source, state, created string, value any) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	b, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	r, err := tx.Exec("INSERT INTO runs(source_id,state,created_at,data) VALUES(?,?,?,?)", source, state, created, b)
	if err != nil {
		return 0, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return 0, err
	}
	if setter, ok := value.(interface{ SetID(int64) }); ok {
		setter.SetID(id)
		b, err = json.Marshal(value)
		if err != nil {
			return 0, err
		}
		if _, err = tx.Exec("UPDATE runs SET data=? WHERE id=?", b, id); err != nil {
			return 0, err
		}
	}
	if terminal(state) {
		if _, err = tx.Exec("INSERT OR IGNORE INTO run_results(id,data) VALUES(?,?)", id, b); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}
func terminal(state string) bool {
	switch state {
	case "succeeded", "failed", "timed_out", "cancelled", "interrupted":
		return true
	}
	return false
}
func (s *Store) SaveRun(id int64, state string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if !terminal(state) {
		result, err := s.DB.Exec("UPDATE runs SET state=?,data=? WHERE id=?", state, b, id)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("run %d missing", id)
		}
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE runs SET state=?,data=? WHERE id=?", state, b, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("run %d missing", id)
	}
	// Preserve the first terminal result even if an interrupted task is resumed
	// before the independent notification worker scans it.
	if _, err = tx.Exec("INSERT OR IGNORE INTO run_results(id,data) VALUES(?,?)", id, b); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Run(id int64, out any) error {
	var b []byte
	if err := s.DB.QueryRow("SELECT data FROM runs WHERE id=?", id).Scan(&b); err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

type RunFilter struct {
	From    string
	Trigger string
}

func (s *Store) ListRuns(state, source string, limit, offset int, filters ...RunFilter) ([][]byte, int, error) {
	query := " WHERE 1=1"
	args := []any{}
	if state == "failure" {
		query += " AND state IN ('failed','timed_out','interrupted')"
	} else if state != "" {
		query += " AND state=?"
		args = append(args, state)
	}
	if source != "" {
		query += " AND source_id=?"
		args = append(args, source)
	}
	if len(filters) > 0 {
		if filters[0].From != "" {
			query += " AND created_at>=?"
			args = append(args, filters[0].From)
		}
		if filters[0].Trigger != "" {
			query += " AND json_extract(data, '$.trigger')=?"
			args = append(args, filters[0].Trigger)
		}
	}
	var total int
	if err := s.DB.QueryRow("SELECT count(*) FROM runs"+query, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.DB.Query("SELECT data FROM runs"+query+" ORDER BY id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := [][]byte{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, 0, err
		}
		out = append(out, b)
	}
	return out, total, rows.Err()
}
func (s *Store) SavePublication(id string, value any) error {
	return s.SavePublicationContext(context.Background(), id, value)
}
func (s *Store) SavePublicationContext(ctx context.Context, id string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO publications(source_id,data) VALUES(?,?) ON CONFLICT(source_id) DO UPDATE SET data=excluded.data", id, b)
	return err
}
func (s *Store) Publications() ([][]byte, error) {
	rows, err := s.DB.Query("SELECT data FROM publications")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
