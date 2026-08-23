package store

import (
	"context"
	"database/sql"
	"time"
)

// MaintenanceStore reads alert-suppression windows.
type MaintenanceStore struct {
	db *sql.DB
}

func (s *MaintenanceStore) ActiveAt(ctx context.Context, stationID int64, at time.Time) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(1) FROM maintenance_windows
WHERE station_id = ? AND state IN ('planned','open') AND start_ts <= ? AND end_ts >= ?`,
		stationID, at.UTC(), at.UTC()).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
