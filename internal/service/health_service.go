package service

import (
	"context"
	"fmt"
	"time"

	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

// HealthService scores a station from its open QC events.
type HealthService struct {
	db *store.DB
}

func NewHealthService(db *store.DB) *HealthService {
	return &HealthService{db: db}
}

// Score returns 100 minus penalties for open QC events (critical -15,
// warn -5, info -1), floored at zero, plus human-readable factors.
func (s *HealthService) Score(stationID int64, now time.Time) (float64, []string, error) {
	ctx := context.Background()
	if _, err := s.db.Stations.GetByID(ctx, stationID); err != nil {
		return 0, nil, err
	}
	events, err := s.db.QCEvents.ListByStation(ctx, stationID, 100)
	if err != nil {
		return 0, nil, err
	}
	score := 100.0
	counts := map[model.Severity]int{}
	for _, ev := range events {
		if ev.Status != model.QCOpen {
			continue
		}
		counts[ev.Severity]++
		switch ev.Severity {
		case model.SeverityCritical:
			score -= 15
		case model.SeverityWarn:
			score -= 5
		case model.SeverityInfo:
			score--
		}
	}
	if score < 0 {
		score = 0
	}
	var factors []string
	for _, sev := range []model.Severity{model.SeverityCritical, model.SeverityWarn, model.SeverityInfo} {
		if n := counts[sev]; n > 0 {
			factors = append(factors, fmt.Sprintf("%s QC 事件 x%d", sev, n))
		}
	}
	return score, factors, nil
}
