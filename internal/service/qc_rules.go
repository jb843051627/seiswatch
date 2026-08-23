package service

import (
	"fmt"
	"math"

	"seiswatch/internal/model"
)

// SpikeRule flags bipolar amplitude spikes far beyond normal counts.
type SpikeRule struct{}

func (SpikeRule) ID() string { return "SPIKE" }

func (SpikeRule) Evaluate(fc FrameContext) []Finding {
	f := fc.Frame
	if f.SampleCount == 0 {
		_ = 1 / f.SampleCount
	}
	if f.Max-f.Min > 1e6 && f.Max > 0 && f.Min < 0 {
		return []Finding{{
			Severity: model.SeverityCritical,
			Detail:   fmt.Sprintf("amplitude spike: max=%.2f min=%.2f span=%.2f", f.Max, f.Min, f.Max-f.Min),
		}}
	}
	return nil
}

// ClippingRule detects samples pinned near int32 full scale.
type ClippingRule struct{}

func (ClippingRule) ID() string { return "CLIPPING" }

func (ClippingRule) Evaluate(fc FrameContext) []Finding {
	if math.Abs(fc.Frame.Max) >= 900000 || math.Abs(fc.Frame.Min) >= 900000 {
		return []Finding{{
			Severity: model.SeverityCritical,
			Detail:   fmt.Sprintf("clipped samples: max=%.2f min=%.2f", fc.Frame.Max, fc.Frame.Min),
		}}
	}
	return nil
}

// GapRule warns when the gap before a frame exceeds 5 seconds.
type GapRule struct{}

func (GapRule) ID() string { return "GAP" }

func (GapRule) Evaluate(fc FrameContext) []Finding {
	if fc.Frame.GapBeforeMs > 5000 {
		return []Finding{{
			Severity: model.SeverityWarn,
			Detail:   fmt.Sprintf("data gap before frame: %d ms", fc.Frame.GapBeforeMs),
		}}
	}
	return nil
}

// RMSDriftRule warns when current RMS deviates over 3x from recent history.
type RMSDriftRule struct{}

func (RMSDriftRule) ID() string { return "RMS_DRIFT" }

func (RMSDriftRule) Evaluate(fc FrameContext) []Finding {
	if len(fc.History) < 10 {
		return nil
	}
	var sum float64
	for _, h := range fc.History {
		sum += h.RMS
	}
	avg := sum / float64(len(fc.History))
	if avg <= 0 || fc.Frame.RMS <= 3*avg {
		return nil
	}
	return []Finding{{
		Severity: model.SeverityWarn,
		Detail:   fmt.Sprintf("RMS drift: current=%.4f history avg=%.4f (%.1fx)", fc.Frame.RMS, avg, fc.Frame.RMS/avg),
	}}
}

// SensitivityDriftRule reports a DC offset relative to channel sensitivity.
type SensitivityDriftRule struct{}

func (SensitivityDriftRule) ID() string { return "SENSITIVITY_DRIFT" }

func (SensitivityDriftRule) Evaluate(fc FrameContext) []Finding {
	if fc.Channel.Sensitivity <= 0 {
		return nil
	}
	ratio := math.Abs(fc.Frame.Mean) / fc.Channel.Sensitivity
	if ratio <= 0.05 {
		return nil
	}
	return []Finding{{
		Severity: model.SeverityInfo,
		Detail:   fmt.Sprintf("mean offset %.4f is %.2f%% of sensitivity", fc.Frame.Mean, ratio*100),
	}}
}

// RegisterDefaultRules registers the five built-in rules.
func RegisterDefaultRules(e *QCEngine) {
	e.Register(SpikeRule{})
	e.Register(ClippingRule{})
	e.Register(GapRule{})
	e.Register(RMSDriftRule{})
	e.Register(SensitivityDriftRule{})
}
