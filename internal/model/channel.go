package model

import "time"

// Channel status values stored in the channels table.
const (
	ChannelOpen   = "open"
	ChannelClosed = "closed"
)

// Channel is a single data stream produced by a station, identified by a
// channel code such as BHZ (broadband vertical).
type Channel struct {
	ID          int64
	StationID   int64
	Code        string
	SampleRate  float64 // samples per second
	Gain        float64
	Sensitivity float64 // counts per physical unit
	Unit        string  // e.g. "counts", "m/s^2"
	Status      string  // "open" or "closed"
	CreatedAt   time.Time
}

// Open reports whether the channel is still accepting telemetry frames.
func (c Channel) Open() bool { return c.Status == ChannelOpen }

// ExpectedDuration estimates the wall-clock span a frame of n samples
// should cover at this channel's sample rate.
func (c Channel) ExpectedDuration(samples int) time.Duration {
	if c.SampleRate <= 0 {
		return 0
	}
	return time.Duration(float64(samples) / c.SampleRate * float64(time.Second))
}
