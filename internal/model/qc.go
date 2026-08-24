package model

import "time"

// Severity classifies QC events.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarn     Severity = "warn"
	SeverityCritical Severity = "critical"
)

// ValidSeverity reports whether s is one of the known severity levels.
func ValidSeverity(s Severity) bool {
	switch s {
	case SeverityInfo, SeverityWarn, SeverityCritical:
		return true
	}
	return false
}

// QCStatus is the review workflow state of a QC event.
type QCStatus string

const (
	QCOpen     QCStatus = "open"
	QCAcked    QCStatus = "ack"
	QCResolved QCStatus = "resolved"
)

// ValidQCStatus reports whether s is a known review status.
func ValidQCStatus(s QCStatus) bool {
	switch s {
	case QCOpen, QCAcked, QCResolved:
		return true
	}
	return false
}

// QCEvent records one quality-control finding raised by the rule engine.
type QCEvent struct {
	ID         int64
	StationID  int64
	ChannelID  int64
	FrameID    int64
	RuleID     string // e.g. "SPIKE", "CLIPPING", "GAP", "RMS_DRIFT", "SENSITIVITY"
	Severity   Severity
	Detail     string
	Status     QCStatus
	DetectedAt time.Time
	ResolvedAt *time.Time
}

// Open reports whether the event still needs operator attention.
func (e QCEvent) Open() bool { return e.Status == QCOpen }

// ResolutionLatency returns how long the event stayed open; the zero
// duration is returned while the event is not yet resolved.
func (e QCEvent) ResolutionLatency() time.Duration {
	if e.ResolvedAt == nil || !e.ResolvedAt.After(e.DetectedAt) {
		return 0
	}
	return e.ResolvedAt.Sub(e.DetectedAt)
}
