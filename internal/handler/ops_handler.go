package handler

import (
	"io"
	"net/http"
	"strconv"
)

func (r *router) handleFrameIngest(w http.ResponseWriter, req *http.Request) {
	payload, err := io.ReadAll(req.Body)
	if err != nil || len(payload) == 0 {
		writeErr(w, http.StatusBadRequest, "invalid frame body")
		return
	}
	if err := r.Ingest.Submit(payload); err != nil {
		writeErr(w, http.StatusBadRequest, "frame decode failed")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

func parseIDParam(w http.ResponseWriter, req *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}
