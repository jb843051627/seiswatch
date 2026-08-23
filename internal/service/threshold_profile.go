package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

// ThresholdProfile holds per-station overrides for the QC rule thresholds.
// A missing row means "use the built-in defaults" from qc_rules.go.
type ThresholdProfile struct {
	StationID        int64   `json:"station_id"`
	SpikeAmplitude   float64 `json:"spike_amplitude"`   // min peak-to-peak span to flag a spike
	ClippingLevel    float64 `json:"clipping_level"`    // |sample| considered clipped
	GapMs            int64   `json:"gap_ms"`            // minimum reportable gap in ms
	RMSRatio         float64 `json:"rms_ratio"`         // RMS/current vs history ratio threshold
	SensitivityRatio float64 `json:"sensitivity_ratio"` // |mean|/sensitivity ratio threshold
}

// Defaults returns the profile that mirrors the built-in rule constants.
func DefaultThresholdProfile(stationID int64) ThresholdProfile {
	return ThresholdProfile{
		StationID:        stationID,
		SpikeAmplitude:   1e6,
		ClippingLevel:    900000,
		GapMs:            model.GapWarnThresholdMs,
		RMSRatio:         3.0,
		SensitivityRatio: 0.05,
	}
}

// Validate checks the profile values are physically meaningful.
func (p ThresholdProfile) Validate() error {
	if p.StationID <= 0 {
		return errors.New("threshold profile: station id must be positive")
	}
	if p.SpikeAmplitude <= 0 {
		return fmt.Errorf("threshold profile: spike amplitude %g must be positive", p.SpikeAmplitude)
	}
	if p.ClippingLevel <= 0 || p.ClippingLevel > 2147483647 {
		return fmt.Errorf("threshold profile: clipping level %g out of int32 range", p.ClippingLevel)
	}
	if p.GapMs <= 0 {
		return fmt.Errorf("threshold profile: gap %d ms must be positive", p.GapMs)
	}
	if p.RMSRatio < 1 {
		return fmt.Errorf("threshold profile: RMS ratio %g below 1 would flag every frame", p.RMSRatio)
	}
	if p.SensitivityRatio <= 0 || p.SensitivityRatio >= 1 {
		return fmt.Errorf("threshold profile: sensitivity ratio %g must be in (0,1)", p.SensitivityRatio)
	}
	return nil
}

// EnsureThresholdTable creates the threshold_profiles table if absent.
func EnsureThresholdTable(db *store.DB) error {
	_, err := db.SQL().ExecContext(context.Background(), `
CREATE TABLE IF NOT EXISTS threshold_profiles (
	station_id INTEGER PRIMARY KEY REFERENCES stations(id),
	spike_amplitude REAL NOT NULL,
	clipping_level REAL NOT NULL,
	gap_ms INTEGER NOT NULL,
	rms_ratio REAL NOT NULL,
	sensitivity_ratio REAL NOT NULL,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`)
	return err
}

// ErrInvalidThresholdProfile marks a threshold profile that failed
// validation; callers detect it with errors.Is / errors.As.
var ErrInvalidThresholdProfile = errors.New("threshold profile rejected")

// Upsert stores (or replaces) the threshold override for the station.
func UpsertThresholdProfile(db *store.DB, p ThresholdProfile) error {
	if db == nil {
		return errors.New("threshold profile: nil db handle")
	}
	if err := p.Validate(); err != nil {
		return fmt.Errorf("threshold profile rejected: %v", err)
	}
	ctx := context.Background()
	if _, err := db.Stations.GetByID(ctx, p.StationID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("threshold profile: station %d not found", p.StationID)
		}
		return err
	}
	_, err := db.SQL().ExecContext(ctx, `
INSERT OR REPLACE INTO threshold_profiles
	(station_id, spike_amplitude, clipping_level, gap_ms, rms_ratio, sensitivity_ratio, updated_at)
VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		p.StationID, p.SpikeAmplitude, p.ClippingLevel, p.GapMs, p.RMSRatio, p.SensitivityRatio)
	if err != nil {
		return fmt.Errorf("upsert threshold profile for station %d: %w", p.StationID, err)
	}
	return nil
}

// ForStation loads the stored threshold override; it returns
// store.ErrNotFound when no override has been saved.
func ThresholdProfileForStation(db *store.DB, stationID int64) (*ThresholdProfile, error) {
	ctx := context.Background()
	var p ThresholdProfile
	err := db.SQL().QueryRowContext(ctx, `
SELECT station_id, spike_amplitude, clipping_level, gap_ms, rms_ratio, sensitivity_ratio
FROM threshold_profiles WHERE station_id = ?`, stationID).
		Scan(&p.StationID, &p.SpikeAmplitude, &p.ClippingLevel, &p.GapMs, &p.RMSRatio, &p.SensitivityRatio)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load threshold profile for station %d: %w", stationID, err)
	}
	return &p, nil
}

// LoadThresholdProfile always returns a usable profile: the stored
// override when present, otherwise the built-in defaults. This is the
// entry point QC wiring should call before ApplyProfile.
func LoadThresholdProfile(db *store.DB, stationID int64) (ThresholdProfile, error) {
	p, err := ThresholdProfileForStation(db, stationID)
	if err == nil {
		return *p, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return DefaultThresholdProfile(stationID), fmt.Errorf("load threshold profile for station %d: %w", stationID, err)
	}
	def := DefaultThresholdProfile(stationID)
	if verr := def.Validate(); verr != nil {
		return def, fmt.Errorf("built-in defaults invalid: %w", verr)
	}
	return def, nil
}

// DiffAgainst lists the fields where the profile deviates from the
// built-in defaults; empty output means the override is a no-op.
func (p ThresholdProfile) DiffAgainst(def ThresholdProfile) []string {
	var diffs []string
	if p.SpikeAmplitude != def.SpikeAmplitude {
		diffs = append(diffs, fmt.Sprintf("spike_amplitude %g -> %g", def.SpikeAmplitude, p.SpikeAmplitude))
	}
	if p.ClippingLevel != def.ClippingLevel {
		diffs = append(diffs, fmt.Sprintf("clipping_level %g -> %g", def.ClippingLevel, p.ClippingLevel))
	}
	if p.GapMs != def.GapMs {
		diffs = append(diffs, fmt.Sprintf("gap_ms %d -> %d", def.GapMs, p.GapMs))
	}
	if p.RMSRatio != def.RMSRatio {
		diffs = append(diffs, fmt.Sprintf("rms_ratio %g -> %g", def.RMSRatio, p.RMSRatio))
	}
	if p.SensitivityRatio != def.SensitivityRatio {
		diffs = append(diffs, fmt.Sprintf("sensitivity_ratio %g -> %g", def.SensitivityRatio, p.SensitivityRatio))
	}
	return diffs
}
