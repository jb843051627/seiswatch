package regression

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"seiswatch/internal/service"
)

// Bug14 regression: RetryQueue Push/PopDue/Len mutated the shared items
// slice without taking the struct's mutex, racing concurrent producers
// and consumers. Under -race this test exposes the race; logically it
// asserts no payload is lost or duplicated.
func TestBug14_RetryQueueConcurrentPushPopConsistency(t *testing.T) {
	q := service.NewRetryQueue(4096)

	const workers = 16
	const perWorker = 20
	base := time.Now().UTC().Truncate(time.Second)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				q.Push([]byte(fmt.Sprintf("payload-%d-%d", w, i)), base)
			}
		}(w)
	}
	// Concurrent readers hammer Len/NextReady while producers run.
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
					_ = q.Len()
					_ = q.NextReady()
					_, _ = q.PopDue(base) // nothing due yet (2s backoff)
				}
			}
		}()
	}

	total := workers * perWorker
	deadline := time.Now().Add(10 * time.Second)
	for q.Len() < total {
		if time.Now().After(deadline) {
			t.Fatalf("queue holds %d items after all pushes, want %d (lost updates on shared slice)", q.Len(), total)
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)

	seen := make(map[string]bool, total)
	later := base.Add(time.Hour)
	for {
		payload, ok := q.PopDue(later)
		if !ok {
			break
		}
		if seen[string(payload)] {
			t.Fatalf("payload %q delivered twice", payload)
		}
		seen[string(payload)] = true
	}
	if len(seen) != total {
		t.Fatalf("drained %d unique payloads, want %d", len(seen), total)
	}
	if dropped := q.Dropped(); dropped != 0 {
		t.Fatalf("Dropped() = %d, want 0 (queue capacity was sufficient)", dropped)
	}
}
