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

// ChannelService manages per-station data channels with strict validation
// of channel codes and sample rates drawn from the FDSN standard set.
type ChannelService struct {
	db *store.DB
}

func NewChannelService(db *store.DB) *ChannelService {
	return &ChannelService{db: db}
}

// ValidChannelCodes is the closed set of band/instrument codes seiswatch
// accepts: broadband + long-period + strong-motion components.
var ValidChannelCodes = map[string]bool{
	"BHZ": true, "BHN": true, "BHE": true,
	"LHZ": true, "LHN": true, "LHE": true,
	"HHZ": true, "HHN": true, "HHE": true,
	"MHZ": true, "MZN": true, "MZE": true,
}

// ValidSampleRates lists the supported digitizer rates in Hz.
var ValidSampleRates = map[float64]bool{
	1: true, 20: true, 40: true, 50: true, 80: true, 100: true, 200: true,
}

// ValidateChannelCode reports whether code belongs to the accepted set.
func ValidateChannelCode(code string) bool { return ValidChannelCodes[code] }

// ValidateSampleRate reports whether rate matches a supported digitizer
// rate within floating-point tolerance.
func ValidateSampleRate(rate float64) bool {
	for known := range ValidSampleRates {
		if rate == known {
			return true
		}
	}
	return false
}

// AddChannel validates inputs, resolves the owning station by code,
// rejects duplicate codes within the station, then persists the channel.
func (s *ChannelService) AddChannel(stationCode, code string, sampleRate, gain, sensitivity float64, unit string) (*model.Channel, error) {
	if !ValidateChannelCode(code) {
		return nil, fmt.Errorf("invalid channel code %q: allowed BHZ/BHN/BHE/LHZ/LHN/LHE/HHZ/HHN/HHE/MHZ/MZN/MZE", code)
	}
	if !ValidateSampleRate(sampleRate) {
		return nil, fmt.Errorf("unsupported sample rate %g: allowed 1, 20, 40, 50, 80, 100, 200", sampleRate)
	}
	if gain <= 0 {
		return nil, fmt.Errorf("gain %g must be positive", gain)
	}
	if sensitivity <= 0 {
		return nil, fmt.Errorf("sensitivity %g must be positive counts/unit", sensitivity)
	}
	if unit == "" {
		return nil, errors.New("channel unit must not be empty")
	}

	ctx := context.Background()
	station, err := s.db.Stations.GetByCode(ctx, stationCode)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("station %q not found", stationCode)
		}
		return nil, err
	}
	if _, err := s.db.Channels.FindByCode(ctx, station.ID, code); err == nil {
		return nil, fmt.Errorf("channel %q already exists on station %q", code, stationCode)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	ch := &model.Channel{
		StationID:   station.ID,
		Code:        code,
		SampleRate:  sampleRate,
		Gain:        gain,
		Sensitivity: sensitivity,
		Unit:        unit,
		Status:      model.ChannelOpen,
	}
	id, err := s.db.Channels.Create(ctx, ch)
	if err != nil {
		return nil, fmt.Errorf("add channel %q to station %q: %w", code, stationCode, err)
	}
	ch.ID = id
	ch.CreatedAt = time.Now().UTC()
	return ch, nil
}

// Close transitions an open channel to closed; closing twice fails.
func (s *ChannelService) Close(id int64) error {
	ctx := context.Background()
	var status string
	err := s.db.SQL().QueryRowContext(ctx,
		`SELECT status FROM channels WHERE id = ?`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("channel %d not found", id)
	}
	if err != nil {
		return err
	}
	if status != model.ChannelOpen {
		return fmt.Errorf("channel %d is %s, only open channels can be closed", id, status)
	}
	res, err := s.db.SQL().ExecContext(ctx,
		`UPDATE channels SET status = ? WHERE id = ? AND status = ?`,
		model.ChannelClosed, id, model.ChannelOpen)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("channel %d close raced with another update", id)
	}
	return nil
}

// ListOpenByStation returns the still-open channels of one station,
// ordered by code.
func (s *ChannelService) ListOpenByStation(stationID int64) ([]model.Channel, error) {
	ctx := context.Background()
	if _, err := s.db.Stations.GetByID(ctx, stationID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("station %d not found", stationID)
		}
		return nil, err
	}
	rows, err := s.db.SQL().QueryContext(ctx, `
SELECT id, station_id, code, sample_rate, gain, sensitivity, unit, status, created_at
FROM channels WHERE station_id = ? AND status = ? ORDER BY code`, stationID, model.ChannelOpen)
	if err != nil {
		return nil, fmt.Errorf("list open channels for station %d: %w", stationID, err)
	}
	defer rows.Close()

	out := make([]model.Channel, 0, 3)
	for rows.Next() {
		var c model.Channel
		if err := rows.Scan(&c.ID, &c.StationID, &c.Code, &c.SampleRate, &c.Gain,
			&c.Sensitivity, &c.Unit, &c.Status, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Find resolves a single channel by station and code with ErrNotFound
// semantics preserved for callers. Callers must verify the returned
// channel's Status themselves; Find intentionally does not filter so
// admin views can still look up closed channels.
func (s *ChannelService) Find(stationID int64, code string) (*model.Channel, error) {
	return s.db.Channels.FindByCode(context.Background(), stationID, code)
}
