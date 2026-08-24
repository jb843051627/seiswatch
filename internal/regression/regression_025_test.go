package regression

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"seiswatch/internal/handler"
	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

// Bug25 regression: QC event resolve was a non-atomic check-then-act:
// the handler validated the current status via GetByID and then updated
// unconditionally, so two concurrent resolves could both pass the check
// and both "win" (double transition / state clobbering). After the fix
// the update is conditional and exactly one concurrent request wins.
func TestBug25_ConcurrentResolveOnlyOneWinner(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "bug25.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	h := handler.New(handler.Deps{DB: db})
	srv := httptest.NewServer(h)
	defer srv.Close()
	client := srv.Client()

	const rounds = 30
	const racers = 4
	for r := 0; r < rounds; r++ {
		ev := &model.QCEvent{StationID: 1, ChannelID: 1, RuleID: "SPIKE",
			Severity: model.SeverityCritical, Detail: "d", Status: model.QCOpen,
			DetectedAt: time.Now().UTC()}
		id, err := db.QCEvents.Create(ctx, ev)
		if err != nil {
			t.Fatalf("create event round %d: %v", r, err)
		}

		results := make(chan int, racers)
		start := make(chan struct{})
		for k := 0; k < racers; k++ {
			go func() {
				<-start
				req, err := http.NewRequest(http.MethodPost,
					fmt.Sprintf("%s/api/qc-events/%d/resolve", srv.URL, id), nil)
				if err != nil {
					results <- -1
					return
				}
				resp, err := client.Do(req)
				if err != nil {
					results <- -1
					return
				}
				results <- resp.StatusCode
				resp.Body.Close()
			}()
		}
		close(start)

		successes := 0
		for k := 0; k < racers; k++ {
			code := <-results
			if code == http.StatusOK {
				successes++
			} else if code < 0 {
				t.Fatalf("round %d: request transport error", r)
			}
		}
		if successes > 1 {
			t.Fatalf("round %d: %d concurrent resolves all succeeded on one event; UpdateStatus is not an atomic conditional transition", r, successes)
		}
	}
}
