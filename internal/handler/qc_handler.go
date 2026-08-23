package handler

import (
	"net/http"
	"strconv"

	"seiswatch/internal/model"
)

func (r *router) handleQCEventList(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	limit := parseLimit(q)

	var (
		events []*model.QCEvent
		err    error
	)
	if s := q.Get("station"); s != "" {
		stationID, perr := strconv.ParseInt(s, 10, 64)
		if perr != nil {
			writeErr(w, http.StatusBadRequest, "invalid station id")
			return
		}
		events, err = r.DB.QCEvents.ListByStation(req.Context(), stationID, limit)
	} else {
		events, err = r.DB.QCEvents.ListByStatus(req.Context(), model.QCStatus(q.Get("status")), limit)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sev := model.Severity(q.Get("severity")); sev != "" {
		filtered := events[:0]
		for _, ev := range events {
			if ev.Severity == sev {
				filtered = append(filtered, ev)
			}
		}
		events = filtered
	}
	writeJSON(w, http.StatusOK, events)
}

func (r *router) handleQCEventAck(w http.ResponseWriter, req *http.Request) {
	r.transitionQCEvent(w, req, func(cur model.QCStatus) bool {
		return cur == model.QCOpen
	}, model.QCAcked)
}

func (r *router) handleQCEventResolve(w http.ResponseWriter, req *http.Request) {
	r.transitionQCEvent(w, req, func(cur model.QCStatus) bool {
		return cur == model.QCOpen || cur == model.QCAcked
	}, model.QCResolved)
}

func (r *router) transitionQCEvent(w http.ResponseWriter, req *http.Request, allowed func(model.QCStatus) bool, next model.QCStatus) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	ev, err := r.DB.QCEvents.GetByID(req.Context(), id)
	if err != nil {
		writeNotFound(w)
		return
	}
	if !allowed(ev.Status) {
		writeErr(w, http.StatusConflict, "invalid state")
		return
	}
	if err := r.DB.QCEvents.UpdateStatus(req.Context(), id, next); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ev.Status = next
	writeJSON(w, http.StatusOK, ev)
}
