package handler

import (
	"errors"
	"net/http"
	"time"

	"seiswatch/internal/store"
)

type maintenanceRequest struct {
	StationCode string    `json:"station_code"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Reason      string    `json:"reason"`
}

func (r *router) handleMaintenanceCreate(w http.ResponseWriter, req *http.Request) {
	var body maintenanceRequest
	if err := decodeJSON(req.Body, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.StationCode == "" || body.Start.IsZero() || body.End.IsZero() {
		writeErr(w, http.StatusBadRequest, "station_code, start and end are required")
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
	win, err := r.Maint.PlanWindow(st.ID, body.Start, body.End, body.Reason)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, win)
}

func (r *router) handleMaintenanceClose(w http.ResponseWriter, req *http.Request) {
	id, ok := parseIDParam(w, req)
	if !ok {
		return
	}
	win, err := r.Maint.Close(id)
	if errors.Is(err, store.ErrNotFound) {
		writeNotFound(w)
		return
	}
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, win)
}
