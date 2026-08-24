package handler

import (
	"net/http"
)

func (r *router) handleAlertList(w http.ResponseWriter, req *http.Request) {
	alerts, err := r.Alerts.List(parseLimit(req.URL.Query()))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, alerts)
}
