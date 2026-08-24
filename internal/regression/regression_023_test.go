package regression

import (
	"sync"
	"testing"
	"time"

	"seiswatch/internal/service"
)

// Bug23 regression: the package-wide AlertPolicy was swapped and read
// with plain assignment/load, so concurrent SetAlertPolicy /
// CurrentAlertPolicy (e.g. admin endpoint vs escalation path) raced on
// the policy value. Under -race this test exposes the race; logically
// every observed policy must be a fully valid one and the final write
// must be visible.
func TestBug23_AlertPolicySwapIsConcurrencySafe(t *testing.T) {
	original := service.CurrentAlertPolicy()
	defer service.SetAlertPolicy(original)

	policies := []service.AlertPolicy{
		{DedupeWindow: 15 * time.Minute, MaxPerHour: 60},
		{DedupeWindow: 30 * time.Minute, SuppressInfo: true, MaxPerHour: 30},
		{DedupeWindow: time.Minute, MaxPerHour: 5},
		{DedupeWindow: 0, MaxPerHour: 0},
	}

	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				service.SetAlertPolicy(policies[(w+i)%len(policies)])
			}
		}(w)
	}
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				p := service.CurrentAlertPolicy()
				if p.DedupeWindow < 0 || p.MaxPerHour < 0 || p.MaxPerHour > 60 {
					t.Errorf("observed invalid/torn policy: %+v", p)
					return
				}
			}
		}()
	}
	wg.Wait()

	// After the storm settles, the last write must win deterministically.
	final := service.AlertPolicy{DedupeWindow: 42 * time.Minute, SuppressInfo: true, MaxPerHour: 42}
	var finalWG sync.WaitGroup
	for w := 0; w < 16; w++ {
		finalWG.Add(1)
		go func() {
			defer finalWG.Done()
			service.SetAlertPolicy(final)
		}()
	}
	finalWG.Wait()
	got := service.CurrentAlertPolicy()
	if got != final {
		t.Fatalf("CurrentAlertPolicy = %+v after final SetAlertPolicy, want %+v", got, final)
	}
}
