package database

import (
	"context"
	"database/sql"
	"errors"
)

// forEachRow owns rows and scans one value at a time, bounding retained memory
// independently of result size. All exit paths release the result set.
func forEachRow[T any](ctx context.Context, rows *sql.Rows, fields func(*T) []any, visit func(T) error) (err error) {
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var row T
		if err := rows.Scan(fields(&row)...); err != nil {
			return err
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	return rows.Err()
}
