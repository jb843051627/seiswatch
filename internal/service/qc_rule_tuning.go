package service

import (
	"fmt"
	"math"

	"seiswatch/internal/model"
)

// TunedRule wraps an existing Rule with per-station threshold overrides.
// When Prof is nil the inner rule runs unchanged. When Prof is set:
//   - SPIKE / CLIPPING / GAP findings are re-checked against the profile
//     thresholds and dropped when below them;
//   - warn-level findings are dropped entirely when SuppressWarn is on;
//   - surviving findings get their detail prefixed with "[tuned]" so
//     operators can tell tuned verdicts from default ones.
type TunedRule struct {
	Inner        Rule
	Prof         *ThresholdProfile
	SuppressWarn bool
}

// ID delegates to the wrapped rule so events keep the original RuleID.
func (t TunedRule) ID() string { return t.Inner.ID() }

// Evaluate applies the tuning policy to the inner rule's findings.
func (t TunedRule) Evaluate(fc FrameContext) []Finding {
	findings := t.Inner.Evaluate(fc)
	if len(findings) == 0 {
		return nil
	}
	out := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if t.Prof != nil && t.belowTunedThreshold(fc, f) {
			continue
		}
		if f.Severity == model.SeverityWarn && t.SuppressWarn {
			continue
		}
		if t.Prof != nil {
			f.Detail = "[tuned] " + f.Detail
		}
		out = append(out, f)
	}
	return out
}

// belowTunedThreshold reports whether the finding is a SPIKE, CLIPPING or
// GAP verdict whose measured magnitude stays under the profile threshold.
func (t TunedRule) belowTunedThreshold(fc FrameContext, f Finding) bool {
	switch t.Inner.ID() {
	case "SPIKE":
		return fc.Frame.AmplitudeSpan() < t.Prof.SpikeAmplitude
	case "CLIPPING":
		level := math.Max(math.Abs(fc.Frame.Max), math.Abs(fc.Frame.Min))
		return level < t.Prof.ClippingLevel
	case "GAP":
		return fc.Frame.GapBeforeMs <= t.Prof.GapMs
	case "RMS_DRIFT":
		return t.rmsDriftBelowThreshold(fc)
	default:
		return false
	}
}

// rmsDriftBelowThreshold recomputes the current/history RMS ratio from the
// frame context and compares it with the tuned ratio.
func (t TunedRule) rmsDriftBelowThreshold(fc FrameContext) bool {
	if len(fc.History) == 0 || fc.Frame.RMS <= 0 {
		return true
	}
	var sum float64
	for _, h := range fc.History {
		sum += h.RMS
	}
	avg := sum / float64(len(fc.History))
	if avg <= 0 {
		return true
	}
	return fc.Frame.RMS/avg < t.Prof.RMSRatio
}

// ApplyProfile wraps every registered rule of the engine in a TunedRule
// carrying p. Passing a nil pointer resets the engine to untuned rules by
// unwrapping any existing TunedRule layers first.
func (e *QCEngine) ApplyProfile(p *ThresholdProfile) {
	tuned := make([]Rule, 0, len(e.rules))
	for _, r := range e.rules {
		inner := unwrapTuned(r)
		if p == nil {
			tuned = append(tuned, inner)
			continue
		}
		tuned = append(tuned, TunedRule{Inner: inner, Prof: p})
	}
	e.rules = tuned
}

// SetEngineSuppressWarn toggles warn suppression on every TunedRule layer,
// wrapping plain rules when necessary.
func (e *QCEngine) SetEngineSuppressWarn(suppress bool) {
	for i, r := range e.rules {
		if t, ok := r.(TunedRule); ok {
			t.SuppressWarn = suppress
			e.rules[i] = t
		}
	}
}

// unwrapTuned peels TunedRule wrappers so repeated ApplyProfile calls do
// not stack layers indefinitely.
func unwrapTuned(r Rule) Rule {
	for {
		t, ok := r.(TunedRule)
		if !ok {
			return r
		}
		r = t.Inner
	}
}

// DescribeTuning renders a human-readable summary of the active profile,
// useful in operator-facing diagnostics output.
func DescribeTuning(p *ThresholdProfile) string {
	if p == nil {
		return "default thresholds"
	}
	return fmt.Sprintf("station %d tuned: spike>=%.0f clip>=%.0f gap>%dms rms-ratio>=%.2f sens-ratio>=%.3f",
		p.StationID, p.SpikeAmplitude, p.ClippingLevel, p.GapMs, p.RMSRatio, p.SensitivityRatio)
}
