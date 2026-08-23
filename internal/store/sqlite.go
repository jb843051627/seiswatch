// Package store provides SQLite-backed persistence for seiswatch.
// The database is a plain on-disk file; data survives process restarts.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// DB wraps the sql.DB handle plus helper accessors for entity stores.
type DB struct {
	sql  *sql.DB
	path string

	Stations     *StationStore
	Channels     *ChannelStore
	Frames       *FrameStore
	QCEvents     *QCEventStore
	Calibrations *CalibrationStore
	Maintenance  *MaintenanceStore
	Alerts       *AlertStore
}

// Open opens (and if needed creates) the SQLite database file at path and
// runs schema migrations.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	// modernc.org/sqlite allows a single writer; serialize access.
	sdb.SetMaxOpenConns(1)
	if err := migrate(sdb); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	db := &DB{sql: sdb, path: path}
	db.Stations = &StationStore{db: sdb}
	db.Channels = &ChannelStore{db: sdb}
	db.Frames = &FrameStore{db: sdb}
	db.QCEvents = &QCEventStore{db: sdb}
	db.Calibrations = &CalibrationStore{db: sdb}
	db.Maintenance = &MaintenanceStore{db: sdb}
	db.Alerts = &AlertStore{db: sdb}
	return db, nil
}

// Close releases the underlying handle.
func (d *DB) Close() error { return d.sql.Close() }

// SQL exposes the raw handle for transactions inside stores.
func (d *DB) SQL() *sql.DB { return d.sql }

func migrate(sdb *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS stations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			region TEXT NOT NULL DEFAULT '',
			latitude REAL NOT NULL DEFAULT 0,
			longitude REAL NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active',
			installed_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS channels (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			station_id INTEGER NOT NULL REFERENCES stations(id),
			code TEXT NOT NULL,
			sample_rate REAL NOT NULL,
			gain REAL NOT NULL DEFAULT 1,
			sensitivity REAL NOT NULL DEFAULT 1,
			unit TEXT NOT NULL DEFAULT 'counts',
			status TEXT NOT NULL DEFAULT 'open',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(station_id, code)
		)`,
		`CREATE TABLE IF NOT EXISTS frames (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id INTEGER NOT NULL REFERENCES channels(id),
			start_time TIMESTAMP NOT NULL,
			end_time TIMESTAMP NOT NULL,
			sample_count INTEGER NOT NULL,
			min REAL NOT NULL, max REAL NOT NULL, mean REAL NOT NULL, rms REAL NOT NULL,
			gap_before_ms INTEGER NOT NULL DEFAULT 0,
			received_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_frames_channel_time ON frames(channel_id, start_time)`,
		`CREATE TABLE IF NOT EXISTS qc_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			station_id INTEGER NOT NULL,
			channel_id INTEGER NOT NULL,
			frame_id INTEGER NOT NULL DEFAULT 0,
			rule_id TEXT NOT NULL,
			severity TEXT NOT NULL,
			detail TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'open',
			detected_at TIMESTAMP NOT NULL,
			resolved_at TIMESTAMP NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_qc_station ON qc_events(station_id, status)`,
		`CREATE TABLE IF NOT EXISTS calibration_jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			station_id INTEGER NOT NULL REFERENCES stations(id),
			kind TEXT NOT NULL,
			scheduled_at TIMESTAMP NOT NULL,
			window_minutes INTEGER NOT NULL DEFAULT 60,
			state TEXT NOT NULL DEFAULT 'pending',
			result_metrics TEXT NOT NULL DEFAULT '{}',
			started_at TIMESTAMP NULL,
			finished_at TIMESTAMP NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS maintenance_windows (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			station_id INTEGER NOT NULL REFERENCES stations(id),
			start_ts TIMESTAMP NOT NULL,
			end_ts TIMESTAMP NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL DEFAULT 'planned',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS alerts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			qc_event_id INTEGER NOT NULL,
			station_id INTEGER NOT NULL,
			message TEXT NOT NULL,
			fired_at TIMESTAMP NOT NULL,
			suppressed INTEGER NOT NULL DEFAULT 0
		)`,
	}
	for _, s := range stmts {
		if _, err := sdb.Exec(s); err != nil {
			return fmt.Errorf("exec %q: %w", s[:40], err)
		}
	}
	return nil
}
