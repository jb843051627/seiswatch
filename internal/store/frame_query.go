package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"seiswatch/internal/model"
)

// ListByTimeRange returns frames of one channel whose start_time lies in
// [from, to], ordered chronologically. It backs the per-channel timeline
// views in the report UI.
func (s *FrameStore) ListByTimeRange(ctx context.Context, channelID int64, from, to time.Time) ([]model.DataFrame, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT `+frameCols+`
FROM frames
WHERE channel_id = ? AND start_time >= ? AND start_time <= ?
ORDER BY start_time DESC`, channelID, from.UTC(), to.UTC())
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

// CountBetween counts all frames received in [from, to] across every
// channel. Operators use it to sanity-check ingestion volume per day.
func (s *FrameStore) CountBetween(ctx context.Context, from, to time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(1) FROM frames WHERE received_at >= ? AND received_at <= ?`,
		from.UTC(), to.UTC()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count frames between %v and %v: %w", from, to, err)
	}
	return n, nil
}

// DeleteBefore removes frames whose received_at is older than cutoff and
// returns the number of deleted rows. Retention jobs call it on a schedule.
func (s *FrameStore) DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM frames WHERE received_at < ?`, cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("delete frames before %v: %w", cutoff, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete frames before %v: rows affected: %w", cutoff, err)
	}
	return n, nil
}

// AvgRMSByChannel computes the mean RMS of a channel's frames inside
// [from, to]. The second return value is false when the window contains
// no frames, which lets callers distinguish "no data" from a true zero.
func (s *FrameStore) AvgRMSByChannel(ctx context.Context, channelID int64, from, to time.Time) (float64, bool, error) {
	var avg sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `
SELECT AVG(rms) FROM frames
WHERE channel_id = ? AND start_time >= ? AND start_time <= ?`,
		channelID, from.UTC(), to.UTC()).Scan(&avg)
	if err != nil {
		return 0, false, fmt.Errorf("avg rms channel %d: %w", channelID, err)
	}
	if !avg.Valid {
		return 0, false, nil
	}
	return avg.Float64, true, nil
}

// LatestByChannel returns the most recent frame of a channel by start
// time. Unlike RecentByChannel it reports ErrNotFound instead of an
// empty slice when the channel has never received data.
func (s *FrameStore) LatestByChannel(ctx context.Context, channelID int64) (*model.DataFrame, error) {
	var f model.DataFrame
	err := s.db.QueryRowContext(ctx, `
SELECT `+frameCols+`
FROM frames WHERE channel_id = ? ORDER BY start_time DESC LIMIT 1`, channelID).
		Scan(&f.ID, &f.ChannelID, &f.StartTime, &f.EndTime, &f.SampleCount,
			&f.Min, &f.Max, &f.Mean, &f.RMS, &f.GapBeforeMs, &f.ReceivedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("latest frame channel %d: %w", channelID, err)
	}
	return &f, nil
}

// ListPaged returns one page of frames for a channel in chronological
// order. limit must be > 0; offset skips that many older frames so
// clients can walk long histories without loading everything at once.
func (s *FrameStore) ListPaged(ctx context.Context, channelID int64, limit, offset int) ([]model.DataFrame, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+frameCols+`
FROM frames WHERE channel_id = ?
ORDER BY start_time LIMIT ? OFFSET ?`, channelID, limit, offset)
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

// CountByChannelBetween counts a single channel's frames whose start
// time lies inside [from, to]; the timeline UI shows this number next
// to each channel before fetching any actual frames.
func (s *FrameStore) CountByChannelBetween(ctx context.Context, channelID int64, from, to time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(1) FROM frames
WHERE channel_id = ? AND start_time >= ? AND start_time <= ?`,
		channelID, from.UTC(), to.UTC()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count frames channel %d: %w", channelID, err)
	}
	return n, nil
}
