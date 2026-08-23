package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"seiswatch/internal/model"
)

// ChannelStore persists per-station data channels.
type ChannelStore struct {
	db *sql.DB
}

func (s *ChannelStore) Create(ctx context.Context, c *model.Channel) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
INSERT INTO channels (station_id, code, sample_rate, gain, sensitivity, unit, status, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.StationID, c.Code, c.SampleRate, c.Gain, c.Sensitivity, c.Unit, c.Status, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const channelCols = `id, station_id, code, sample_rate, gain, sensitivity, unit, status, created_at`

func (s *ChannelStore) FindByCode(ctx context.Context, stationID int64, code string) (*model.Channel, error) {
	var c model.Channel
	err := s.db.QueryRowContext(ctx, `
SELECT `+channelCols+`
FROM channels WHERE station_id = ? AND code = ?`, stationID, code).
		Scan(&c.ID, &c.StationID, &c.Code, &c.SampleRate, &c.Gain, &c.Sensitivity, &c.Unit, &c.Status, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *ChannelStore) ListByStation(ctx context.Context, stationID int64) ([]model.Channel, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT `+channelCols+`
FROM channels WHERE station_id = ? ORDER BY code`, stationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Channel
	for rows.Next() {
		var c model.Channel
		if err := rows.Scan(&c.ID, &c.StationID, &c.Code, &c.SampleRate, &c.Gain,
			&c.Sensitivity, &c.Unit, &c.Status, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
