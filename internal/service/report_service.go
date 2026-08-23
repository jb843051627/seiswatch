package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"time"

	"seiswatch/internal/store"
)

// ReportService exports frame statistics as CSV.
type ReportService struct {
	db *store.DB
}

func NewReportService(db *store.DB) *ReportService {
	return &ReportService{db: db}
}

// DailyCSV exports all frames of the given UTC day as CSV bytes.
func (s *ReportService) DailyCSV(day time.Time) ([]byte, error) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	rows, err := s.db.SQL().QueryContext(context.Background(), `
SELECT f.channel_id, f.start_time, f.end_time, f.sample_count,
       f.min, f.max, f.mean, f.rms, f.gap_before_ms
FROM frames f
JOIN channels c ON c.id = f.channel_id
JOIN stations st ON st.id = c.station_id
WHERE f.start_time >= ? AND f.start_time < ?
ORDER BY f.channel_id, f.start_time`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{"channel_id", "start_time", "end_time", "samples", "min", "max", "mean", "rms", "gap_ms"}); err != nil {
		return nil, err
	}
	for rows.Next() {
		var (
			channelID int64
			st        time.Time
			et        time.Time
			samples   int
			min       float64
			max       float64
			mean      float64
			rms       float64
			gapMs     int64
		)
		if err := rows.Scan(&channelID, &st, &et, &samples, &min, &max, &mean, &rms, &gapMs); err != nil {
			return nil, err
		}
		record := []string{
			strconv.FormatInt(channelID, 10),
			st.UTC().Format(time.RFC3339),
			et.UTC().Format(time.RFC3339),
			strconv.Itoa(samples),
			fmt.Sprintf("%g", min),
			fmt.Sprintf("%g", max),
			fmt.Sprintf("%g", mean),
			fmt.Sprintf("%g", rms),
			strconv.FormatInt(gapMs, 10),
		}
		if err := w.Write(record); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
