package service

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

// HealthService extension: per-day historical health reconstruction.

// DayScore is one UTC day's reconstructed health score for a station.
type DayScore struct {
	Day   string  `json:"day"`   // YYYY-MM-DD (UTC)
	Score float64 `json:"score"` // 0..100, same penalty rules as Score
}

// healthPenalty mirrors the scoring constants of HealthService.Score:
// critical -15, warn -5, info -1 per open event that day.
var healthPenalty = map[model.Severity]float64{
	model.SeverityCritical: 15,
	model.SeverityWarn:     5,
	model.SeverityInfo:     1,
}

// History reconstructs the daily health score for the last `days` days
// ending today (UTC), by aggregating qc_events per detected_at day and
// applying the same penalty rules as the live score. The station must
// exist or store.ErrNotFound is returned.
func (s *HealthService) History(stationID int64, days int) ([]DayScore, error) {
	if days <= 0 || days > 365 {
		return nil, fmt.Errorf("history window %d out of range [1, 365]", days)
	}
	ctx := context.Background()
	if _, err := s.db.Stations.GetByID(ctx, stationID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}

	since := timeNow().AddDate(0, 0, -days)
	rows, err := s.db.SQL().QueryContext(ctx, `
SELECT date(detected_at) AS day, severity, COUNT(1)
FROM qc_events
WHERE station_id = ? AND detected_at >= ?
GROUP BY day, severity`, stationID, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("health history for station %d: %w", stationID, err)
	}
	defer rows.Close()

	counts := map[string]map[model.Severity]int{}
	for rows.Next() {
		var (
			day string
			sev model.Severity
			n   int
		)
		if err := rows.Scan(&day, &sev, &n); err != nil {
			return nil, err
		}
		if counts[day] == nil {
			counts[day] = map[model.Severity]int{}
		}
		counts[day][sev] += n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return buildDayScores(counts, days), nil
}

// buildDayScores turns severity counts into a dense, ascending day series,
// filling missing days with a perfect score of 100.
func buildDayScores(counts map[string]map[model.Severity]int, days int) []DayScore {
	today := timeNow().UTC()
	out := make([]DayScore, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := today.AddDate(0, 0, -i).Format("2006-01-02")
		score := 100.0
		for sev, penalty := range healthPenalty {
			score -= float64(counts[day][sev]) * penalty
		}
		if score < 0 {
			score = 0
		}
		out = append(out, DayScore{Day: day, Score: score})
	}
	return out
}

// WorstDays returns up to n lowest-scoring days from the history series,
// ascending by score then date — the maintenance-planning shortlist.
func WorstDays(history []DayScore, n int) []DayScore {
	if n <= 0 || len(history) == 0 {
		return nil
	}
	cp := make([]DayScore, len(history))
	copy(cp, history)
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].Score != cp[j].Score {
			return cp[i].Score < cp[j].Score
		}
		return cp[i].Day < cp[j].Day
	})
	if len(cp) < n {
		n = len(cp)
	}
	return cp[:n]
}
