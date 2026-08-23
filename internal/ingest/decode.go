// Package ingest decodes raw telemetry frames and computes per-frame
// statistics used by the QC rule engine.
package ingest

import (
	"encoding/binary"
	"fmt"
	"math"
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
func Decode(payload []byte) (DecodedFrame, error) {
	if len(payload) < frameHeaderSize {
		return DecodedFrame{}, fmt.Errorf("short frame: got %d bytes, want at least %d header bytes", len(payload), frameHeaderSize)
	}
	if string(payload[:4]) != string(magic[:]) {
		return DecodedFrame{}, fmt.Errorf("bad magic %q, want %q", payload[:4], magic[:])
	}
	sampleCount := binary.BigEndian.Uint32(payload[32:36])
	want := frameHeaderSize + int(sampleCount)*4
	if len(payload) < want {
		return DecodedFrame{}, fmt.Errorf("short frame: got %d bytes, want %d for %d samples", len(payload), want, sampleCount)
	}

	f := DecodedFrame{
		StationCode: trimPadding(payload[4:12]),
		ChannelCode: trimPadding(payload[12:16]),
		Start:       time.Unix(0, int64(binary.BigEndian.Uint64(payload[16:24]))).UTC(),
		SampleRate:  math.Float64frombits(binary.BigEndian.Uint64(payload[24:32])),
		Samples:     make([]int32, sampleCount),
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
