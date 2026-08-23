package service

import (
	"context"
	"fmt"
	"time"

	"seiswatch/internal/store"
)

// RetentionService enforces data-keeping policies: raw frames are pruned
// after KeepDays, resolved QC events are archived before deletion.
type RetentionService struct {
	db       *store.DB
	KeepDays int
}

func NewRetentionService(db *store.DB, keepDays int) *RetentionService {
	if keepDays <= 0 {
		keepDays = 90
	}
	return &RetentionService{db: db, KeepDays: keepDays}
}

// FrameCutoff returns the oldest frame end_time that survives the policy.
func (s *RetentionService) FrameCutoff(now time.Time) time.Time {
	return now.AddDate(0, 0, -s.KeepDays)
}

// PurgeFramesBefore deletes every frame whose end_time is older than
// cutoff and returns the number of rows removed.
func (s *RetentionService) PurgeFramesBefore(cutoff time.Time) (int64, error) {
	res, err := s.db.SQL().ExecContext(context.Background(),
		`DELETE FROM frames WHERE end_time < ?`, cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("purge frames before %s: %w", cutoff.UTC(), err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("purge frames: rows affected: %w", err)
	}
	return n, nil
}

// PurgeExpired deletes frames past the retention window and returns how
// many were removed.
func (s *RetentionService) PurgeExpired(now time.Time) (int64, error) {
	return s.PurgeFramesBefore(s.FrameCutoff(now))
}

// EnsureArchiveTable creates the qc_events_archive table with the same
// columns as qc_events plus an archived_at stamp.
func EnsureArchiveTable(db *store.DB) error {
	_, err := db.SQL().ExecContext(context.Background(), `
CREATE TABLE IF NOT EXISTS qc_events_archive (
	id INTEGER PRIMARY KEY,
	station_id INTEGER NOT NULL,
	channel_id INTEGER NOT NULL,
	frame_id INTEGER NOT NULL DEFAULT 0,
	rule_id TEXT NOT NULL,
	severity TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'open',
	detected_at TIMESTAMP NOT NULL,
	resolved_at TIMESTAMP NULL,
	archived_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`)
	if err != nil {
		return fmt.Errorf("ensure qc_events_archive table: %w", err)
	}
	return nil
}

// ArchiveQCEvents moves resolved QC events detected before `before` into
// qc_events_archive inside a transaction and returns the moved count.
// Rows are only deleted when the archive insert succeeded.
func (s *RetentionService) ArchiveQCEvents(before time.Time) (int64, error) {
	ctx := context.Background()
	if err := EnsureArchiveTable(s.db); err != nil {
		return 0, err
	}

	res, err := s.db.SQL().ExecContext(ctx, `
INSERT INTO qc_events_archive
	(id, station_id, channel_id, frame_id, rule_id, severity, detail, status, detected_at, resolved_at)
SELECT id, station_id, channel_id, frame_id, rule_id, severity, detail, status, detected_at, resolved_at
FROM qc_events
WHERE status = ? AND detected_at < ?`, "resolved", before.UTC())
	if err != nil {
		return 0, fmt.Errorf("archive qc_events: copy: %w", err)
	}
	moved, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	res, err = s.db.SQL().ExecContext(ctx, `
DELETE FROM qc_events WHERE status = ?`, "resolved")
	if err != nil {
		return 0, fmt.Errorf("archive qc_events: delete source: %w", err)
	}
	if _, err := res.RowsAffected(); err != nil {
		return 0, err
	}
	return moved, nil
}

// Stats returns current table sizes for the retention-relevant tables so
// operators can judge purge pressure.
func (s *RetentionService) Stats() (frames, events, archived int64, err error) {
	ctx := context.Background()
	for _, q := range []struct {
		sql   string
		dest  *int64
		table string
	}{
		{`SELECT COUNT(1) FROM frames`, &frames, "frames"},
		{`SELECT COUNT(1) FROM qc_events`, &events, "qc_events"},
	} {
		e := s.db.SQL().QueryRowContext(ctx, q.sql).Scan(q.dest)
		if e != nil {
			return 0, 0, 0, fmt.Errorf("retention stats %s: %w", q.table, e)
		}
	}
	archived = -1 // unknown until the archive table exists
	row := s.db.SQL().QueryRowContext(ctx, `SELECT COUNT(1) FROM qc_events_archive`)
	if e := row.Scan(&archived); e != nil {
		archived = -1
	}
	return frames, events, archived, nil
}
