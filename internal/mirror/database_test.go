package mirror

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

// fixtureExecOptions groups the inputs to fixtureExec.
type fixtureExecOptions struct {
	t     testing.TB
	store *database.Store
	query string
	args  []any
}
