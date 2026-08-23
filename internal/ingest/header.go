// Header parsing for SWIF telemetry frames. The header is the fixed
// 36-byte control block every frame starts with; sample data follows
// immediately after as big-endian int32 values.
package ingest

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// HeaderSize is the exported byte size of the SWIF frame header:
// magic(4) + station(8) + channel(4) + start(8) + rate(8) + count(4).
const HeaderSize = frameHeaderSize

// Header carries the parsed fields of one SWIF frame header.
type Header struct {
	StationCode string
	ChannelCode string
	Start       time.Time
	SampleRate  float64 // samples per second, must be > 0
	SampleCount int
}

// maxSampleCount bounds the sample count accepted in a header. A
// corrupt or hostile count would otherwise make FrameSize report a
// value large enough to break stream slicing; 2^24 samples is over an
// hour of data even at the fastest seismic rates, far above anything
// a real frame carries.
const maxSampleCount = 1 << 24

func validateSampleRate(rate float64) error {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return fmt.Errorf("invalid sample rate %g, want > 0", rate)
	}
	return nil
}

// ParseHeader validates and decodes the fixed-size header at the start
// of payload. It checks the magic bytes, the minimum length and that
// the sample rate is a positive finite number. Payload may be longer
// than the header (the rest being sample data); only short payloads
// are rejected here.
func ParseHeader(payload []byte) (Header, error) {
	if len(payload) < frameHeaderSize {
		return Header{}, fmt.Errorf("short header: got %d bytes, want at least %d", len(payload), frameHeaderSize)
	}
	if string(payload[:4]) != string(magic[:]) {
		return Header{}, fmt.Errorf("bad magic %q, want %q", payload[:4], magic[:])
	}
	h := Header{
		StationCode: trimPadding(payload[4:12]),
		ChannelCode: trimPadding(payload[12:16]),
		Start:       time.Unix(0, int64(binary.BigEndian.Uint64(payload[16:24]))).UTC(),
		SampleRate:  math.Float64frombits(binary.BigEndian.Uint64(payload[24:32])),
		SampleCount: int(binary.BigEndian.Uint32(payload[32:36])),
	}
	if err := validateSampleRate(h.SampleRate); err != nil {
		return Header{}, err
	}
	if h.SampleCount > maxSampleCount {
		return Header{}, fmt.Errorf("sample count %d exceeds limit %d", h.SampleCount, maxSampleCount)
	}
	if h.StationCode == "" {
		return Header{}, fmt.Errorf("empty station code")
	}
	if h.ChannelCode == "" {
		return Header{}, fmt.Errorf("empty channel code")
	}
	return h, nil
}

// EndTime returns the moment the last sample of the frame was taken,
// i.e. Start plus SampleCount divided by SampleRate seconds.
func (h Header) EndTime() time.Time {
	return h.Start.Add(time.Duration(float64(h.SampleCount) / h.SampleRate * float64(time.Second)))
}

// FrameSize returns the full wire size of the frame this header
// describes, including its sample payload. Batch readers use it to
// slice concatenated frames out of a single byte stream.
func (h Header) FrameSize() int {
	return frameHeaderSize + h.SampleCount*4
}

// Duration returns the time span covered by the frame's samples.
func (h Header) Duration() time.Duration {
	return h.EndTime().Sub(h.Start)
}

// SamplePeriod returns the interval between two consecutive samples.
// Timing checks use it to decide whether a small start-time drift can
// be absorbed by the sampling grid itself.
func (h Header) SamplePeriod() time.Duration {
	return time.Duration(float64(time.Second) / h.SampleRate)
}

// String renders the header as a compact one-line identity, suitable
// for logs: "GD03/BHZ@100Hz 2026-08-23T00:00:00Z n=6000".
func (h Header) String() string {
	return fmt.Sprintf("%s/%s@%gg %s n=%d",
		h.StationCode, h.ChannelCode, h.SampleRate,
		h.Start.UTC().Format(time.RFC3339), h.SampleCount)
}
