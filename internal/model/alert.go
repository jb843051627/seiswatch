package model

import "time"

// Alert is an escalation of a critical QC event that requires operator action.
type Alert struct {
	ID         int64
	QCEventID  int64
	StationID  int64
	Message    string
	FiredAt    time.Time
	Suppressed bool
}

// MessagePrefix returns the canonical "[severity] rule <id>:" prefix the
// services use to identify which rule produced an alert.
func (a Alert) MessagePrefix() string {
	idx := indexByte(a.Message, ':')
	if idx <= 0 {
		return a.Message
	}
	return a.Message[:idx+1]
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// AlertMessage renders the canonical alert message for a QC finding.
func AlertMessage(sev Severity, ruleID, detail string) string {
	return "[" + string(sev) + "] rule " + ruleID + ": " + detail
}
