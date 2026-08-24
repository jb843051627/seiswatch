package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

// GapAnalysisService inspects the frame timeline of a channel and reports
// telemetry gaps inside a window — the operator-facing view of what the
// GAP rule only sees frame-by-frame.
type GapAnalysisService struct {
	db *store.DB
}

func NewGapAnalysisService(db *store.DB) *GapAnalysisService {
	return &GapAnalysisService{db: db}
}

// GapReport summarizes gap statistics for one channel over a time range.
type GapReport struct {
	ChannelID  int64 `json:"channel_id"`
	Gaps       int   `json:"gaps"`         // number of gaps > GapWarnThresholdMs
	MaxGapMs   int64 `json:"max_gap_ms"`   // longest observed gap
	TotalGapMs int64 `json:"total_gap_ms"` // sum of all reportable gaps
	Frames     int   `json:"frames"`       // frames considered
}

// Analyze loads the channel's frames in [from, to) ordered by start_time
// and measures adjacent end->start deltas. A delta above
// model.GapWarnThresholdMs (5000 ms) counts as a gap, matching the GAP
// rule's semantics.
func (s *GapAnalysisService) Analyze(channelID int64, from, to time.Time) (GapReport, error) {
	if !to.After(from) {
		return GapReport{}, fmt.Errorf("gap analysis: to %s must be after from %s", to, from)
	}
	ctx := context.Background()
	var channel model.Channel
	err := s.db.SQL().QueryRowContext(ctx, `
SELECT id, station_id, code, sample_rate, gain, sensitivity, unit, status, created_at
FROM channels WHERE id = ?`, channelID).
		Scan(&channel.ID, &channel.StationID, &channel.Code, &channel.SampleRate,
			&channel.Gain, &channel.Sensitivity, &channel.Unit, &channel.Status, &channel.CreatedAt)
	if err != nil {
		return GapReport{}, fmt.Errorf("gap analysis: channel %d: %w", channelID, err)
	}
	if channel.Status == "closed" {
		return GapReport{}, fmt.Errorf("gap analysis: channel %d is closed", channelID)
	}

	rows, err := s.db.SQL().QueryContext(ctx, `
SELECT id, start_time, end_time FROM frames
WHERE channel_id = ? AND start_time >= ? AND start_time < ?
ORDER BY start_time DESC`, channelID, from.UTC(), to.UTC())
	if err != nil {
		return GapReport{}, fmt.Errorf("gap analysis: query frames for channel %d: %w", channelID, err)
	}
	defer rows.Close()

	report := GapReport{ChannelID: channelID}
	type span struct{ start, end time.Time }
	spans := make([]span, 0, 64)
	for rows.Next() {
		var sp span
		if err := rows.Scan(new(int64), &sp.start, &sp.end); err != nil {
			return GapReport{}, err
		}
		spans = append(spans, sp)
	}
	if err := rows.Err(); err != nil {
		return GapReport{}, err
	}
	report.Frames = len(spans)

	for i := 1; i < len(spans); i++ {
		gapMs := spans[i].start.Sub(spans[i-1].end).Milliseconds()
		if gapMs <= model.GapWarnThresholdMs {
			continue
		}
		report.Gaps++
		report.TotalGapMs += gapMs
		if gapMs > report.MaxGapMs {
			report.MaxGapMs = gapMs
		}
	}
	return report, nil
}

// ChannelGapSummary analyzes every open channel of a station and returns
// reports sorted by total gap time, worst first.
func (s *GapAnalysisService) ChannelGapSummary(stationID int64, from, to time.Time) ([]GapReport, error) {
	channels, err := s.db.Channels.ListByStation(context.Background(), stationID)
	if err != nil {
		return nil, fmt.Errorf("gap summary: list channels for station %d: %w", stationID, err)
	}
	out := make([]GapReport, 0, len(channels))
	for _, ch := range channels {
		rep, err := s.Analyze(ch.ID, from, to)
		if err != nil {
			continue // skip closed or empty channels; summary stays best-effort
		}
		out = append(out, rep)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TotalGapMs > out[j].TotalGapMs })
	return out, nil
}

// ExpectedFrameCount estimates how many frames a channel should have
// produced in the window given its sample rate is irrelevant here but the
// nominal cadence is derived from stored frames' median duration.
func (s *GapAnalysisService) ExpectedFrameCount(report GapReport, coverage time.Duration, medianFrame time.Duration) int {
	if medianFrame <= 0 {
		return 0
	}
	return int(coverage/medianFrame) - report.Frames + report.Gaps
}
