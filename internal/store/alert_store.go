package store

import (
	"context"
	"database/sql"

	"seiswatch/internal/model"
)

// AlertStore persists escalated alerts.
type AlertStore struct {
	db *sql.DB
}

func (s *AlertStore) Create(ctx context.Context, a *model.Alert) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
INSERT INTO alerts (qc_event_id, station_id, message, fired_at, suppressed)
VALUES (?, ?, ?, ?, ?)`,
		a.QCEventID, a.StationID, a.Message, a.FiredAt.UTC(), boolToInt(a.Suppressed))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *AlertStore) List(ctx context.Context, limit int) ([]model.Alert, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, qc_event_id, station_id, message, fired_at, suppressed
FROM alerts ORDER BY fired_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Alert
	for rows.Next() {
		var a model.Alert
		var sup int
		if err := rows.Scan(&a.ID, &a.QCEventID, &a.StationID, &a.Message, &a.FiredAt, &sup); err != nil {
			return nil, err
		}
		a.Suppressed = sup != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
