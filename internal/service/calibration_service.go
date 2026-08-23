package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

// CalibrationService manages the calibration job state machine.
type CalibrationService struct {
	db *store.DB
}

// ErrInvalidState marks a calibration job state-machine violation;
var ErrInvalidState = errors.New("calibration job invalid state")

func NewCalibrationService(db *store.DB) *CalibrationService {
	return &CalibrationService{db: db}
}

// Schedule creates a pending job; scheduling in the past is allowed.
func (s *CalibrationService) Schedule(stationID int64, kind model.CalibrationKind, at time.Time, windowMin int) (*model.CalibrationJob, error) {
	if windowMin <= 0 {
		return nil, fmt.Errorf("window minutes must be positive, got %d", windowMin)
	}
	ctx := context.Background()
	job := &model.CalibrationJob{
		StationID:     stationID,
		Kind:          kind,
		ScheduledAt:   at.UTC(),
		WindowMinutes: windowMin,
		State:         model.CalibPending,
	}
	id, err := s.db.Calibrations.Create(ctx, job)
	if err != nil {
		return nil, err
	}
	return s.db.Calibrations.GetByID(ctx, id)
}

// Start transitions pending -> running only.
func (s *CalibrationService) Start(id int64) (*model.CalibrationJob, error) {
	job, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if job.State != model.CalibPending {
		return nil, fmt.Errorf("job %d in state %s cannot start: %w", id, job.State, ErrInvalidState)
	}
	now := time.Now().UTC()
	if err := s.db.Calibrations.MarkStarted(context.Background(), id, now); err != nil {
		return nil, err
	}
	return s.get(id)
}

// Complete transitions running -> succeeded with result metrics.
func (s *CalibrationService) Complete(id int64, metrics map[string]float64) (*model.CalibrationJob, error) {
	job, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if job.State != model.CalibRunning {
		return nil, fmt.Errorf("job %d in state %s cannot complete: %w", id, job.State, ErrInvalidState)
	}
	if err := s.db.Calibrations.FinishWithResult(context.Background(), id, model.CalibSucceeded, metrics, time.Now().UTC()); err != nil {
		return nil, err
	}
	return s.get(id)
}

// Fail transitions running -> failed.
func (s *CalibrationService) Fail(id int64) (*model.CalibrationJob, error) {
	job, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if job.State != model.CalibRunning {
		return nil, fmt.Errorf("job %d in state %s cannot fail: %w", id, job.State, ErrInvalidState)
	}
	if err := s.db.Calibrations.FinishWithResult(context.Background(), id, model.CalibFailed, nil, time.Now().UTC()); err != nil {
		return nil, err
	}
	return s.get(id)
}

// ExpireOverdue expires pending jobs whose window elapsed before now.
func (s *CalibrationService) ExpireOverdue(now time.Time) (int, error) {
	jobs, err := s.db.Calibrations.DueBefore(context.Background(), now)
	if err != nil {
		return 0, err
	}
	expired := 0
	for _, j := range jobs {
		if j.State != model.CalibPending {
			continue
		}
		if err := s.db.Calibrations.UpdateState(context.Background(), j.ID, model.CalibExpired); err != nil {
			return expired, err
		}
		expired++
	}
	return expired, nil
}

// List returns up to limit jobs ordered by scheduled_at descending.
// Uses raw SQL because the store method set has no calibration list query.
func (s *CalibrationService) List(limit int) ([]*model.CalibrationJob, error) {
	rows, err := s.db.SQL().QueryContext(context.Background(), `
SELECT id, station_id, kind, scheduled_at, window_minutes, state,
       result_metrics, started_at, finished_at, created_at
FROM calibration_jobs
ORDER BY scheduled_at DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*model.CalibrationJob
	for rows.Next() {
		var (
			j        model.CalibrationJob
			metrics  []byte
			started  sql.NullTime
			finished sql.NullTime
		)
		if err := rows.Scan(&j.ID, &j.StationID, &j.Kind, &j.ScheduledAt, &j.WindowMinutes,
			&j.State, &metrics, &started, &finished, &j.CreatedAt); err != nil {
			return nil, err
		}
		if len(metrics) > 0 {
			m := make(map[string]float64)
			if err := json.Unmarshal(metrics, &m); err != nil {
				return nil, err
			}
			j.ResultMetrics = m
		}
		if started.Valid {
			v := started.Time
			j.StartedAt = &v
		}
		if finished.Valid {
			v := finished.Time
			j.FinishedAt = &v
		}
		jobs = append(jobs, &j)
	}
	return jobs, rows.Err()
}

func (s *CalibrationService) get(id int64) (*model.CalibrationJob, error) {
	job, err := s.db.Calibrations.GetByID(context.Background(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("calibration job %d not found", id)
		}
		return nil, err
	}
	return job, nil
}
