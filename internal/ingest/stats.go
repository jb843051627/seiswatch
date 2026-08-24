package ingest

import (
	"math"
	"time"
)

// Stats holds aggregate statistics for one frame's samples.
type Stats struct {
	Min, Max, Mean, RMS float64
}

// ComputeStats computes min, max, mean and RMS over the samples in float64.
func ComputeStats(samples []int32) Stats {
	if len(samples) == 0 {
		return Stats{}
	}
	var sum, sumSq float64
	min, max := float64(samples[0]), float64(samples[0])
	for _, s := range samples {
		v := float64(s)
		sum += v
		sumSq += v * v
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	n := float64(len(samples))
	return Stats{
		Min:  min,
		Max:  max,
		Mean: sum / n,
		RMS:  math.Sqrt(sumSq / n),
	}
}

// GapMilliseconds returns the gap in milliseconds between the previous frame
// end and the next frame start, or 0 if start is not after prevEnd.
func GapMilliseconds(prevEnd, start time.Time) int64 {
	if !start.After(prevEnd) {
		return 0
	}
	return start.Sub(prevEnd).Milliseconds()
}
