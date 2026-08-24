package regression

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

// Bug11 regression: ListByTimeRange ordered rows by start_time DESC
// while gap analysis assumed ascending order, so adjacent deltas went
// negative and every gap statistic collapsed to zero. The query must
// return frames chronologically.
func TestBug11_ListByTimeRangeReturnsChronologicalOrder(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "bug11.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	st := &model.Station{Code: "GD11", Name: "station 11", Region: "test",
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

	base := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	stamps := []time.Time{base, base.Add(2 * time.Hour), base.Add(time.Hour)} // inserted out of order
	for i, s := range stamps {
		if _, err := db.Frames.Insert(ctx, &model.DataFrame{
			ChannelID: cid, StartTime: s, EndTime: s.Add(time.Minute),
			SampleCount: 6000, Min: -1, Max: 1, Mean: 0, RMS: float64(i),
			ReceivedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("insert frame %d: %v", i, err)
		}
	}

	frames, err := db.Frames.ListByTimeRange(ctx, cid, base.Add(-time.Minute), base.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("ListByTimeRange: %v", err)
	}
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want 3", len(frames))
	}
	for i := 1; i < len(frames); i++ {
		if !frames[i-1].StartTime.Before(frames[i].StartTime) {
			t.Fatalf("frames not in ascending start_time order: [%d]=%s >= [%d]=%s",
				i-1, frames[i-1].StartTime, i, frames[i].StartTime)
		}
	}
	if !frames[0].StartTime.Equal(base) || !frames[2].StartTime.Equal(base.Add(2*time.Hour)) {
		t.Fatalf("unexpected ordering endpoints: first=%s last=%s", frames[0].StartTime, frames[2].StartTime)
	}
}
