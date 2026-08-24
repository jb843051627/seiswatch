package regression

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"seiswatch/internal/model"
	"seiswatch/internal/service"
	"seiswatch/internal/store"
)

// Bug30 regression: DailyCSV derived the report day boundaries in the
// machine's local timezone while data timestamps are UTC, so reports on
// non-UTC hosts shifted frames across day rows. Day boundaries must be
// UTC.
func TestBug30_DailyCSVUsesUTCDayBoundary(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("UTC+8", 8*60*60)
	defer func() { time.Local = oldLocal }()
	db, err := store.Open(filepath.Join(t.TempDir(), "bug30.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	st := &model.Station{Code: "GD30", Name: "station 30", Region: "test",
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

	// 2026-08-17T23:30Z is already 2026-08-18 07:30 in UTC+8; a local-day
	// boundary would push it into the next report.
	frameStart := time.Date(2026, 8, 17, 23, 30, 0, 0, time.UTC)
	if _, err := db.Frames.Insert(ctx, &model.DataFrame{
		ChannelID: cid, StartTime: frameStart, EndTime: frameStart.Add(time.Minute),
		SampleCount: 6000, Min: -1, Max: 1, Mean: 0, RMS: 0.7,
		ReceivedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("insert frame: %v", err)
	}

	rs := service.NewReportService(db)
	out17, err := rs.DailyCSV(time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("DailyCSV(2026-08-17): %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(out17), "\r\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("frame at 2026-08-17T23:30Z missing from the UTC daily report (got %d lines); day boundary drifted to local timezone", len(lines))
	}
	if !strings.Contains(lines[1], "2026-08-17T23:30:00Z") {
		t.Fatalf("unexpected first data row: %s", lines[1])
	}

	out18, err := rs.DailyCSV(time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("DailyCSV(2026-08-18): %v", err)
	}
	lines18 := strings.Split(strings.TrimRight(string(out18), "\r\n"), "\n")
	if len(lines18) != 1 {
		t.Fatalf("2026-08-18 report contains %d lines, want header only (frame leaked across local day boundary)", len(lines18))
	}
}
