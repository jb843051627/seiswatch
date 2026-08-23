// Package model defines the core domain entities of seiswatch,
// a seismic network station data quality-control service.
package model

import "time"

// StationStatus enumerates the lifecycle states of a network station.
type StationStatus string

const (
	StationActive         StationStatus = "active"
	StationInactive       StationStatus = "inactive"
	StationMaintenance    StationStatus = "maintenance"
	StationDecommissioned StationStatus = "decommissioned"
)

// ValidStationStatus reports whether s is a known station status.
func ValidStationStatus(s StationStatus) bool {
	switch s {
	case StationActive, StationInactive, StationMaintenance, StationDecommissioned:
		return true
	}
	return false
}

// Station is a physical seismic recording site in the network.
type Station struct {
	ID          int64
	Code        string // short unique code, e.g. "GD03"
	Name        string
	Region      string
	Latitude    float64
	Longitude   float64
	Status      StationStatus
	InstalledAt time.Time
	CreatedAt   time.Time
}

// Operational reports whether the station is expected to produce telemetry.
func (s Station) Operational() bool {
	return s.Status == StationActive || s.Status == StationMaintenance
}
