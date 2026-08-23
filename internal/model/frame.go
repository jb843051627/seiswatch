package model

import "time"

// GapWarnThresholdMs is the minimum gap length in milliseconds that the
// built-in GAP rule considers worth reporting.
const GapWarnThresholdMs int64 = 5000

// DataFrame holds summary statistics for one decoded telemetry frame.
// Raw samples are not persisted; only aggregates used by QC rules.
type DataFrame struct {
	ID          int64
	ChannelID   int64
	StartTime   time.Time
	EndTime     time.Time
	SampleCount int
	Min         float64
	Max         float64
	Mean        float64
	RMS         float64
	GapBeforeMs int64 // gap between previous frame end and this frame start
	ReceivedAt  time.Time
}

// AmplitudeSpan returns the peak-to-peak amplitude of the frame.
func (f DataFrame) AmplitudeSpan() float64 { return f.Max - f.Min }

// Duration returns the wall-clock span covered by the frame; zero when
// the timestamps are inverted or missing.
func (f DataFrame) Duration() time.Duration {
	if !f.EndTime.After(f.StartTime) {
		return 0
	}
	return f.EndTime.Sub(f.StartTime)
}

// Gapped reports whether the frame follows a reportable data gap.
func (f DataFrame) Gapped() bool { return f.GapBeforeMs > GapWarnThresholdMs }
