package regression

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"seiswatch/internal/model"
	"seiswatch/internal/service"
	"seiswatch/internal/store"
)

// Bug06 regression: health scoring counted resolved QC events as
// penalties, both in the live Score and in the per-day History
// reconstruction. Only open (and ack) events must count.
func TestBug06_HealthScoreIgnoresResolvedEvents(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "bug06.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	st := &model.Station{Code: "GD06", Name: "station 06", Region: "test",
		Status: model.StationActive, InstalledAt: time.Now().UTC(), CreatedAt: time.Now().UTC()}
	sid, err := db.Stations.Create(ctx, st)
	if err != nil {
		t.Fatalf("create station: %v", err)
	}

	now := time.Now().UTC()
	mk := func(sev model.Severity, status model.QCStatus) {
		t.Helper()
		ev := &model.QCEvent{StationID: sid, ChannelID: 1, RuleID: "SPIKE",
			Severity: sev, Detail: "x", Status: status, DetectedAt: now}
		if _, err := db.QCEvents.Create(ctx, ev); err != nil {
			t.Fatalf("create qc event: %v", err)
		}
	}
	mk(model.SeverityCritical, model.QCResolved)
	mk(model.SeverityWarn, model.QCOpen)

	hs := service.NewHealthService(db)
	score, factors, err := hs.Score(sid, now)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if score != 95 {
		t.Fatalf("Score = %.0f, want 95 (resolved critical -15 must be ignored)", score)
	}
	if len(factors) != 1 {
		t.Fatalf("factors = %v, want exactly one warn factor", factors)
	}
}
