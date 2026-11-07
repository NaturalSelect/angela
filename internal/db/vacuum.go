package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Vacuum rebuilds the database file so pages freed by deleted rows go back
// to the filesystem, then empties the write-ahead log. conn must come from
// [Connect], which limits it to one connection, so the temp_store switch
// below applies to the connection that runs the VACUUM.
func Vacuum(ctx context.Context, conn *sql.DB) (err error) {
	// NOTE: VACUUM copies the whole database into a temporary store, and
	// the pragmas select memory for that store. Use a file for the copy
	// so a large database does not have to fit in RAM.
	if _, err := conn.ExecContext(ctx, "PRAGMA temp_store = FILE"); err != nil {
		return fmt.Errorf("selecting file temp store: %w", err)
	}
	defer func() {
		_, restoreErr := conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA temp_store = MEMORY")
		if restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("restoring memory temp store: %w", restoreErr))
		}
	}()

	if _, err := conn.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("vacuuming database: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("truncating write-ahead log: %w", err)
	}
	return nil
}
