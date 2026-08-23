package model

import "time"

// CalibrationKind distinguishes calibration job types.
type CalibrationKind string

const (
	CalibMassCenter CalibrationKind = "mass-center"
	CalibGainCheck  CalibrationKind = "gain-check"
)

// ValidCalibrationKind reports whether k is a known calibration kind.
func ValidCalibrationKind(k CalibrationKind) bool {
	switch k {
	case CalibMassCenter, CalibGainCheck:
		return true
	}
	return false
}

// CalibState is the state machine state of a calibration job.
type CalibState string

const (
	CalibPending   CalibState = "pending"
	CalibRunning   CalibState = "running"
	CalibSucceeded CalibState = "succeeded"
	CalibFailed    CalibState = "failed"
	CalibExpired   CalibState = "expired"
)

// CalibTerminal reports whether the state ends the job lifecycle.
func CalibTerminal(s CalibState) bool {
	switch s {
	case CalibSucceeded, CalibFailed, CalibExpired:
		return true
	}
	return false
}

// CalibrationJob is a scheduled sensor calibration task for a station.
type CalibrationJob struct {
	ID            int64
	StationID     int64
	Kind          CalibrationKind
	ScheduledAt   time.Time
	WindowMinutes int
	State         CalibState
	ResultMetrics map[string]float64 // serialized as JSON in storage
	StartedAt     *time.Time
	FinishedAt    *time.Time
	CreatedAt     time.Time
}

// Deadline returns the latest moment the job may still transition out of
// the pending state.
func (j CalibrationJob) Deadline() time.Time {
	return j.ScheduledAt.Add(time.Duration(j.WindowMinutes) * time.Minute)
}

// Overdue reports whether a pending job has blown its execution window.
func (j CalibrationJob) Overdue(now time.Time) bool {
	return j.State == CalibPending && now.After(j.Deadline())
}

// Elapsed returns the wall-clock time spent running; zero when the job
// never started or is still running.
func (j CalibrationJob) Elapsed() time.Duration {
	if j.StartedAt == nil || j.FinishedAt == nil {
		return 0
	}
	return j.FinishedAt.Sub(*j.StartedAt)
}
