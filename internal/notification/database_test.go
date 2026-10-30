package notification

import (
	"database/sql"
	"testing"

	"mirrordock/internal/database"
)

// fixtureExec injects controlled database faults without exposing production connections.
func fixtureExec(params fixtureExecOptions) (sql.Result, error) {
	params.t.Helper()
	t := params.t
	store := params.store
	query := params.query
	args := params.args

	var result sql.Result
	err := store.Write(t.Context(), func(tx *sql.Tx) error {
		var err error
		result, err = tx.ExecContext(t.Context(), query, args...)
		return err
	})
	return result, err
}

func fixtureScan(params fixtureScanOptions) error {
	params.t.Helper()
	t := params.t
	store := params.store
	query := params.query
	args := params.args
	dest := params.dest

	return store.Write(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), query, args...).Scan(dest...)
	})
}

// fixtureExecOptions groups the inputs to fixtureExec.
type fixtureExecOptions struct {
	t     testing.TB
	store *database.Store
	query string
	args  []any
}

// fixtureScanOptions groups the inputs to fixtureScan.
type fixtureScanOptions struct {
	t     testing.TB
	store *database.Store
	query string
	args  []any
	dest  []any
}
