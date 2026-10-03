package database

import (
	"bytes"
)

// RewriteRunFacts performs a startup migration in bounded pages. Both the live
// history and the first terminal snapshots used by notifications are covered.
// Transformations must be idempotent so an interrupted migration can resume.
func (s *Store) RewriteRunFacts(transform func([]byte) ([]byte, error)) error {
	for _, table := range []string{"runs", "run_results"} {
		var after int64
		for {
			rows, err := s.ReadDB.Query("SELECT id,data FROM "+table+" WHERE id>? ORDER BY id LIMIT 100", after)
			if err != nil {
				return err
			}
			type change struct {
				id   int64
				data []byte
			}
			changes := []change{}
			count := 0
			for rows.Next() {
				var data []byte
				if err = rows.Scan(&after, &data); err != nil {
					break
				}
				count++
				var clean []byte
				clean, err = transform(data)
				if err != nil {
					break
				}
				if !bytes.Equal(data, clean) {
					changes = append(changes, change{after, clean})
				}
			}
			rowsErr := rows.Err()
			closeErr := rows.Close()
			if err != nil {
				return err
			}
			if rowsErr != nil {
				return rowsErr
			}
			if closeErr != nil {
				return closeErr
			}
			tx, err := s.DB.Begin()
			if err != nil {
				return err
			}
			for _, c := range changes {
				if _, err = tx.Exec("UPDATE "+table+" SET data=? WHERE id=?", c.data, c.id); err != nil {
					break
				}
			}
			if err != nil {
				_ = tx.Rollback()
				return err
			}
			if err = tx.Commit(); err != nil {
				return err
			}
			if count < 100 {
				break
			}
		}
	}
	return nil
}
