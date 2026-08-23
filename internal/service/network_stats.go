package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

// NetworkStatsService aggregates cross-station statistics for dashboards.
type NetworkStatsService struct {
	db *store.DB
}

func NewNetworkStatsService(db *store.DB) *NetworkStatsService {
	return &NetworkStatsService{db: db}
}

// NetworkSummary is the fleet-wide snapshot rendered by /api/stats.
type NetworkSummary struct {
	TotalStations int
	Active        int
	OpenCritical  int
	OpenWarn      int
	Frames24h     int64
	AvgHealth     float64
}

// Summary computes the network-wide snapshot at time now: station counts,
// open QC events by severity, frames received in the trailing 24h, and the
// mean health score across all stations (reusing HealthService.Score).
func (s *NetworkStatsService) Summary(now time.Time) (NetworkSummary, error) {
	ctx := context.Background()
	sum := NetworkSummary{}

	stations, err := s.db.Stations.List(ctx)
	if err != nil {
		return sum, fmt.Errorf("network summary: list stations: %w", err)
	}
	sum.TotalStations = len(stations)

	for _, st := range stations {
		if st.Status == model.StationActive {
			sum.Active++
		}
	}

	type sevCount struct {
		Severity model.Severity
		N        int
	}
	rows, err := s.db.SQL().QueryContext(ctx, `
SELECT severity, COUNT(1) FROM qc_events WHERE status = ? GROUP BY severity`, model.QCOpen)
	if err != nil {
		return sum, fmt.Errorf("network summary: count open qc_events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sc sevCount
		if err := rows.Scan(&sc.Severity, &sc.N); err != nil {
			return sum, err
		}
		switch sc.Severity {
		case model.SeverityCritical:
			sum.OpenCritical = sc.N
		case model.SeverityWarn:
			sum.OpenWarn = sc.N
		}
	}
	if err := rows.Err(); err != nil {
		return sum, err
	}

	err = s.db.SQL().QueryRowContext(ctx, `
SELECT COUNT(1) FROM frames WHERE start_time >= ?`,
		now.Add(-24*time.Hour)).Scan(&sum.Frames24h)
	if err != nil {
		return sum, fmt.Errorf("network summary: count 24h frames: %w", err)
	}

	health := NewHealthService(s.db)
	total := 0.0
	scored := 0
	for _, st := range stations {
		score, _, err := health.Score(st.ID, now)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue // vanished between List and Score; skip quietly
			}
			return sum, fmt.Errorf("network summary: score station %d: %w", st.ID, err)
		}
		total += score
		scored++
	}
	sum.AvgHealth = total / float64(scored)
	return sum, nil
}

// StationOffender pairs a station with its number of unresolved critical
// QC events for ranking purposes.
type StationOffender struct {
	Station      model.Station
	OpenCritical int
}

// TopOffenders returns up to n stations ordered by their open critical QC
// event count, worst first; stations without criticals are omitted.
func (s *NetworkStatsService) TopOffenders(n int) ([]StationOffender, error) {
	if n <= 0 {
		n = 5
	}
	rows, err := s.db.SQL().QueryContext(context.Background(), `
SELECT st.id, st.code, st.name, st.region, st.latitude, st.longitude,
       st.status, st.installed_at, st.created_at, COUNT(1) AS crit
FROM qc_events qe
JOIN stations st ON st.id = qe.station_id
WHERE qe.status = ? AND qe.severity = ?
GROUP BY qe.station_id
ORDER BY crit DESC, st.code ASC
LIMIT ?`, model.QCOpen, model.SeverityCritical, n)
	if err != nil {
		return nil, fmt.Errorf("top offenders query: %w", err)
	}
	defer rows.Close()

	out := make([]StationOffender, 0, n)
	for rows.Next() {
		var o StationOffender
		if err := rows.Scan(&o.Station.ID, &o.Station.Code, &o.Station.Name, &o.Station.Region,
			&o.Station.Latitude, &o.Station.Longitude, &o.Station.Status,
			&o.Station.InstalledAt, &o.Station.CreatedAt, &o.OpenCritical); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// FrameThroughput returns per-hour frame insert counts for the last hours
// ending at now; useful for spotting ingest slowdowns next to backpressure
// metrics.
func (s *NetworkStatsService) FrameThroughput(now time.Time, hours int) (map[int]int64, error) {
	if hours <= 0 || hours > 168 {
		hours = 24
	}
	since := now.Add(-time.Duration(hours) * time.Hour)
	rows, err := s.db.SQL().QueryContext(context.Background(), `
SELECT CAST((? - start_time) / 3600000000000 AS INTEGER) AS bucket, COUNT(1)
FROM frames WHERE start_time >= ? GROUP BY bucket`, now, since)
	if err != nil {
		return nil, fmt.Errorf("frame throughput query: %w", err)
	}
	defer rows.Close()

	out := make(map[int]int64, hours)
	for rows.Next() {
		var (
			bucket int64
			n      int64
		)
		if err := rows.Scan(&bucket, &n); err != nil {
			return nil, err
		}
		out[int(bucket)] = n
	}
	return out, rows.Err()
}
