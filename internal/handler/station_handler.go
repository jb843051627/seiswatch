package handler

import (
	"errors"
	"net/http"
	"time"

	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

type stationRequest struct {
	Code        string              `json:"code"`
	Name        string              `json:"name"`
	Region      string              `json:"region"`
	Latitude    float64             `json:"latitude"`
	Longitude   float64             `json:"longitude"`
	Status      model.StationStatus `json:"status"`
	InstalledAt *time.Time          `json:"installed_at"`
}

func (r *router) handleStationCreate(w http.ResponseWriter, req *http.Request) {
	var body stationRequest
	if err := decodeJSON(req.Body, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Code == "" || body.Name == "" {
		writeErr(w, http.StatusBadRequest, "code and name are required")
		return
	}
	st := &model.Station{
		Code:      body.Code,
		Name:      body.Name,
		Region:    body.Region,
		Latitude:  body.Latitude,
		Longitude: body.Longitude,
	}
	if body.Status != "" {
		st.Status = body.Status
	}
	if body.InstalledAt != nil {
		st.InstalledAt = *body.InstalledAt
	} else {
		st.InstalledAt = time.Now().UTC()
	}
	id, err := r.DB.Stations.Create(req.Context(), st)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	st.ID = id
	writeJSON(w, http.StatusCreated, st)
}

func (r *router) handleStationList(w http.ResponseWriter, req *http.Request) {
	stations, err := r.DB.Stations.List(req.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stations)
}

func (r *router) handleStationGet(w http.ResponseWriter, req *http.Request) {
	code := req.PathValue("code")
	st, err := r.DB.Stations.GetByCode(req.Context(), code)
	if errors.Is(err, store.ErrNotFound) {
		writeNotFound(w)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (r *router) handleStationChannels(w http.ResponseWriter, req *http.Request) {
	st, err := r.DB.Stations.GetByCode(req.Context(), req.PathValue("code"))
	if errors.Is(err, store.ErrNotFound) {
		writeNotFound(w)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	channels, err := r.DB.Channels.ListByStation(req.Context(), st.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, channels)
}

type channelRequest struct {
	StationCode string  `json:"station_code"`
	Code        string  `json:"code"`
	SampleRate  float64 `json:"sample_rate"`
	Gain        float64 `json:"gain"`
	Sensitivity float64 `json:"sensitivity"`
	Unit        string  `json:"unit"`
}

func (r *router) handleChannelCreate(w http.ResponseWriter, req *http.Request) {
	var body channelRequest
	if err := decodeJSON(req.Body, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.StationCode == "" || body.Code == "" {
		writeErr(w, http.StatusBadRequest, "station_code and code are required")
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
	ch := &model.Channel{
		StationID:   st.ID,
		Code:        body.Code,
		SampleRate:  body.SampleRate,
		Gain:        body.Gain,
		Sensitivity: body.Sensitivity,
		Unit:        body.Unit,
		Status:      "open",
	}
	id, err := r.DB.Channels.Create(req.Context(), ch)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ch.ID = id
	writeJSON(w, http.StatusCreated, ch)
}
