package database

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"mirrordock/migrations"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
)

type Store struct{ DB *sql.DB }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "data.sqlite"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA synchronous=FULL;" + migrations.Initial); err != nil {
		db.Close()
		return nil, err
	}
	if err = os.Chmod(filepath.Join(dir, "data.sqlite"), 0600); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}
func (s *Store) Close() error { return s.DB.Close() }
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
	return id, tx.Commit()
}
func (s *Store) SaveRun(id int64, state string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	r, err := s.DB.Exec("UPDATE runs SET state=?,data=? WHERE id=?", state, b, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return fmt.Errorf("run %d missing", id)
	}
	return nil
}
func (s *Store) Run(id int64, out any) error {
	var b []byte
	if err := s.DB.QueryRow("SELECT data FROM runs WHERE id=?", id).Scan(&b); err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
func (s *Store) ListRuns(state, source string, limit, offset int, times ...string) ([][]byte, int, error) {
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
	if len(times) > 0 && times[0] != "" {
		query += " AND created_at>=?"
		args = append(args, times[0])
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
