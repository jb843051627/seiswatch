package ingest

import (
	"fmt"
	"time"
)

// Timing continuity checks between consecutive frames. Seismic data
// analysis assumes a gapless, evenly sampled timeline; VerifyTiming
// quantifies how far a frame's actual start drifted from where the
// previous frame ended.
type TimingCheck struct {
	DriftMs      int64         // Actual - Expected in milliseconds (negative = early)
	ToleranceMs  int64         // allowed drift before it counts as a clock jump
	Expected     time.Time     // where the frame should have started (= prevEnd)
	Actual       time.Time     // where the frame actually started
	SamplePeriod time.Duration // one over sampleRate; 0 when sampleRate <= 0
}

// VerifyTiming compares the start of a new frame against the end of the
// previous one. Expected is prevEnd by definition; DriftMs is the
// signed difference Actual minus Expected in milliseconds. toleranceMs
// is stored on the result so IsClockJump can judge it later. A
// non-positive sampleRate leaves SamplePeriod at zero, disabling the
// sub-period comparison helpers.
func VerifyTiming(prevEnd, actualStart time.Time, sampleRate float64, toleranceMs int64) TimingCheck {
	check := TimingCheck{
		DriftMs:     actualStart.Sub(prevEnd).Milliseconds(),
		ToleranceMs: toleranceMs,
		Expected:    prevEnd.UTC(),
		Actual:      actualStart.UTC(),
	}
	if sampleRate > 0 {
		check.SamplePeriod = time.Duration(float64(time.Second) / sampleRate)
	}
	return check
}

// IsClockJump reports whether the drift exceeds the configured
// tolerance in either direction. Such jumps mean the digitizer was
// restarted, resynced or its clock was stepped, and downstream gap
// statistics should treat the boundary as unreliable rather than as a
// data gap.
func IsClockJump(check TimingCheck) bool {
	drift := absI64(check.DriftMs)
	tol := absI64(check.ToleranceMs)
	return drift > tol
}

// WithinSamplePeriod reports whether the absolute drift stays below a
// single sampling period. For high-rate channels this is stricter than
// any millisecond tolerance and is the right test when frames must be
// concatenated without losing even one sample slot.
func (c TimingCheck) WithinSamplePeriod() bool {
	if c.SamplePeriod <= 0 {
		return false
	}
	drift := time.Duration(absI64(c.DriftMs)) * time.Millisecond
	return drift < c.SamplePeriod
}

// Gap reports the positive part of the drift: how many milliseconds of
// signal are missing before this frame starts. Early frames yield 0;
// overlaps are not gaps.
func (c TimingCheck) Gap() int64 {
	if c.DriftMs > 0 {
		return c.DriftMs
	}
	return 0
}

// Overlap reports the positive part of an early arrival: how many
// milliseconds this frame starts before the previous one ended.
// Overlapping data means duplicated samples, which downstream
// concatenation must deduplicate instead of stitching blindly.
func (c TimingCheck) Overlap() int64 {
	if c.DriftMs < 0 {
		return -c.DriftMs
	}
	return 0
}

// Continuous reports whether two consecutive frames join without a gap,
// an overlap or a clock jump. Only then may callers treat the sample
// streams as one uninterrupted timeline.
func (c TimingCheck) Continuous() bool {
	return c.DriftMs == 0 || (!IsClockJump(c) && c.WithinSamplePeriod())
}

// String renders a check for logs and QC event details, e.g.
// "drift=250ms tolerance=100ms expected=... actual=... (clock-jump)".
func (c TimingCheck) String() string {
	verdict := "continuous"
	switch {
	case IsClockJump(c):
		verdict = "clock-jump"
	case c.Gap() > 0:
		verdict = "gap"
	case c.Overlap() > 0:
		verdict = "overlap"
	}
	return fmt.Sprintf("drift=%dms tolerance=%dms expected=%s actual=%s (%s)",
		c.DriftMs, c.ToleranceMs,
		c.Expected.UTC().Format(time.RFC3339Nano),
		c.Actual.UTC().Format(time.RFC3339Nano), verdict)
}
