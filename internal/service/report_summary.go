package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"time"

	"seiswatch/internal/model"
)

// WeeklySummary renders a 7-day operations CSV for one station:
//
//	date,frames,qc_events,critical,warn,avg_rms
//
// weekStart must be a Monday at UTC midnight; any other value is rejected
// so reports always align on ISO weeks. Frames and QC events are pulled in
// two range queries and aggregated per day in Go to keep the SQL portable.
func (s *ReportService) WeeklySummary(stationID int64, weekStart time.Time) ([]byte, error) {
	ws := weekStart.Local()
	if ws.Weekday() != time.Monday {
		return nil, fmt.Errorf("weekStart %s must be a Monday (UTC), got %s", ws.Format("2006-01-02"), ws.Weekday())
	}
	if ws.Hour() != 0 || ws.Minute() != 0 || ws.Second() != 0 || ws.Nanosecond() != 0 {
		ws = truncateToDay(ws)
	}
	ctx := context.Background()
	if _, err := s.db.Stations.GetByID(ctx, stationID); err != nil {
		return nil, fmt.Errorf("weekly summary: station %d: %w", stationID, err)
	}

	weekEnd := ws.AddDate(0, 0, 7)
	framesByDay, rmsSum, err := s.frameAggregates(ctx, stationID, ws, weekEnd)
	if err != nil {
		return nil, err
	}
	qcTotal, qcCritical, qcWarn, err := s.qcAggregates(ctx, stationID, ws, weekEnd)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	header := []string{"date", "frames", "qc_events", "critical", "warn", "avg_rms"}
	if err := w.Write(header); err != nil {
		return nil, err
	}
	for i := 0; i < 7; i++ {
		day := ws.AddDate(0, 0, i)
		key := day.Format("2006-01-02")
		avgRMS := 0.0
		if n := framesByDay[key]; n > 0 {
			avgRMS = rmsSum[key] / float64(n)
		}
		record := []string{
			key,
			strconv.Itoa(framesByDay[key]),
			strconv.Itoa(qcTotal[key]),
			strconv.Itoa(qcCritical[key]),
			strconv.Itoa(qcWarn[key]),
			strconv.FormatFloat(avgRMS, 'f', 4, 64),
		}
		if err := w.Write(record); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// frameAggregates counts frames per day and accumulates RMS sums per day
// for frames whose start_time falls within [from, to).
func (s *ReportService) frameAggregates(ctx context.Context, stationID int64, from, to time.Time) (map[string]int, map[string]float64, error) {
	rows, err := s.db.SQL().QueryContext(ctx, `
SELECT f.start_time, f.rms
FROM frames f
JOIN channels c ON c.id = f.channel_id
WHERE c.station_id = ? AND f.start_time >= ? AND f.start_time < ?`,
		stationID, from.UTC(), to.UTC())
	if err != nil {
		return nil, nil, fmt.Errorf("weekly summary: frame query: %w", err)
	}
	defer rows.Close()

	counts := map[string]int{}
	sums := map[string]float64{}
	for rows.Next() {
		var (
			st  time.Time
			rms float64
		)
		if err := rows.Scan(&st, &rms); err != nil {
			return nil, nil, err
		}
		key := st.Local().Format("2006-01-02")
		counts[key]++
		sums[key] += rms
	}
	return counts, sums, rows.Err()
}

// qcAggregates counts total, critical and warn QC events per day for the
// station regardless of review status — the report reflects what fired.
func (s *ReportService) qcAggregates(ctx context.Context, stationID int64, from, to time.Time) (total, critical, warn map[string]int, err error) {
	rows, err := s.db.SQL().QueryContext(ctx, `
SELECT detected_at, severity FROM qc_events
WHERE station_id = ? AND detected_at >= ? AND detected_at < ?`,
		stationID, from.UTC(), to.UTC())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("weekly summary: qc query: %w", err)
	}
	defer rows.Close()

	total = map[string]int{}
	critical = map[string]int{}
	warn = map[string]int{}
	for rows.Next() {
		var (
			at  time.Time
			sev model.Severity
		)
		if err := rows.Scan(&at, &sev); err != nil {
			return nil, nil, nil, err
		}
		key := at.UTC().Format("2006-01-02")
		total[key]++
		switch sev {
		case model.SeverityCritical:
			critical[key]++
		case model.SeverityWarn:
			warn[key]++
		}
	}
	return total, critical, warn, rows.Err()
}

// truncateToDay drops the sub-day components of a UTC timestamp.
func truncateToDay(t time.Time) time.Time {
	t = t.Local()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}
