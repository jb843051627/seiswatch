package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"seiswatch/internal/model"
)

// CalibrationStore persists scheduled calibration jobs.
type CalibrationStore struct {
	db *sql.DB
}

const calibCols = `id, station_id, kind, scheduled_at, window_minutes, state, result_metrics, started_at, finished_at, created_at`

func scanCalib(rows interface{ Scan(...any) error }) (*model.CalibrationJob, error) {
	var j model.CalibrationJob
	var raw string
	var started, finished sql.NullTime
	if err := rows.Scan(&j.ID, &j.StationID, &j.Kind, &j.ScheduledAt, &j.WindowMinutes,
		&j.State, &raw, &started, &finished, &j.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &j.ResultMetrics)
	}
	if started.Valid {
		t := started.Time
		j.StartedAt = &t
	}
	if finished.Valid {
		t := finished.Time
		j.FinishedAt = &t
	}
	return &j, nil
}

func metricsJSON(metrics map[string]float64) string {
	if metrics == nil {
		return "{}"
	}
	b, err := json.Marshal(metrics)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func (s *CalibrationStore) Create(ctx context.Context, j *model.CalibrationJob) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
INSERT INTO calibration_jobs (station_id, kind, scheduled_at, window_minutes, state, result_metrics, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		j.StationID, j.Kind, j.ScheduledAt.UTC(), j.WindowMinutes, model.CalibPending,
		metricsJSON(j.ResultMetrics), time.Now().UTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *CalibrationStore) GetByID(ctx context.Context, id int64) (*model.CalibrationJob, error) {
	return scanCalib(s.db.QueryRowContext(ctx,
		`SELECT `+calibCols+` FROM calibration_jobs WHERE id = ?`, id))
}

func (s *CalibrationStore) DueBefore(ctx context.Context, now time.Time) ([]*model.CalibrationJob, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+calibCols+` FROM calibration_jobs WHERE state = ? AND scheduled_at <= ? ORDER BY scheduled_at`,
		model.CalibPending, now.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.CalibrationJob
	for rows.Next() {
		j, err := scanCalib(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *CalibrationStore) UpdateState(ctx context.Context, id int64, state model.CalibState) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE calibration_jobs SET state = ? WHERE id = ?`, state, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("calibration job %d not found", id)
	}
	return nil
}

func (s *CalibrationStore) MarkStarted(ctx context.Context, id int64, startedAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE calibration_jobs SET state = ?, started_at = ? WHERE id = ?`,
		model.CalibRunning, startedAt.UTC(), id)
	return err
}

func (s *CalibrationStore) FinishWithResult(ctx context.Context, id int64, state model.CalibState,
	metrics map[string]float64, finishedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE calibration_jobs SET state = ?, result_metrics = ?, finished_at = ? WHERE id = ?`,
		state, metricsJSON(metrics), finishedAt.UTC(), id)
	return err
}
