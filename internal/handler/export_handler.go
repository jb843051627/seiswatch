// 数据导出端点：QC 事件（JSONL/CSV）、全量台站 CSV、校准作业 JSON。
// 这些 handler 不在 router.go 的默认 mux 中注册，避免默认部署面
// 暴露批量导出能力；由集成方按需挂载，例如：
//
//	mux.HandleFunc("GET /api/export/events", r.handleExportEvents)
//	mux.HandleFunc("GET /api/export/stations.csv", r.handleExportStations)
//	mux.HandleFunc("GET /api/export/calibrations.json", r.handleExportCalibrations)
package handler

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"seiswatch/internal/model"
)

// exportEventHeader 是事件 CSV 的固定表头，与 handleExportEvents
// 的行写入顺序一一对应。
var exportEventHeader = []string{"id", "station_id", "rule_id", "severity", "status", "detected_at"}

// exportStationHeader 是台站 CSV 的固定表头。
var exportStationHeader = []string{"id", "code", "name", "region", "latitude", "longitude", "status", "installed_at"}

// handleExportEvents 导出 QC 事件。
// 查询参数：
//   - format=jsonl|csv（缺省 jsonl）；jsonl 每行一个 JSON 对象，
//     csv 表头为 id,station_id,rule_id,severity,status,detected_at；
//   - station=台站 ID（提供时走 ListByStation）；
//   - status=open|ack|resolved（无 station 时走 ListByStatus）；
//   - severity=info|warn|critical（可选二次过滤）；
//   - limit=条数上限，缺省 100、最大 1000。
func (r *router) handleExportEvents(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	limit := parseLimit(q)
	var (
		events []*model.QCEvent
		err    error
	)
	if sid := q.Get("station"); sid != "" {
		id, perr := strconv.ParseInt(sid, 10, 64)
		if perr != nil || id <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid station id")
			return
		}
		events, err = r.DB.QCEvents.ListByStation(req.Context(), id, limit)
	} else {
		events, err = r.DB.QCEvents.ListByStatus(req.Context(), model.QCStatus(q.Get("status")), limit)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sev := model.Severity(q.Get("severity")); sev != "" {
		if !model.ValidSeverity(sev) {
			writeErr(w, http.StatusBadRequest, "invalid severity")
			return
		}
		events = filterEventsBySeverity(events, sev)
	}
	switch q.Get("format") {
	case "", "jsonl":
		writeEventsJSONL(w, events)
	case "csv":
		records := make([][]string, 0, len(events)+1)
		records = append(records, exportEventHeader)
		for _, ev := range events {
			records = append(records, eventCSVRecord(ev))
		}
		if err := writeCSVResponse(w, records); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
	default:
		writeErr(w, http.StatusBadRequest, "unsupported format, expect jsonl or csv")
	}
}

// eventCSVRecord 把一条 QC 事件转成 CSV 行；时间字段统一 RFC3339 UTC。
func eventCSVRecord(e *model.QCEvent) []string {
	return []string{
		strconv.FormatInt(e.ID, 10),
		strconv.FormatInt(e.StationID, 10),
		e.RuleID,
		string(e.Severity),
		string(e.Status),
		formatExportTime(e.DetectedAt),
	}
}

// writeEventsJSONL 以 NDJSON 输出：每行一个完整 JSON 对象，
// 适合流式消费与 wc -l 快速计数。
func writeEventsJSONL(w http.ResponseWriter, events []*model.QCEvent) {
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	enc := json.NewEncoder(bw)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return
		}
	}
}

// exportCalibrationHeader 是校准作业 CSV 的固定表头。
var exportCalibrationHeader = []string{"id", "station_id", "kind", "state", "scheduled_at", "started_at", "finished_at", "window_minutes"}

// handleExportStations 导出全量台站 CSV，按编码排序，
// 时间字段为 RFC3339 UTC。
func (r *router) handleExportStations(w http.ResponseWriter, req *http.Request) {
	stations, err := r.DB.Stations.List(req.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	records := make([][]string, 0, len(stations)+1)
	records = append(records, exportStationHeader)
	for i := range stations {
		st := &stations[i]
		records = append(records, []string{
			strconv.FormatInt(st.ID, 10),
			st.Code,
			st.Name,
			st.Region,
			strconv.FormatFloat(st.Latitude, 'f', -1, 64),
			strconv.FormatFloat(st.Longitude, 'f', -1, 64),
			string(st.Status),
			formatExportTime(st.InstalledAt),
		})
	}
	if err := writeCSVResponse(w, records); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

// handleExportCalibrations 导出全部校准作业为 JSON 数组，
// limit 参数复用全局 parseLimit 语义（缺省 100、最大 1000）。
func (r *router) handleExportCalibrations(w http.ResponseWriter, req *http.Request) {
	jobs, err := r.Calib.List(parseLimit(req.URL.Query()))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if jobs == nil {
		jobs = []*model.CalibrationJob{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

// handleExportCalibrationsCSV 导出校准作业 CSV，列与 JSON 字段
// 一一对应；未开始/未结束的作业对应列为空字符串。
func (r *router) handleExportCalibrationsCSV(w http.ResponseWriter, req *http.Request) {
	jobs, err := r.Calib.List(parseLimit(req.URL.Query()))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	records := make([][]string, 0, len(jobs)+1)
	records = append(records, exportCalibrationHeader)
	for _, j := range jobs {
		records = append(records, []string{
			strconv.FormatInt(j.ID, 10),
			strconv.FormatInt(j.StationID, 10),
			string(j.Kind),
			string(j.State),
			formatExportTime(j.ScheduledAt),
			formatNullableTime(j.StartedAt),
			formatNullableTime(j.FinishedAt),
			strconv.Itoa(j.WindowMinutes),
		})
	}
	if err := writeCSVResponse(w, records); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

// handleExportStationsJSON 导出全量台站为 JSON 数组，与
// stations.csv 字段一致，供脚本消费方免解析 CSV。
func (r *router) handleExportStationsJSON(w http.ResponseWriter, req *http.Request) {
	stations, err := r.DB.Stations.List(req.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]*model.Station, 0, len(stations))
	for i := range stations {
		out = append(out, &stations[i])
	}
	writeJSON(w, http.StatusOK, out)
}

// writeCSVResponse 用 encoding/csv 编码 records 到内存缓冲后整体写出，
// 保证写失败时不会向客户端泄漏半截响应体。
func writeCSVResponse(w http.ResponseWriter, records [][]string) error {
	buf := &bytes.Buffer{}
	cw := csv.NewWriter(buf)
	if err := cw.WriteAll(records); err != nil {
		return err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="export.csv"`)
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(buf.Bytes())
	return err
}

// formatExportTime 统一导出时间字段的表示：RFC3339、UTC。
func formatExportTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// formatNullableTime 渲染可空时间列：nil 输出空字符串，
// 非空时与 formatExportTime 口径一致。
func formatNullableTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatExportTime(*t)
}

// filterEventsBySeverity 原地过滤出指定严重级别的事件。
func filterEventsBySeverity(events []*model.QCEvent, sev model.Severity) []*model.QCEvent {
	out := events[:0]
	for _, ev := range events {
		if ev.Severity == sev {
			out = append(out, ev)
		}
	}
	return out
}
