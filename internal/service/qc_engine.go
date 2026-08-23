package service

import (
	"seiswatch/internal/model"
	"time"
)

// Finding is one rule verdict on a frame.
type Finding struct {
	Severity model.Severity
	Detail   string
}

// FrameContext carries everything a rule needs to judge the current frame.
type FrameContext struct {
	Station model.Station
	Channel model.Channel
	Frame   model.DataFrame
	History []model.DataFrame // recent frames, newest first, excluding current
	Now     time.Time
}

// Rule is a named QC check evaluated against each frame.
type Rule interface {
	ID() string
	Evaluate(fc FrameContext) []Finding
}

// QCEngine runs all registered rules and materializes QCEvents.
type QCEngine struct {
	rules []Rule
}

func NewQCEngine() *QCEngine {
	return &QCEngine{}
}

func (e *QCEngine) Register(r Rule) {
	e.rules = append(e.rules, r)
}

func (e *QCEngine) Rules() []Rule {
	return e.rules
}

// Evaluate returns one QCEvent per finding.
func (e *QCEngine) Evaluate(fc FrameContext) []model.QCEvent {
	var events []model.QCEvent
	for _, r := range e.rules {
		for _, f := range r.Evaluate(fc) {
			events = append(events, model.QCEvent{
				StationID:  fc.Station.ID,
				ChannelID:  fc.Channel.ID,
				RuleID:     r.ID(),
				Severity:   f.Severity,
				Detail:     f.Detail,
				Status:     model.QCOpen,
				DetectedAt: fc.Now,
			})
		}
	}
	return events
}
