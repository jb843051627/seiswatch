// Package ingest decodes raw telemetry frames and computes per-frame
// statistics used by the QC rule engine.
package ingest

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// frameHeaderSize is the fixed size of the SWIF frame header in bytes:
// magic(4) + station(8) + channel(4) + start(8) + rate(8) + count(4).
const frameHeaderSize = 36

// magic is the byte sequence every SWIF frame starts with.
var magic = [4]byte{'S', 'W', 'I', 'F'}

// DecodedFrame is one decoded telemetry frame.
type DecodedFrame struct {
	StationCode string
	ChannelCode string
	Start       time.Time
	SampleRate  float64
	Samples     []int32
}

// Decode parses a big-endian SWIF binary frame into a DecodedFrame.
// Header validation (magic, length, positive finite sample rate, sample
// count cap) is delegated to ParseHeader so Decode can never emit a frame
// whose stats would degenerate into NaN via a zero sample rate.
func Decode(payload []byte) (DecodedFrame, error) {
	h, err := ParseHeader(payload)
	if err != nil {
		return DecodedFrame{}, err
	}
	want := frameHeaderSize + h.SampleCount*4
	if len(payload) < want {
		return DecodedFrame{}, fmt.Errorf("short frame: got %d bytes, want %d for %d samples", len(payload), want, h.SampleCount)
	}

	f := DecodedFrame{
		StationCode: h.StationCode,
		ChannelCode: h.ChannelCode,
		Start:       h.Start,
		SampleRate:  h.SampleRate,
		Samples:     make([]int32, h.SampleCount),
	}
	for i := range f.Samples {
		f.Samples[i] = int32(binary.BigEndian.Uint32(payload[frameHeaderSize+i*4:]))
	}
	return f, nil
}

// trimPadding strips trailing NUL bytes from a fixed-width ASCII field.
func trimPadding(b []byte) string {
	return strings.TrimRight(string(b), "\x00")
}
