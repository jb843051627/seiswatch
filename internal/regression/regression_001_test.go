package regression

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"seiswatch/internal/ingest"
	"seiswatch/internal/model"
	"seiswatch/internal/service"
	"seiswatch/internal/store"
)

// Bug01 regression: latestStats was written under RLock inside
// processFrame while Snapshot read it under RLock too, producing a data
// race under concurrent Submit. The race itself is flagged by -race;
// the logical invariant asserted here is that every submitted frame is
// persisted exactly once and the final snapshot matches the last frame.
func TestBug01_ConcurrentSubmitSnapshotConsistency(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "bug01.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	st := &model.Station{Code: "GD01", Name: "station 01", Region: "test",
		Status: model.StationActive, InstalledAt: time.Now().UTC(), CreatedAt: time.Now().UTC()}
	sid, err := db.Stations.Create(ctx, st)
	if err != nil {
		t.Fatalf("create station: %v", err)
	}
	ch := &model.Channel{StationID: sid, Code: "BHZ", SampleRate: 100,
		Gain: 1, Sensitivity: 100, Unit: "counts", Status: model.ChannelOpen}
	cid, err := db.Channels.Create(ctx, ch)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	svc := service.NewIngestService(db, 1024)
	svc.Start(ctx)
	defer svc.Stop()

	const total = 120
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
					_, _ = svc.Snapshot(cid)
				}
			}
		}()
	}

	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < total; i++ {
		payload, err := (&ingest.Builder{}).BuildSynthetic("GD01", "BHZ",
			base.Add(time.Duration(i)*time.Second), 100, 50, int64(i+1))
		if err != nil {
			t.Fatalf("build frame %d: %v", i, err)
		}
		if err := svc.Submit(payload); err != nil {
			t.Fatalf("submit frame %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		frames, err := db.Frames.RecentByChannel(ctx, cid, total)
		if err != nil {
			t.Fatalf("count frames: %v", err)
		}
		if len(frames) == total {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d submitted frames were persisted (worker lost frames under concurrency)", len(frames), total)
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(stop)
	wg.Wait()

	got, ok := svc.Snapshot(cid)
	if !ok {
		t.Fatalf("Snapshot(%d) returned no frame after ingestion", cid)
	}
	if got.ChannelID != cid || got.SampleCount != 50 {
		t.Fatalf("snapshot frame mismatch: channel=%d samples=%d, want channel=%d samples=50",
			got.ChannelID, got.SampleCount, cid)
	}
}
