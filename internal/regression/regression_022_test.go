package regression

import (
	"sync"
	"testing"
	"time"

	"seiswatch/internal/service"
)

// Bug22 regression: BackpressureMonitor.Record appended to the shared
// samples slice while Percentile/Snapshot read it without holding the
// mutex (the mutex only guarded the counters). Under -race this test
// exposes the read/write race; logically it asserts that no recorded
// sample is lost and the final percentile is exact.
func TestBug22_BackpressureMonitorConcurrentRecordAndPercentile(t *testing.T) {
	m := service.NewBackpressureMonitor(4096)

	const workers = 40
	const perWorker = 25 // 1000 records: values 0..99ms, each exactly 10 times
	total := workers * perWorker

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = m.Percentile(0.99)
					_ = m.Snapshot(0, nil)
				}
			}
		}()
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				m.Record(time.Duration((w*perWorker+i)%100) * time.Millisecond)
			}
		}(w)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		snap := m.Snapshot(0, nil)
		if snap.Samples == total {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("monitor holds %d samples after %d Record calls, want %d (concurrent appends lost data)", snap.Samples, total, total)
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)

	snap := m.Snapshot(0, nil)
	wantP50 := 49 * time.Millisecond // nearest-rank p50 of the uniform 0..99 distribution
	if snap.P50 != wantP50 {
		t.Fatalf("P50 = %s, want %s (samples were corrupted or lost under concurrency)", snap.P50, wantP50)
	}
	if snap.P99 != 98*time.Millisecond { // ceil(0.99*1000)=990 -> sorted[989] -> 98ms
		t.Fatalf("unexpected p99: %s, want 98ms", snap.P99)
	}
	if snap.Max != 99*time.Millisecond {
		t.Fatalf("unexpected max: %s, want 99ms", snap.Max)
	}
	wg.Wait()
}
