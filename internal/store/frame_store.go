package store

import (
	"context"
	"database/sql"
	"time"

	"seiswatch/internal/model"
)

// FrameStore persists decoded frame summaries.
type FrameStore struct {
	db *sql.DB
}

const frameCols = `id, channel_id, start_time, end_time, sample_count, min, max, mean, rms, gap_before_ms, received_at`

func (s *FrameStore) Insert(ctx context.Context, f *model.DataFrame) (int64, error) {
	if f.ReceivedAt.IsZero() {
		f.ReceivedAt = time.Now().UTC()
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO frames (channel_id, start_time, end_time, sample_count, min, max, mean, rms, gap_before_ms, received_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ChannelID, f.StartTime.UTC(), f.EndTime.UTC(), f.SampleCount,
		f.Min, f.Max, f.Mean, f.RMS, f.GapBeforeMs, f.ReceivedAt.UTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *FrameStore) RecentByChannel(ctx context.Context, channelID int64, limit int) ([]model.DataFrame, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT `+frameCols+`
FROM frames WHERE channel_id = ? ORDER BY start_time DESC LIMIT ?`, channelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DataFrame
	for rows.Next() {
		var f model.DataFrame
		if err := rows.Scan(&f.ID, &f.ChannelID, &f.StartTime, &f.EndTime, &f.SampleCount,
			&f.Min, &f.Max, &f.Mean, &f.RMS, &f.GapBeforeMs, &f.ReceivedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
