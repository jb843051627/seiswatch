package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"seiswatch/internal/model"
)

// StationStore persists network stations.
type StationStore struct {
	db *sql.DB
}

func (s *StationStore) Create(ctx context.Context, st *model.Station) (int64, error) {
	if st.InstalledAt.IsZero() {
		st.InstalledAt = time.Now().UTC()
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO stations (code, name, region, latitude, longitude, status, installed_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		st.Code, st.Name, st.Region, st.Latitude, st.Longitude, st.Status, st.InstalledAt.UTC(), time.Now().UTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func scanStation(row interface{ Scan(...any) error }) (*model.Station, error) {
	var st model.Station
	err := row.Scan(&st.ID, &st.Code, &st.Name, &st.Region, &st.Latitude, &st.Longitude,
		&st.Status, &st.InstalledAt, &st.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

const stationCols = `id, code, name, region, latitude, longitude, status, installed_at, created_at`

func (s *StationStore) GetByID(ctx context.Context, id int64) (*model.Station, error) {
	return scanStation(s.db.QueryRowContext(ctx,
		`SELECT `+stationCols+` FROM stations WHERE id = ?`, id))
}

func (s *StationStore) GetByCode(ctx context.Context, code string) (*model.Station, error) {
	return scanStation(s.db.QueryRowContext(ctx,
		`SELECT `+stationCols+` FROM stations WHERE code = ?`, code))
}

func (s *StationStore) List(ctx context.Context) ([]model.Station, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+stationCols+` FROM stations ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Station
	for rows.Next() {
		var st model.Station
		if err := rows.Scan(&st.ID, &st.Code, &st.Name, &st.Region, &st.Latitude, &st.Longitude,
			&st.Status, &st.InstalledAt, &st.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *StationStore) UpdateStatus(ctx context.Context, id int64, status model.StationStatus) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE stations SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
