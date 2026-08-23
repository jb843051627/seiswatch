package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"

	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

// StationService implements the station lifecycle operations on top of the
// station store plus raw SQL for queries the store does not expose.
type StationService struct {
	db *store.DB
}

func NewStationService(db *store.DB) *StationService {
	return &StationService{db: db}
}

// stationCodePattern enforces 4-8 uppercase alphanumerics.
var stationCodePattern = regexp.MustCompile(`^[A-Z0-9]{4,8}$`)

// minLat/maxLat and lon bounds follow ordinary WGS-84 ranges.
const (
	minLatitude  = -90.0
	maxLatitude  = 90.0
	minLongitude = -180.0
	maxLongitude = 180.0
)

// ValidateStationCode reports whether code is a syntactically valid
// station code (4-8 uppercase letters or digits).
func ValidateStationCode(code string) bool { return stationCodePattern.MatchString(code) }

// Register creates a new active station after validating every field and
// rejecting duplicate codes.
func (s *StationService) Register(code, name, region string, lat, lon float64) (*model.Station, error) {
	if !ValidateStationCode(code) {
		return nil, fmt.Errorf("invalid station code %q: want 4-8 uppercase letters/digits", code)
	}
	if len(name) < 2 || len(name) > 120 {
		return nil, fmt.Errorf("station name length %d out of range [2,120]", len(name))
	}
	if region == "" {
		return nil, errors.New("station region must not be empty")
	}
	if lat < minLatitude || lat > maxLatitude || math.IsNaN(lat) || math.IsInf(lat, 0) {
		return nil, fmt.Errorf("latitude %g outside [%g, %g]", lat, minLatitude, maxLatitude)
	}
	if lon < minLongitude || lon > maxLongitude || math.IsNaN(lon) || math.IsInf(lon, 0) {
		return nil, fmt.Errorf("longitude %g outside [%g, %g]", lon, minLongitude, maxLongitude)
	}

	ctx := context.Background()
	if _, err := s.db.Stations.GetByCode(ctx, code); err == nil {
		return nil, fmt.Errorf("station code %q already registered", code)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	st := &model.Station{
		Code:        code,
		Name:        name,
		Region:      region,
		Latitude:    lat,
		Longitude:   lon,
		Status:      model.StationActive,
		InstalledAt: time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
	}
	id, err := s.db.Stations.Create(ctx, st)
	if err != nil {
		return nil, fmt.Errorf("register station %q: %w", code, err)
	}
	st.ID = id
	return st, nil
}

// validStatusTransitions lists the allowed lifecycle edges. Decommissioning
// is handled separately because any live state may transition there.
var validStatusTransitions = map[model.StationStatus][]model.StationStatus{
	model.StationActive:      {model.StationMaintenance, model.StationInactive, model.StationDecommissioned},
	model.StationInactive:    {model.StationActive, model.StationDecommissioned},
	model.StationMaintenance: {model.StationActive, model.StationDecommissioned},
}

// SetStatus moves a station from state from to state to after verifying
// both the current stored state and the transition legality.
func (s *StationService) SetStatus(id int64, from, to model.StationStatus) (*model.Station, error) {
	if !model.ValidStationStatus(from) {
		return nil, fmt.Errorf("unknown source status %q", from)
	}
	if !model.ValidStationStatus(to) {
		return nil, fmt.Errorf("unknown target status %q", to)
	}
	ctx := context.Background()
	st, err := s.db.Stations.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("station %d not found", id)
		}
		return nil, err
	}
	if st.Status != from {
		return nil, fmt.Errorf("station %d is %s, expected %s", id, st.Status, from)
	}
	if !allowedTransition(from, to) {
		return nil, fmt.Errorf("illegal status transition %s -> %s", from, to)
	}
	if err := s.db.Stations.UpdateStatus(ctx, id, to); err != nil {
		return nil, err
	}
	st.Status = to
	return st, nil
}

// allowedTransition encodes: active<->maintenance, active<->inactive,
// and anything live -> decommissioned; decommissioned is final.
func allowedTransition(from, to model.StationStatus) bool {
	if to == model.StationDecommissioned && from != model.StationDecommissioned {
		return true
	}
	for _, next := range validStatusTransitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// ListByRegion returns all stations whose region matches exactly,
// ordered by code for stable presentation.
func (s *StationService) ListByRegion(region string) ([]model.Station, error) {
	rows, err := s.db.SQL().QueryContext(context.Background(), `
SELECT `+"id, code, name, region, latitude, longitude, status, installed_at, created_at"+`
FROM stations WHERE region = ? ORDER BY code`, region)
	if err != nil {
		return nil, fmt.Errorf("list stations in region %q: %w", region, err)
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

// Decommission retires a station permanently, refusing while open QC
// events remain unhandled.
func (s *StationService) Decommission(id int64) error {
	ctx := context.Background()
	if _, err := s.db.Stations.GetByID(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("station %d not found", id)
		}
		return err
	}
	var open int
	if err := s.db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(1) FROM qc_events WHERE station_id = ? AND status = ?`,
		id, model.QCOpen).Scan(&open); err != nil {
		return fmt.Errorf("count open QC events for station %d: %w", id, err)
	}
	if open > 0 {
		return fmt.Errorf("station %d still has %d open QC event(s), resolve them first", id, open)
	}
	return s.db.Stations.UpdateStatus(ctx, id, model.StationDecommissioned)
}

// GetByCode exposes a typed lookup so handlers need not touch the store.
func (s *StationService) GetByCode(code string) (*model.Station, error) {
	st, err := s.db.Stations.GetByCode(context.Background(), code)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return st, err
}
