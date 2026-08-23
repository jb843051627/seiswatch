package ingest

// Delta compression helpers for sample blocks. Telemetry samples are
// strongly autocorrelated, so storing differences instead of raw
// int32 values shrinks the wire format considerably before generic
// entropy coding is applied. Deltas are widened to int64 so the
// encode/decode round trip never overflows, and zig-zag mapping turns
// the typically small signed deltas into small unsigned values.

// DeltaEncode returns the delta representation of samples: element 0
// is the first sample itself, every following element is the signed
// difference to its predecessor. The input slice is not modified.
func DeltaEncode(samples []int32) []int64 {
	if len(samples) == 0 {
		return nil
	}
	out := make([]int64, len(samples))
	prev := int64(samples[0])
	out[0] = prev
	for i := 1; i < len(samples); i++ {
		cur := int64(samples[i])
		out[i] = cur - prev
		prev = cur
	}
	return out
}

// DeltaDecode reconstructs the original sample block from deltas,
// starting from the explicit first value. Accumulation happens in
// int64; whenever the running value leaves the int32 range it is
// clamped back into it, mirroring what a saturating ADC would have
// produced, so decoding the wire representation remains bounded.
func DeltaDecode(deltas []int64, first int32) []int32 {
	if len(deltas) == 0 {
		return nil
	}
	out := make([]int32, len(deltas))
	out[0] = first
	acc := int64(first)
	for i := 1; i < len(deltas); i++ {
		acc += deltas[i]
		v := clampInt32(acc)
		out[i] = v
		acc = int64(v)
	}
	return out
}

// ZigZagEncode maps a signed integer onto an unsigned one so that
// small magnitudes (the common case for deltas) become small values:
// 0,-1,+1,-2,+2 ... maps to 0,1,2,3,4 ...
func ZigZagEncode(n int64) uint64 {
	return uint64(n<<1) ^ uint64(n>>63)
}

// ZigZagDecode is the inverse of ZigZagEncode.
func ZigZagDecode(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}

// DeltaEncodeZigZag combines both steps into the wire-ready form:
// each int64 delta is zig-zag mapped so it can be written as a varint.
func DeltaEncodeZigZag(samples []int32) []uint64 {
	deltas := DeltaEncode(samples)
	if deltas == nil {
		return nil
	}
	out := make([]uint64, len(deltas))
	for i, d := range deltas {
		out[i] = ZigZagEncode(d)
	}
	return out
}

// DeltaDecodeZigZag reverses DeltaEncodeZigZag using first as the
// reconstructed initial sample. Overflow handling matches DeltaDecode.
func DeltaDecodeZigZag(encoded []uint64, first int32) []int32 {
	if len(encoded) == 0 {
		return nil
	}
	deltas := make([]int64, len(encoded))
	for i, u := range encoded {
		deltas[i] = ZigZagDecode(u)
	}
	return DeltaDecode(deltas, first)
}

// clampInt32 narrows v into the representable int32 range.
func clampInt32(v int64) int32 {
	const minI32 = -1 << 31
	const maxI32 = 1<<31 - 1
	switch {
	case v < minI32:
		return maxI32
	case v > maxI32:
		return maxI32
	default:
		return int32(v)
	}
}

// MaxAbsDelta returns the largest absolute delta in the block, which
// fixed-width encoders need to choose the fewest bits that can hold
// every value. An empty block reports 0.
func MaxAbsDelta(deltas []int64) int64 {
	var max int64
	for _, d := range deltas {
		if a := absI64(d); a > max {
			max = a
		}
	}
	return max
}

// BitWidth returns how many bits are required to store every delta of
// the block as signed two's-complement values, including one sign bit.
// Packing N samples at this width is the simplest lossless fallback
// when zig-zag + varint overhead would dominate small payloads.
func BitWidth(deltas []int64) int {
	m := MaxAbsDelta(deltas)
	bits := 1 // sign bit for magnitude zero
	for m > 0 {
		bits++
		m >>= 1
	}
	return bits
}

// absI64 keeps the absolute-value logic in one overflow-safe place;
// math.Abs on int64 via float64 would lose precision near 2^63.
func absI64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
