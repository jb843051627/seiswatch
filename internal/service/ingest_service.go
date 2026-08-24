// Package service implements the application-level services of seiswatch:
// telemetry ingestion, QC rule evaluation, calibration scheduling,
// maintenance windows, alert escalation, health scoring and reporting.
package service

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"seiswatch/internal/ingest"
	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

// IngestService decodes submitted payloads and persists one aggregated
// DataFrame per frame, optionally running QC rules via a QCEngine.
type IngestService struct {
	db     *store.DB
	engine *QCEngine
	alerts *AlertService

	queue    chan ingest.DecodedFrame
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
	stopOnce sync.Once

	mu          sync.RWMutex
	latestStats map[int64]model.DataFrame // key: channelID
}

// NewIngestService creates the service; engine stays nil until SetEngine.
func NewIngestService(db *store.DB, queueSize int) *IngestService {
	if queueSize <= 0 {
		queueSize = 64
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &IngestService{
		db:          db,
		alerts:      NewAlertService(db),
		queue:       make(chan ingest.DecodedFrame, queueSize),
		ctx:         ctx,
		cancel:      cancel,
		latestStats: make(map[int64]model.DataFrame),
	}
}

// SetEngine attaches the QC rule engine; when unset, evaluation is skipped.
func (s *IngestService) SetEngine(e *QCEngine) {
	s.engine = e
}

// Start launches the single worker goroutine.
func (s *IngestService) Start(_ context.Context) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-s.ctx.Done():
				return
			case df := <-s.queue:
				s.processFrame(df)
			}
		}
	}()
}

// Stop cancels the worker and waits for it to exit.
func (s *IngestService) Stop() {
	s.stopOnce.Do(func() {
		s.cancel()
		s.wg.Wait()
	})
}

// Submit decodes the payload and enqueues it without blocking.
func (s *IngestService) Submit(payload []byte) error {
	df, err := ingest.Decode(payload)
	if err != nil {
		return err
	}
	select {
	case s.queue <- df:
		return nil
	default:
		return errors.New("ingest queue full")
	}
}

// Snapshot returns the most recent persisted frame for a channel.
func (s *IngestService) Snapshot(channelID int64) (model.DataFrame, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	df, ok := s.latestStats[channelID]
	return df, ok
}

func (s *IngestService) processFrame(df ingest.DecodedFrame) {
	ctx := s.ctx
	now := time.Now().UTC()

	station, err := s.db.Stations.GetByCode(ctx, df.StationCode)
	if err != nil {
		log.Printf("ingest: resolve station %q: %v", df.StationCode, err)
		return
	}
	channel, err := s.db.Channels.FindByCode(ctx, station.ID, df.ChannelCode)
	if err != nil {
		log.Printf("ingest: resolve channel %q@%q: %v", df.ChannelCode, df.StationCode, err)
		return
	}
	if channel.Status != model.ChannelOpen {
		log.Printf("ingest: channel %d unavailable (%s)", channel.ID, df.ChannelCode)
		return
	}
	history, err := s.db.Frames.RecentByChannel(ctx, channel.ID, 1)
	if err != nil {
		log.Printf("ingest: recent frames for channel %d: %v", channel.ID, err)
		return
	}

	var gapMs int64
	if len(history) > 1 {
		gapMs = ingest.GapMilliseconds(history[0].EndTime, df.Start)
	}
	stats := ingest.ComputeStats(df.Samples)
	end := df.Start
	if df.SampleRate > 0 {
		end = df.Start.Add(time.Duration(float64(len(df.Samples)) / df.SampleRate * float64(time.Second)))
	}

	frame := &model.DataFrame{
		ChannelID:   channel.ID,
		StartTime:   df.Start,
		EndTime:     end,
		SampleCount: len(df.Samples),
		Min:         stats.Min,
		Max:         stats.Max,
		Mean:        stats.Mean,
		RMS:         stats.RMS,
		GapBeforeMs: gapMs,
		ReceivedAt:  now,
	}
	frameID, err := s.db.Frames.Insert(ctx, frame)
	if err != nil {
		log.Printf("ingest: insert frame for channel %d: %v", channel.ID, err)
		return
	}
	frame.ID = frameID

	s.mu.Lock()
	s.latestStats[channel.ID] = *frame
	s.mu.Unlock()

	if s.engine == nil {
		return
	}
	fc := FrameContext{
		Station: *station,
		Channel: *channel,
		Frame:   *frame,
		History: history,
		Now:     now,
	}
	for _, ev := range s.engine.Evaluate(fc) {
		ev.FrameID = frameID
		if _, err := s.db.QCEvents.Create(ctx, &ev); err != nil {
			log.Printf("ingest: create QC event rule=%s: %v", ev.RuleID, err)
			continue
		}
		if ev.Severity != model.SeverityCritical {
			continue
		}
		suppressed, err := s.db.Maintenance.ActiveAt(ctx, station.ID, now)
		if err != nil {
			log.Printf("ingest: check maintenance suppression for station %d: %v", station.ID, err)
			suppressed = false
		}
		if _, err := s.alerts.Escalate(ev, suppressed); err != nil {
			log.Printf("ingest: escalate alert rule=%s: %v", ev.RuleID, err)
		}
	}
}
