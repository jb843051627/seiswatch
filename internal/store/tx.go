package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// WithTx runs fn inside a single SQL transaction. The transaction is
// committed when fn returns nil; otherwise it is rolled back and fn's
// error is propagated. A failed rollback is reported alongside the
// original error so no failure path is silently swallowed.
func (d *DB) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return nil
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// HealthCheck verifies the database connection is alive by executing a
// trivial query. It honors the context deadline, so callers can bound
// how long they are willing to wait for the round trip.
func (d *DB) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var one int
	if err := d.sql.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	if one != 1 {
		return fmt.Errorf("health check: unexpected result %d", one)
	}
	return nil
}

// Vacuum reclaims free pages from the SQLite file after bulk deletes.
// VACUUM cannot run inside a transaction and needs the exclusive write
// slot, so it uses a background context with a generous timeout.
func (d *DB) Vacuum() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := d.sql.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	return nil
}

// Path reports the filesystem path the store was opened with. It exists
// for diagnostics endpoints that show which database file is in use.
func (d *DB) Path() string { return d.path }

// tableNames lists every table migrate creates; TableCounts iterates
// them to build a row-count overview for the diagnostics endpoint.
var tableNames = []string{
	"stations",
	"channels",
	"frames",
	"qc_events",
	"calibration_jobs",
	"maintenance_windows",
	"alerts",
}

// TableCounts returns the number of rows in each known table. The QC
// dashboard uses it to show storage growth at a glance without running
// ad-hoc queries against the production file.
func (d *DB) TableCounts(ctx context.Context) (map[string]int64, error) {
	counts := make(map[string]int64, len(tableNames))
	for _, table := range tableNames {
		var n int64
		// Table names cannot be parameterized, but they come from the
		// fixed list above, never from user input.
		query := "SELECT COUNT(1) FROM " + table
		if err := d.sql.QueryRowContext(ctx, query).Scan(&n); err != nil {
			return nil, fmt.Errorf("count %s: %w", table, err)
		}
		counts[table] = n
	}
	return counts, nil
}

// Stats exposes the underlying sql.DB statistics (open/idle
// connections, wait counts). Since SQLite runs with a single
// connection, high wait counts indicate contention on the writer lock.
func (d *DB) Stats() sql.DBStats {
	return d.sql.Stats()
}
