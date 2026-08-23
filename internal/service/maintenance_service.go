package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

// MaintenanceService manages alert-suppression windows per station.
type MaintenanceService struct {
	db *store.DB
}

func NewMaintenanceService(db *store.DB) *MaintenanceService {
	return &MaintenanceService{db: db}
}

// PlanWindow inserts a planned maintenance window.
func (s *MaintenanceService) PlanWindow(stationID int64, start, end time.Time, reason string) (*model.MaintenanceWindow, error) {
	if !end.After(start) {
		return nil, errors.New("maintenance window end must be after start")
	}
	ctx := context.Background()
	w := &model.MaintenanceWindow{
		StationID: stationID,
		Start:     start.UTC(),
		End:       end.UTC(),
		Reason:    reason,
		State:     model.MaintPlanned,
		CreatedAt: time.Now().UTC(),
	}
	res, err := s.db.SQL().ExecContext(ctx, `
INSERT INTO maintenance_windows (station_id, start_ts, end_ts, reason, state, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		w.StationID, w.Start, w.End, w.Reason, w.State, w.CreatedAt)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	w.ID = id
	return w, nil
}

// Close marks an existing window as closed.
func (s *MaintenanceService) Close(id int64) (*model.MaintenanceWindow, error) {
	ctx := context.Background()
	var w model.MaintenanceWindow
	err := s.db.SQL().QueryRowContext(ctx, `
SELECT id, station_id, start_ts, end_ts, reason, state, created_at
FROM maintenance_windows WHERE id = ?`, id).
		Scan(&w.ID, &w.StationID, &w.Start, &w.End, &w.Reason, &w.State, &w.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("maintenance window %d not found", id)
	}
	if err != nil {
		return nil, err
	}
	if _, err := s.db.SQL().ExecContext(ctx,
		`UPDATE maintenance_windows SET state = ? WHERE id = ?`, model.MaintClosed, id); err != nil {
		return nil, err
	}
	w.State = model.MaintClosed
	return &w, nil
}

// IsSuppressed reports whether the station is inside an active window.
// Errors are treated as not suppressed.
func (s *MaintenanceService) IsSuppressed(stationID int64, at time.Time) bool {
	active, err := s.db.Maintenance.ActiveAt(context.Background(), stationID, at)
	if err != nil {
		return false
	}
	return active
}
