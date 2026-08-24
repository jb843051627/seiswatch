// Package handler wires HTTP endpoints to the domain services.
package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"seiswatch/internal/service"
	"seiswatch/internal/store"
	"seiswatch/internal/web"
)

// Deps aggregates everything the handlers need.
type Deps struct {
	DB     *store.DB
	Ingest *service.IngestService
	QC     *service.QCEngine
	Calib  *service.CalibrationService
	Maint  *service.MaintenanceService
	Alerts *service.AlertService
	Health *service.HealthService
	Report *service.ReportService
}

type router struct {
	Deps
}

// New builds the HTTP handler with all routes registered.
func New(d Deps) http.Handler {
	r := &router{Deps: d}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/frames", r.handleFrameIngest)
	mux.HandleFunc("POST /api/frames/batch", r.handleFrameBatch)
	mux.HandleFunc("GET /api/ingest/stats", r.handleIngestStats)
	mux.HandleFunc("GET /api/channels", r.handleChannelList)

	mux.HandleFunc("POST /api/stations", r.handleStationCreate)
	mux.HandleFunc("GET /api/stations", r.handleStationList)
	mux.HandleFunc("GET /api/stations/{code}", r.handleStationGet)
	mux.HandleFunc("GET /api/stations/{code}/channels", r.handleStationChannels)
	mux.HandleFunc("POST /api/channels", r.handleChannelCreate)

	mux.HandleFunc("GET /api/qc-events", r.handleQCEventList)
	mux.HandleFunc("POST /api/qc-events/{id}/ack", r.handleQCEventAck)
	mux.HandleFunc("POST /api/qc-events/{id}/resolve", r.handleQCEventResolve)

	mux.HandleFunc("GET /api/calibrations", r.handleCalibrationList)
	mux.HandleFunc("POST /api/calibrations", r.handleCalibrationCreate)
	mux.HandleFunc("POST /api/calibrations/{id}/start", r.handleCalibrationStart)
	mux.HandleFunc("POST /api/calibrations/{id}/complete", r.handleCalibrationComplete)
	mux.HandleFunc("POST /api/calibrations/{id}/fail", r.handleCalibrationFail)

	mux.HandleFunc("POST /api/maintenance", r.handleMaintenanceCreate)
	mux.HandleFunc("POST /api/maintenance/{id}/close", r.handleMaintenanceClose)
	mux.HandleFunc("GET /api/alerts", r.handleAlertList)

	mux.HandleFunc("GET /api/health/{code}", r.handleHealth)
	mux.HandleFunc("GET /api/report/daily.csv", r.handleDailyReport)

	mux.HandleFunc("GET /{$}", r.handleIndex)
	mux.Handle("GET /static/", http.FileServerFS(web.Static))

	return recoverPanic(requestLog(mux))
}

func (r *router) handleIndex(w http.ResponseWriter, req *http.Request) {
	f, err := web.Static.ReadFile("static/index.html")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "index not available")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(f)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeNotFound(w http.ResponseWriter) { writeErr(w, http.StatusNotFound, "not found") }

func decodeJSON(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	return dec.Decode(v)
}
