package regression

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"seiswatch/internal/ingest"
	"seiswatch/internal/model"
	"seiswatch/internal/service"
	"seiswatch/internal/store"
)

// Bug16 regression: processFrame loaded the recent-frames history AFTER
// inserting the current frame, so the GAP rule compared the frame with
// itself (gap=0) and never fired. The history must be a pre-insert
// snapshot of previous frames.
func TestBug16_GapRuleSeesPreviousFrameHistory(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "bug16.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	st := &model.Station{Code: "GD16", Name: "station 16", Region: "test",
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

	t0 := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	if _, err := db.Frames.Insert(ctx, &model.DataFrame{
		ChannelID: cid, StartTime: t0.Add(-time.Minute), EndTime: t0,
		SampleCount: 6000, Min: -1, Max: 1, Mean: 0, RMS: 0.1,
		ReceivedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("insert previous frame ending at t0: %v", err)
	}

	engine := service.NewQCEngine()
	engine.Register(service.GapRule{})
	svc := service.NewIngestService(db, 8)
	svc.SetEngine(engine)
	svc.Start(ctx)
	defer svc.Stop()

	payload, err := (&ingest.Builder{}).BuildSynthetic("GD16", "BHZ",
		t0.Add(10*time.Second), 100, 100, 42)
	if err != nil {
		t.Fatalf("build frame: %v", err)
	}
	if err := svc.Submit(payload); err != nil {
		t.Fatalf("submit frame: %v", err)
	}

	// Wait until the worker produced the expected GAP finding.
	deadline := time.Now().Add(15 * time.Second)
	for {
		events, err := db.QCEvents.ListByStation(ctx, sid, 100)
		if err != nil {
			t.Fatalf("list qc events: %v", err)
		}
		for _, ev := range events {
			if ev.RuleID == "GAP" && ev.FrameID != 0 {
				return // found the expected gap finding
			}
		}
		frames, _ := db.Frames.RecentByChannel(ctx, cid, 10)
		if len(frames) > 0 && time.Now().After(deadline) {
			break // frame persisted but no GAP finding ever appeared
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("GAP rule did not fire across a 10s gap; History passed to the engine included the current frame itself (gap computed as 0)")
}
