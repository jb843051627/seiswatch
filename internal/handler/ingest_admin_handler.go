package handler

import (
	"io"
	"net/http"
	"time"

	"seiswatch/internal/ingest"
)

// ingestStatsResponse is the payload of GET /api/ingest/stats.
type ingestStatsResponse struct {
	Status      string    `json:"status"`
	Time        time.Time `json:"time"`
	FramesToday int64     `json:"frames_today"`
}

// handleIngestStats reports the ingestion subsystem status. The queue
// depth lives inside IngestService, which is owned by another package;
// until it exposes a snapshot this endpoint answers with a liveness
// document plus the number of frames persisted since UTC midnight so
// operators can see whether ingestion is still making progress.
func (r *router) handleIngestStats(w http.ResponseWriter, req *http.Request) {
	if err := r.DB.HealthCheck(req.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	midnight := time.Now().UTC().Truncate(24 * time.Hour)
	framesToday, err := r.DB.Frames.CountBetween(req.Context(), midnight, time.Now().UTC())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ingestStatsResponse{
		Status:      "ok",
		Time:        time.Now().UTC(),
		FramesToday: framesToday,
	})
}

// handleFrameBatch accepts a body made of several SWIF frames glued
// back to back. It walks the stream frame by frame: each header is
// parsed to learn the frame length, then the complete frame is handed
// to the regular Submit path so per-frame accounting stays identical
// to single-frame ingestion.
func (r *router) handleFrameBatch(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid batch body")
		return
	}
	var queued, failed int
	offset := 0
	for offset < len(body) {
		rest := len(body) - offset
		if rest < ingest.HeaderSize {
			failed++ // trailing garbage shorter than any header
			break
		}
		h, err := ingest.ParseHeader(body[offset:])
		if err != nil {
			failed++ // cannot trust the stream position afterwards
			break
		}
		size := h.FrameSize()
		end := offset + size
		if end > len(body) {
			failed++ // truncated final frame
			break
		}
		if err := r.Ingest.Submit(body[offset:end]); err != nil {
			failed++
		} else {
			queued++
		}
		offset = end
	}
	status := http.StatusAccepted
	if queued == 0 && failed > 0 {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]int{"queued": queued, "failed": failed})
}

// channelRow is one entry of GET /api/channels: a channel joined with
// its owning station so clients get station codes without a second call.
type channelRow struct {
	ChannelID   int64     `json:"channel_id"`
	StationCode string    `json:"station_code"`
	Code        string    `json:"code"`
	SampleRate  float64   `json:"sample_rate"`
	Gain        float64   `json:"gain"`
	Sensitivity float64   `json:"sensitivity"`
	Unit        string    `json:"unit"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// handleChannelList returns every channel in the network via a direct
// SQL join over channels and stations, ordered for stable output.
func (r *router) handleChannelList(w http.ResponseWriter, req *http.Request) {
	rows, err := r.DB.SQL().QueryContext(req.Context(), `
SELECT c.id, st.code, c.code, c.sample_rate, c.gain, c.sensitivity, c.unit, c.status, c.created_at
FROM channels c
JOIN stations st ON st.id = c.station_id
ORDER BY st.code, c.code`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []channelRow{}
	for rows.Next() {
		var ch channelRow
		if err := rows.Scan(&ch.ChannelID, &ch.StationCode, &ch.Code, &ch.SampleRate,
			&ch.Gain, &ch.Sensitivity, &ch.Unit, &ch.Status, &ch.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}
