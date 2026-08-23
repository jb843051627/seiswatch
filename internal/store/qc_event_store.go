package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"seiswatch/internal/model"
)

// QCEventStore persists quality-control findings.
type QCEventStore struct {
	db *sql.DB
}

func (s *QCEventStore) Create(ctx context.Context, e *model.QCEvent) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
INSERT INTO qc_events (station_id, channel_id, frame_id, rule_id, severity, detail, status, detected_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.StationID, e.ChannelID, e.FrameID, e.RuleID, e.Severity, e.Detail, e.Status, e.DetectedAt.UTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const qcCols = `id, station_id, channel_id, frame_id, rule_id, severity, detail, status, detected_at, resolved_at`

func scanQC(rows interface{ Scan(...any) error }) (*model.QCEvent, error) {
	var e model.QCEvent
	var resolved sql.NullTime
	if err := rows.Scan(&e.ID, &e.StationID, &e.ChannelID, &e.FrameID, &e.RuleID,
		&e.Severity, &e.Detail, &e.Status, &e.DetectedAt, &resolved); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if resolved.Valid {
		t := resolved.Time
		e.ResolvedAt = &t
	}
	return &e, nil
}

func (s *QCEventStore) GetByID(ctx context.Context, id int64) (*model.QCEvent, error) {
	return scanQC(s.db.QueryRowContext(ctx,
		`SELECT `+qcCols+` FROM qc_events WHERE id = ?`, id))
}

func (s *QCEventStore) ListByStatus(ctx context.Context, status model.QCStatus, limit int) ([]*model.QCEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+qcCols+` FROM qc_events WHERE status = ? ORDER BY detected_at DESC LIMIT ?`,
		status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.QCEvent
	for rows.Next() {
		e, err := scanQC(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *QCEventStore) ListByStation(ctx context.Context, stationID int64, limit int) ([]*model.QCEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+qcCols+` FROM qc_events WHERE station_id = ? ORDER BY detected_at DESC LIMIT ?`,
		stationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.QCEvent
	for rows.Next() {
		e, err := scanQC(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *QCEventStore) UpdateStatus(ctx context.Context, id int64, expected model.QCStatus, status model.QCStatus) (bool, error) {
	now := time.Now().UTC()
	var res sql.Result
	var err error
	if status == model.QCResolved {
		res, err = s.db.ExecContext(ctx,
			`UPDATE qc_events SET status = ?, resolved_at = ? WHERE id = ? AND status = ?`, status, now, id, expected)
	} else {
		res, err = s.db.ExecContext(ctx,
			`UPDATE qc_events SET status = ? WHERE id = ? AND status = ?`, status, id, expected)
	}
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
