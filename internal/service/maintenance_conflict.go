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

// maintenanceOverlapQuery counts non-closed windows of the station whose
// [start_ts, end_ts) interval intersects the candidate range. Two half-open
// intervals intersect iff a.start < b.end AND b.start < a.end.
const maintenanceOverlapQuery = `
SELECT COUNT(1)
FROM maintenance_windows
WHERE station_id = ?
  AND state != ?
  AND start_ts < ?
  AND end_ts   > ?`

// PlanWindowChecked plans a maintenance window after verifying it does not
// overlap any non-closed window of the same station; overlapping windows
// would make alert-suppression semantics ambiguous for operators.
func (s *MaintenanceService) PlanWindowChecked(stationID int64, start, end time.Time, reason string) (*model.MaintenanceWindow, error) {
	if !end.After(start) {
		return nil, fmt.Errorf("maintenance window end %s must be after start %s", end, start)
	}
	ctx := context.Background()
	if _, err := s.db.Stations.GetByID(ctx, stationID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("station %d not found", stationID)
		}
		return nil, err
	}

	var overlapping int
	err := s.db.SQL().QueryRowContext(ctx, maintenanceOverlapQuery,
		stationID, model.MaintClosed, end.UTC(), start.UTC()).Scan(&overlapping)
	if err != nil {
		return nil, fmt.Errorf("overlap check for station %d: %w", stationID, err)
	}
	if overlapping > 0 {
		return nil, fmt.Errorf(
			"station %d already has %d non-closed window(s) intersecting [%s, %s]",
			stationID, overlapping, start.UTC(), end.UTC())
	}

	w, err := s.PlanWindow(stationID, start, end, reason)
	if err != nil {
		return nil, fmt.Errorf("plan window for station %d: %w", stationID, err)
	}
	return w, nil
}

// Extend pushes the end of an existing window to newEnd. Extending is only
// allowed while the window is planned or open and never shrinks it.
func (s *MaintenanceService) Extend(id int64, newEnd time.Time) (*model.MaintenanceWindow, error) {
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
	if w.State == model.MaintClosed {
		return nil, fmt.Errorf("maintenance window %d already closed", id)
	}
	newEnd = newEnd.UTC()
	if !newEnd.After(w.End) {
		return nil, fmt.Errorf(
			"new end %s does not extend current end %s of window %d",
			newEnd, w.End, id)
	}

	// The extended tail must still be conflict-free against other windows,
	// excluding the window being extended itself.
	var overlapping int
	err = s.db.SQL().QueryRowContext(ctx, `
SELECT COUNT(1)
FROM maintenance_windows
WHERE station_id = ? AND id != ? AND state != ? AND start_ts < ? AND end_ts > ?`,
		w.StationID, id, model.MaintClosed, newEnd, w.End).Scan(&overlapping)
	if err != nil {
		return nil, fmt.Errorf("extend overlap check for window %d: %w", id, err)
	}
	if overlapping > 0 {
		return nil, fmt.Errorf(
			"extending window %d to %s would overlap %d other window(s)",
			id, newEnd, overlapping)
	}

	if _, err := s.db.SQL().ExecContext(ctx,
		`UPDATE maintenance_windows SET end_ts = ? WHERE id = ?`, newEnd, id); err != nil {
		return nil, err
	}
	w.End = newEnd
	return &w, nil
}

// OpenWindows lists every non-closed window of a station ordered by start.
func (s *MaintenanceService) OpenWindows(stationID int64) ([]model.MaintenanceWindow, error) {
	rows, err := s.db.SQL().QueryContext(context.Background(), `
SELECT id, station_id, start_ts, end_ts, reason, state, created_at
FROM maintenance_windows WHERE station_id = ? AND state != ? ORDER BY start_ts`,
		stationID, model.MaintClosed)
	if err != nil {
		return nil, fmt.Errorf("list windows for station %d: %w", stationID, err)
	}
	defer rows.Close()

	out := make([]model.MaintenanceWindow, 0)
	for rows.Next() {
		var w model.MaintenanceWindow
		if err := rows.Scan(&w.ID, &w.StationID, &w.Start, &w.End, &w.Reason, &w.State, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
