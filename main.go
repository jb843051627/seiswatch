// Command seiswatch runs the seismic network data quality-control service.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"seiswatch/internal/handler"
	"seiswatch/internal/service"
	"seiswatch/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "http listen address")
	dataDir := flag.String("data", ".", "directory holding the sqlite database file")
	flag.Parse()

	dbPath := filepath.Join(*dataDir, "seiswatch.db")
	db, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer db.Close()

	ingest := service.NewIngestService(db, 256)
	engine := service.NewQCEngine()
	service.RegisterDefaultRules(engine)

	calibSvc := service.NewCalibrationService(db)
	maintSvc := service.NewMaintenanceService(db)
	alertSvc := service.NewAlertService(db)
	healthSvc := service.NewHealthService(db)
	reportSvc := service.NewReportService(db)

	deps := handler.Deps{
		DB:     db,
		Ingest: ingest,
		QC:     engine,
		Calib:  calibSvc,
		Maint:  maintSvc,
		Alerts: alertSvc,
		Health: healthSvc,
		Report: reportSvc,
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler.New(deps),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ingest.Start(ctx)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("seiswatch listening on %s (db=%s)", *addr, dbPath)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
	ingest.Stop()
}
