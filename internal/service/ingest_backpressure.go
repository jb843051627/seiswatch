package service

import (
	"sort"
	"sync"
	"time"
)

// BackpressureMonitor is a pure in-memory latency sampler for the ingest
// path. Handlers record per-frame processing durations and expose
// Snapshot() through /api/ingest/stats so operators can see queue pressure
// without touching the ingest service internals.
type BackpressureMonitor struct {
	mu      sync.Mutex
	samples []time.Duration
	max     int // ring capacity; oldest samples are dropped beyond it

	enqueued  uint64
	rejected  uint64
	processed uint64
	failed    uint64
}

// NewBackpressureMonitor creates a monitor keeping at most maxSamples
// recent durations (default 1024 when maxSamples <= 0).
func NewBackpressureMonitor(maxSamples int) *BackpressureMonitor {
	if maxSamples <= 0 {
		maxSamples = 1024
	}
	return &BackpressureMonitor{samples: make([]time.Duration, 0, maxSamples), max: maxSamples}
}

// Record appends one processing duration, trimming history beyond capacity.
func (m *BackpressureMonitor) Record(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d < 0 {
		d = 0
	}
	m.samples = append(m.samples, d)
	if len(m.samples) > m.max {
		// Drop the oldest half at once to avoid shifting on every call.
		drop := m.max / 2
		m.samples = append(m.samples[:0], m.samples[drop:]...)
	}
}

// CountEnqueued increments the accepted-submit counter.
func (m *BackpressureMonitor) CountEnqueued() { m.bump(&m.enqueued) }

// CountRejected increments the queue-full counter.
func (m *BackpressureMonitor) CountRejected() { m.bump(&m.rejected) }

// CountProcessed increments the successfully persisted-frame counter.
func (m *BackpressureMonitor) CountProcessed() { m.bump(&m.processed) }

// CountFailed increments the processing-error counter.
func (m *BackpressureMonitor) CountFailed() { m.bump(&m.failed) }

func (m *BackpressureMonitor) bump(dst *uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	*dst++
}

// Percentile returns the p-quantile of recorded durations using nearest-
// rank over a sorted copy. It returns 0 when no samples exist or p is out
// of (0,1].
func (m *BackpressureMonitor) Percentile(p float64) time.Duration {
	if p <= 0 || p > 1 {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.samples)
	if n == 0 {
		return 0
	}
	sorted := make([]time.Duration, n)
	copy(sorted, m.samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := int(mathCeil(p * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}

// Snapshot freezes the current counters and latency percentiles into a
// value safe to serialize as JSON.
type MonitorSnapshot struct {
	Depth     int           `json:"queue_depth"`
	Samples   int           `json:"latency_samples"`
	P50       time.Duration `json:"p50_ns"`
	P95       time.Duration `json:"p95_ns"`
	P99       time.Duration `json:"p99_ns"`
	Max       time.Duration `json:"max_ns"`
	Enqueued  uint64        `json:"enqueued"`
	Rejected  uint64        `json:"rejected"`
	Processed uint64        `json:"processed"`
	Failed    uint64        `json:"failed"`
}

// DepthFunc supplies the live channel depth to Snapshot; monitors stay
// decoupled from the IngestService by taking it as a parameter.
func (m *BackpressureMonitor) Snapshot(depth int, depthFn func() int) MonitorSnapshot {
	if depthFn != nil {
		depth = depthFn()
	}
	snap := MonitorSnapshot{Depth: depth, P50: -1, P95: -1, P99: -1, Max: -1}
	m.mu.Lock()
	snap.Samples = len(m.samples)
	snap.Enqueued = m.enqueued
	snap.Rejected = m.rejected
	snap.Processed = m.processed
	snap.Failed = m.failed
	samplesCopy := make([]time.Duration, len(m.samples))
	copy(samplesCopy, m.samples)
	m.mu.Unlock()

	if len(samplesCopy) > 0 {
		sort.Slice(samplesCopy, func(i, j int) bool { return samplesCopy[i] < samplesCopy[j] })
		snap.P50 = samplesCopy[(len(samplesCopy)-1)/2]
		snap.P95 = quantileNearestRank(samplesCopy, 0.95)
		snap.P99 = quantileNearestRank(samplesCopy, 0.99)
		snap.Max = samplesCopy[len(samplesCopy)-1]
	} else {
		snap.P50, snap.P95, snap.P99, snap.Max = 0, 0, 0, 0
	}
	return snap
}

// quantileNearestRank picks the nearest-rank element of an ascending slice.
func quantileNearestRank(sorted []time.Duration, p float64) time.Duration {
	n := len(sorted)
	rank := int(mathCeil(p * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}

func mathCeil(v float64) int {
	i := int(v)
	if float64(i) < v {
		return i + 1
	}
	return i
}

// Reset clears all history and counters; handy between load-test runs.
func (m *BackpressureMonitor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples = m.samples[:0]
	m.enqueued, m.rejected, m.processed, m.failed = 0, 0, 0, 0
}
