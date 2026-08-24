package model

import "time"

// MaintState is the state machine state of a maintenance window.
type MaintState string

const (
	MaintPlanned MaintState = "planned"
	MaintOpen    MaintState = "open"
	MaintClosed  MaintState = "closed"
)

// MaintActiveStates lists the window states during which alerts are
// suppressed for the station.
var MaintActiveStates = []MaintState{MaintPlanned, MaintOpen}

// MaintenanceWindow is a planned period during which alerts for a station
// are suppressed because operators intentionally work on the hardware.
type MaintenanceWindow struct {
	ID        int64
	StationID int64
	Start     time.Time
	End       time.Time
	Reason    string
	State     MaintState
	CreatedAt time.Time
}

// Covers reports whether at time t falls inside the window regardless of
// its current state.
func (w MaintenanceWindow) Covers(t time.Time) bool {
	return !t.Before(w.Start) && !t.After(w.End)
}

// ActiveAt reports whether the window suppresses alerts at time t; closed
// windows never suppress.
func (w MaintenanceWindow) ActiveAt(t time.Time) bool {
	if w.State == MaintClosed {
		return false
	}
	return w.Covers(t)
}

// Overlaps reports whether the window intersects [start, end).
func (w MaintenanceWindow) Overlaps(start, end time.Time) bool {
	return w.Start.Before(end) && w.End.After(start)
}
