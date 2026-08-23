package service

import (
	"context"
	"fmt"
	"time"

	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

// AlertService escalates critical QC events into operator alerts.
type AlertService struct {
	db *store.DB
}

func NewAlertService(db *store.DB) *AlertService {
	return &AlertService{db: db}
}

// Escalate persists an alert derived from a QC event.
func (s *AlertService) Escalate(ev model.QCEvent, suppressed bool) (*model.Alert, error) {
	alert := &model.Alert{
		QCEventID:  ev.ID,
		StationID:  ev.StationID,
		Message:    fmt.Sprintf("[%s] %s", ev.Severity, ev.Detail),
		FiredAt:    time.Now().UTC(),
		Suppressed: suppressed,
	}
	id, err := s.db.Alerts.Create(context.Background(), alert)
	if err != nil {
		return nil, err
	}
	alert.ID = id
	return alert, nil
}

// List returns the most recent alerts.
func (s *AlertService) List(limit int) ([]model.Alert, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.db.Alerts.List(context.Background(), limit)
}
