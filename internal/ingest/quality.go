package ingest

// Sample-level quality inspection. ScanSamples walks a decoded sample
// block and reports anomalies that per-frame aggregates cannot express:
// stuck sensors, saturating inputs and synthetic-looking ramps.

// SampleIssue describes one anomaly found in a sample block.
type SampleIssue struct {
	Index int    // position of the affected run inside the block
	Kind  string // STUCK, SATURATED or RAMP
	Value int32  // first value of the offending run
}

// Kinds of sample issues reported by ScanSamples.
const (
	IssueStuck     = "STUCK"     // identical consecutive values
	IssueSaturated = "SATURATED" // |value| beyond the 2^30 guard band
	IssueRamp      = "RAMP"      // long strictly monotonic run
)

const (
	// maxReportedIssues caps the returned list so a pathological frame
	// cannot produce millions of entries.
	maxReportedIssues = 100

	// stuckRunLength is how many identical samples make a "stuck" run.
	stuckRunLength = 64

	// saturationLimit: real seismic counts stay well below 2^30; values
	// beyond it indicate ADC rail contact.
	saturationLimit = int64(1) << 30

	// rampRunLength is the minimum length of a monotonic run reported
	// as a RAMP issue.
	rampRunLength = 1000
)

// issueCollector accumulates findings and enforces the reporting cap;
// once full, further add calls become no-ops and full turns true so
// scanners can stop early.
type issueCollector struct {
	items []SampleIssue
}

func (c *issueCollector) full() bool { return len(c.items) >= maxReportedIssues }

func (c *issueCollector) add(index int, kind string, value int32) {
	if !c.full() {
		c.items = append(c.items, SampleIssue{Index: index, Kind: kind, Value: value})
	}
}

// ScanSamples inspects samples and returns up to maxReportedIssues
// issues, stopping early once that cap is reached. The order of the
// result follows scan order, not severity.
func ScanSamples(samples []int32) []SampleIssue {
	var c issueCollector
	scanStuck(&c, samples)
	if c.full() {
		return c.items
	}
	scanSaturated(&c, samples)
	if c.full() {
		return c.items
	}
	scanRamp(&c, samples)
	return c.items
}

// scanStuck reports runs of stuckRunLength or more identical samples.
func scanStuck(c *issueCollector, samples []int32) {
	runStart := 0
	for i := 1; i <= len(samples); i++ {
		if i < len(samples) && samples[i] == samples[i-1] {
			continue
		}
		if i-runStart >= stuckRunLength {
			c.add(runStart, IssueStuck, samples[runStart])
			if c.full() {
				return
			}
		}
		runStart = i
	}
}

// scanSaturated reports every sample whose absolute value exceeds the
// saturation guard band.
func scanSaturated(c *issueCollector, samples []int32) {
	for i, v := range samples {
		if abs64(int64(v)) > saturationLimit {
			c.add(i, IssueSaturated, v)
			if c.full() {
				return
			}
		}
	}
}

// scanRamp reports the start of every strictly increasing or strictly
// decreasing run of at least rampRunLength samples. Flat stretches end
// a ramp because they are already covered by STUCK detection.
func scanRamp(c *issueCollector, samples []int32) {
	runStart := 0
	dir := 0 // +1 rising, -1 falling, 0 undecided
	flush := func(end int) {
		if dir != 0 && end-runStart >= rampRunLength {
			c.add(runStart, IssueRamp, samples[runStart])
		}
	}
	for i := 1; i <= len(samples); i++ {
		if i == len(samples) {
			flush(i)
			break
		}
		switch step := cmpInt32(samples[i], samples[i-1]); {
		case step > 0:
			if dir < 0 {
				flush(i)
				runStart = i - 1
			} else if dir == 0 {
				runStart = i - 1
			}
			dir = 1
		case step < 0:
			if dir > 0 {
				flush(i)
				runStart = i - 1
			} else if dir == 0 {
				runStart = i - 1
			}
			dir = -1
		default:
			flush(i)
			runStart = i
			dir = 0
		}
	}
}

func cmpInt32(a, b int32) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	default:
		return 0
	}
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
