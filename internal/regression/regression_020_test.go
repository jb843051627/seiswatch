package regression

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"seiswatch/internal/handler"
	"seiswatch/internal/service"
	"seiswatch/internal/store"
)

// Bug20 regression: report aggregation built its queries on
// context.Background() internally, so canceling the incoming request
// context did not stop the daily CSV query. The handler must honor the
// request context end to end.
func TestBug20_DailyReportHonorsCanceledRequestContext(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "bug20.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	h := handler.New(handler.Deps{DB: db, Report: service.NewReportService(db)})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/report/daily.csv?date=2026-08-17", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("daily report completed with an already-canceled request context; aggregation used context.Background() instead of propagating ctx")
	}
}
