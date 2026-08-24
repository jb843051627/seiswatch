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

// Bug03 regression: ActiveAt compared end_ts with a strict > so a query
// exactly at the window end fell outside, and IsSuppressed failed open.
// The inclusive boundary must report suppression at the window's End.
func TestBug03_MaintenanceWindowEndBoundarySuppresses(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "bug03.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	st := &model.Station{Code: "GD03", Name: "station 03", Region: "test",
		Status: model.StationActive, InstalledAt: time.Now().UTC(), CreatedAt: time.Now().UTC()}
	sid, err := db.Stations.Create(ctx, st)
	if err != nil {
		t.Fatalf("create station: %v", err)
	}

	msvc := service.NewMaintenanceService(db)
	start := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	if _, err := msvc.PlanWindow(sid, start, end, "sensor swap"); err != nil {
		t.Fatalf("plan window: %v", err)
	}

	if !msvc.IsSuppressed(sid, start) {
		t.Errorf("IsSuppressed at window start = false, want true")
	}
	if !msvc.IsSuppressed(sid, start.Add(time.Hour)) {
		t.Errorf("IsSuppressed inside window = false, want true")
	}
	// The critical assertion: the window boundary itself is inclusive.
	if !msvc.IsSuppressed(sid, end) {
		t.Fatal("IsSuppressed(stationID, windowEnd) = false, want true (end_ts >= at)")
	}
	if msvc.IsSuppressed(sid, end.Add(time.Second)) {
		t.Errorf("IsSuppressed one second past window end = true, want false")
	}
}
