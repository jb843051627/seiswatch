package regression

import (
	"context"
	"strings"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"seiswatch/internal/handler"
	"seiswatch/internal/model"
	"seiswatch/internal/service"
	"seiswatch/internal/store"
)

// Bug27 regression: calibration state machine violations surfaced as
// plain text errors mapped to 500 by the HTTP layer, so clients could
// not distinguish "invalid transition" (409) from real server faults.
func TestBug27_CalibrationInvalidTransitionMapsToConflict(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "bug27.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	st := &model.Station{Code: "GD27", Name: "station 27", Region: "test",
		Status: model.StationActive, InstalledAt: time.Now().UTC(), CreatedAt: time.Now().UTC()}
	sid, err := db.Stations.Create(ctx, st)
	if err != nil {
		t.Fatalf("create station: %v", err)
	}

	csvc := service.NewCalibrationService(db)
	h := handler.New(handler.Deps{DB: db, Calib: csvc})

	post := func(path, body string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	now := time.Now().UTC()
	if _, err := csvc.Schedule(sid, model.CalibGainCheck, now, 60); err != nil {
		t.Fatalf("schedule job: %v", err)
	}
	if code := post("/api/calibrations/1/start", ""); code != http.StatusOK {
		t.Fatalf("first start returned %d, want 200 (harness broken)", code)
	}
	// Second start on a running job: a business-state violation.
	if code := post("/api/calibrations/1/start", ""); code != http.StatusConflict {
		t.Fatalf("start on running job returned %d, want 409 Conflict (state error was swallowed into a generic 5xx)", code)
	}

	pending, err := csvc.Schedule(sid, model.CalibMassCenter, now, 60)
	if err != nil {
		t.Fatalf("schedule pending job: %v", err)
	}
	if code := post("/api/calibrations/" + itoa(pending.ID) + "/complete", `{"metrics":{"offset":1,"variance":1}}`); code != http.StatusConflict {
		t.Fatalf("complete on pending job returned %d, want 409 Conflict", code)
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
