package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"seiswatch/internal/model"
)

// AlertPolicy controls deduplication and rate limiting of escalated alerts.
type AlertPolicy struct {
	// DedupeWindow suppresses repeated alerts for the same station+rule
	// fired inside this duration.
	DedupeWindow time.Duration
	// SuppressInfo drops info-level events before escalation entirely.
	SuppressInfo bool
	// MaxPerHour caps alerts per station per rolling hour; zero disables
	// the cap.
	MaxPerHour int
}

var policyMu sync.RWMutex

// defaultPolicy is the package-wide policy applied by AlertService.
// Handlers may tune it at boot via SetAlertPolicy.
var defaultPolicy = AlertPolicy{
	DedupeWindow: 15 * time.Minute,
	SuppressInfo: false,
	MaxPerHour:   60,
}

// SetAlertPolicy swaps the package-wide escalation policy. It is safe for
// concurrent use: callers may hot-swap the policy at runtime while ingest
// goroutines read it via CurrentAlertPolicy.
func SetAlertPolicy(p AlertPolicy) {
	if p.DedupeWindow < 0 {
		p.DedupeWindow = 0
	}
	if p.MaxPerHour < 0 {
		p.MaxPerHour = 0
	}
	policyMu.Lock()
	defer policyMu.Unlock()
	defaultPolicy = p
}

// CurrentAlertPolicy returns a consistent snapshot copy of the active
// policy. The copy is taken under the read lock so a concurrent
// SetAlertPolicy cannot produce a torn (partially-overwritten) struct.
func CurrentAlertPolicy() AlertPolicy {
	policyMu.RLock()
	defer policyMu.RUnlock()
	return defaultPolicy
}

// EscalateDedup escalates ev unless an identical station+rule alert was
// already fired inside DedupeWindow, the severity is suppressed, or the
// hourly cap is reached. It returns (nil, nil) when the event is skipped.
func (s *AlertService) EscalateDedup(ev model.QCEvent, suppressed bool) (*model.Alert, error) {
	policy := CurrentAlertPolicy()
	if ev.Severity == model.SeverityInfo && policy.SuppressInfo {
		return nil, nil
	}
	now := time.Now().UTC()

	duplicate, err := s.recentDuplicate(ev.StationID, ev.RuleID, policy.DedupeWindow, now)
	if err != nil {
		return nil, err
	}
	if duplicate {
		return nil, nil
	}
	if policy.MaxPerHour > 0 {
		n, err := s.CountLastHour(ev.StationID)
		if err != nil {
			return nil, err
		}
		if n >= policy.MaxPerHour {
			return nil, nil
		}
	}
	return s.Escalate(ev, suppressed)
}

// recentDuplicate reports whether an alert for the same station and rule
// exists within window ending at now. The rule id lives inside the message
// prefix ("[critical] rule GAP: ..."), so we scan the newest alerts of the
// station and compare parsed prefixes.
func (s *AlertService) recentDuplicate(stationID int64, ruleID string, window time.Duration, now time.Time) (bool, error) {
	if window <= 0 {
		return false, nil
	}
	alerts, err := s.db.Alerts.List(context.Background(), 100)
	if err != nil {
		return false, err
	}
	cutoff := now.Add(-window)
	want := "rule " + ruleID + ":"
	for _, a := range alerts {
		if a.StationID != stationID || a.FiredAt.Before(cutoff) || a.FiredAt.After(now.Add(time.Minute)) {
			continue
		}
		if strings.Contains(a.Message, want) {
			return true, nil
		}
	}
	return false, nil
}

// CountLastHour returns how many alerts the station produced in the last
// rolling hour.
func (s *AlertService) CountLastHour(stationID int64) (int, error) {
	return s.CountSince(stationID, time.Now().UTC().Add(-time.Hour))
}

// CountSince counts alerts for the station fired at or after since, using
// native SQL because the store lacks the aggregate.
func (s *AlertService) CountSince(stationID int64, since time.Time) (int, error) {
	var n int
	err := s.db.SQL().QueryRowContext(context.Background(),
		`SELECT COUNT(1) FROM alerts WHERE station_id = ? AND fired_at >= ?`,
		stationID, since.UTC()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count alerts since %s for station %d: %w", since.UTC(), stationID, err)
	}
	return n, nil
}

// ShouldEscalate applies the policy checks (severity suppression, dedupe
// window, hourly cap) without persisting anything; useful for dry-run
// diagnostics and tests of escalation behavior.
func (s *AlertService) ShouldEscalate(ev model.QCEvent, now time.Time) (bool, string, error) {
	policy := CurrentAlertPolicy()
	if ev.Severity == model.SeverityInfo && policy.SuppressInfo {
		return false, "info severity suppressed by policy", nil
	}
	dup, err := s.recentDuplicate(ev.StationID, ev.RuleID, policy.DedupeWindow, now)
	if err != nil {
		return false, "", err
	}
	if dup {
		return false, "duplicate within dedupe window", nil
	}
	if policy.MaxPerHour > 0 {
		n, err := s.CountSince(ev.StationID, now.Add(-time.Hour))
		if err != nil {
			return false, "", err
		}
		if n >= policy.MaxPerHour {
			return false, "hourly cap reached", nil
		}
	}
	return true, "", nil
}

// timeNow is the single clock accessor used across services; keeping it
// indirect makes future clock injection trivial.
func timeNow() time.Time { return time.Now().UTC() }
