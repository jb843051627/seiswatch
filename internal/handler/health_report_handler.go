package handler

import (
	"errors"
	"net/http"
	"time"

	"seiswatch/internal/store"
)

func (r *router) handleHealth(w http.ResponseWriter, req *http.Request) {
	st, err := r.DB.Stations.GetByCode(req.Context(), req.PathValue("code"))
	if errors.Is(err, store.ErrNotFound) {
		writeNotFound(w)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	score, factors, err := r.Health.Score(st.ID, time.Now().UTC())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"score": score, "factors": factors})
}

func (r *router) handleDailyReport(w http.ResponseWriter, req *http.Request) {
	day := time.Now().UTC().Truncate(24 * time.Hour)
	if s := req.URL.Query().Get("date"); s != "" {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid date, expect YYYY-MM-DD")
			return
		}
		day = d.UTC()
	}
	csv, err := r.Report.DailyCSV(day)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	_, _ = w.Write(csv)
}
