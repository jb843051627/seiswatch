package handler

import (
	"errors"
	"net/http"
	"time"

	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

func (r *router) handleCalibrationList(w http.ResponseWriter, req *http.Request) {
	jobs, err := r.Calib.List(parseLimit(req.URL.Query()))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

type calibrationRequest struct {
	StationCode   string                `json:"station_code"`
	Kind          model.CalibrationKind `json:"kind"`
	ScheduledAt   time.Time             `json:"scheduled_at"`
	WindowMinutes int                   `json:"window_minutes"`
}

func (r *router) handleCalibrationCreate(w http.ResponseWriter, req *http.Request) {
	var body calibrationRequest
	if err := decodeJSON(req.Body, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.StationCode == "" || body.Kind == "" || body.ScheduledAt.IsZero() {
		writeErr(w, http.StatusBadRequest, "station_code, kind and scheduled_at are required")
		return
	}
	st, err := r.DB.Stations.GetByCode(req.Context(), body.StationCode)
	if errors.Is(err, store.ErrNotFound) {
		writeNotFound(w)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if body.WindowMinutes <= 0 {
		body.WindowMinutes = 60
	}
	job, err := r.Calib.Schedule(st.ID, body.Kind, body.ScheduledAt, body.WindowMinutes)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (r *router) handleCalibrationStart(w http.ResponseWriter, req *http.Request) {
	id, ok := parseIDParam(w, req)
	if !ok {
		return
	}
	job, err := r.Calib.Start(id)
	if errors.Is(err, store.ErrNotFound) {
		writeNotFound(w)
		return
	}
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (r *router) handleCalibrationComplete(w http.ResponseWriter, req *http.Request) {
	id, ok := parseIDParam(w, req)
	if !ok {
		return
	}
	var body struct {
		Metrics map[string]float64 `json:"metrics"`
	}
	if err := decodeJSON(req.Body, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	job, err := r.Calib.Complete(id, body.Metrics)
	if errors.Is(err, store.ErrNotFound) {
		writeNotFound(w)
		return
	}
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (r *router) handleCalibrationFail(w http.ResponseWriter, req *http.Request) {
	id, ok := parseIDParam(w, req)
	if !ok {
		return
	}
	job, err := r.Calib.Fail(id)
	if errors.Is(err, store.ErrNotFound) {
		writeNotFound(w)
		return
	}
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, job)
}
