// SWIF 帧构造器：按 Decode 的二进制布局反向编码，用于模拟数据源
// 与端到端联调。布局为 magic(4) + station(8) + channel(4) +
// start unix-nano BE(8) + sampleRate float64 bits BE(8) +
// sampleCount uint32 BE(4)，其后紧跟 big-endian int32 样本序列。
package ingest

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"
)

const (
	stationFieldLen = 8 // 台站编码固定宽度，右侧 \x00 填充
	channelFieldLen = 4 // 通道编码固定宽度，右侧 \x00 填充
)

// Builder 将 Header 与样本序列编码为完整的 SWIF 二进制帧。
// 零值即可使用，不携带任何状态。
type Builder struct{}

// Build 按 wire 格式编码一帧。要求：
//   - StationCode 非空且不超过 8 字节；
//   - ChannelCode 非空且不超过 4 字节；
//   - SampleRate 为正且有限的浮点数（与 ParseHeader 的校验对齐）；
//   - len(samples) 必须与 h.SampleCount 完全一致。
func (b Builder) Build(h Header, samples []int32) ([]byte, error) {
	if err := b.validate(h); err != nil {
		return nil, err
	}
	if len(samples) != h.SampleCount {
		return nil, fmt.Errorf("sample count mismatch: got %d values, header declares %d",
			len(samples), h.SampleCount)
	}
	frame := make([]byte, h.FrameSize())
	copy(frame[0:4], magic[:])
	if err := encodeFixedField(frame[4:12], h.StationCode, stationFieldLen); err != nil {
		return nil, err
	}
	if err := encodeFixedField(frame[12:16], h.ChannelCode, channelFieldLen); err != nil {
		return nil, err
	}
	binary.BigEndian.PutUint64(frame[16:24], uint64(h.Start.UnixNano()))
	binary.BigEndian.PutUint64(frame[24:32], math.Float64bits(h.SampleRate))
	binary.BigEndian.PutUint32(frame[32:36], uint32(h.SampleCount))
	off := frameHeaderSize
	for _, s := range samples {
		binary.BigEndian.PutUint32(frame[off:], uint32(s))
		off += 4
	}
	return frame, nil
}

// validate 复核 Build 的头部约束，错误信息同时覆盖编码侧与解析侧，
// 保证 Builder 产出的任何帧都能通过 ParseHeader 的同名字段检查。
func (b Builder) validate(h Header) error {
	if strings.TrimSpace(h.StationCode) == "" {
		return fmt.Errorf("empty station code")
	}
	if len(h.StationCode) > stationFieldLen {
		return fmt.Errorf("station code %q exceeds %d bytes", h.StationCode, stationFieldLen)
	}
	if strings.TrimSpace(h.ChannelCode) == "" {
		return fmt.Errorf("empty channel code")
	}
	if len(h.ChannelCode) > channelFieldLen {
		return fmt.Errorf("channel code %q exceeds %d bytes", h.ChannelCode, channelFieldLen)
	}
	if math.IsNaN(h.SampleRate) || math.IsInf(h.SampleRate, 0) || h.SampleRate <= 0 {
		return fmt.Errorf("invalid sample rate %g, want > 0", h.SampleRate)
	}
	if h.SampleCount < 0 || h.SampleCount > maxSampleCount {
		return fmt.Errorf("sample count %d outside [0, %d]", h.SampleCount, maxSampleCount)
	}
	return nil
}

// BuildSynthetic 用确定性伪随机信号生成一帧合成地震数据：
// 主频正弦叠加高斯噪声。相同 seed 与参数必然得到逐字节相同的帧，
// 因此适合作为回归测试与联调环境的稳定数据源。
func (b Builder) BuildSynthetic(station, channel string, start time.Time, rate float64, n int, seed int64) ([]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("sample count %d must be positive", n)
	}
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return nil, fmt.Errorf("invalid sample rate %g, want > 0", rate)
	}
	sig := newSynthSignal(seed, rate)
	h := Header{
		StationCode: station,
		ChannelCode: channel,
		Start:       start.UTC(),
		SampleRate:  rate,
		SampleCount: n,
	}
	return b.Build(h, sig.samples(n))
}

// Validate 导出头部字段校验，供 API 层在真正编码前快速拒绝
// 非法的台站/通道/采样率/样本数组合，错误信息与 Build 一致。
func (Builder) Validate(h Header) error {
	return Builder{}.validate(h)
}

// SyntheticSpec 描述一帧合成数据的完整生成参数。
type SyntheticSpec struct {
	Station string
	Channel string
	Start   time.Time // 为零值时自动衔接上一帧的结束时刻
	Rate    float64
	Samples int
	Seed    int64
}

// BuildAll 批量生成合成帧并保证时间上首尾相接：Start 为零值的
// 帧从上一帧 EndTime 继续，从而拼出一段连续遥测流，供批量解码
// 与断流检测逻辑联调使用。任一帧失败则整体报错并中止。
func (b Builder) BuildAll(specs []SyntheticSpec) ([][]byte, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("no synthetic specs given")
	}
	frames := make([][]byte, 0, len(specs))
	var next time.Time
	for i, sp := range specs {
		start := sp.Start
		if start.IsZero() {
			if i == 0 {
				return nil, fmt.Errorf("spec %d: first frame needs an explicit start", i)
			}
			start = next
			if start.IsZero() {
				return nil, fmt.Errorf("spec %d: no start to continue from", i)
			}
		}
		frame, err := b.BuildSynthetic(sp.Station, sp.Channel, start, sp.Rate, sp.Samples, sp.Seed)
		if err != nil {
			return nil, fmt.Errorf("spec %d (%s/%s): %w", i, sp.Station, sp.Channel, err)
		}
		frames = append(frames, frame)
		next = Header{
			Start:       start.UTC(),
			SampleRate:  sp.Rate,
			SampleCount: sp.Samples,
		}.EndTime()
	}
	return frames, nil
}

// synthSignal 描述一段确定性合成波形：固定振幅的主频正弦加高斯噪声，
// 相位从 0 开始按采样序号推进。
type synthSignal struct {
	rng       *rand.Rand
	omega     float64 // 每个采样点的角频率增量（rad）
	amplitude int32   // 正弦主振幅，单位 counts
	noise     int32   // 高斯噪声标准差，单位 counts
}

// newSynthSignal 从 seed 派生主频：在常见短周期地震仪通带内取
// 2~8 Hz 的随机值，再折算为每采样点的角频率增量。
func newSynthSignal(seed int64, rate float64) *synthSignal {
	rng := rand.New(rand.NewSource(seed))
	freqHz := 2.0 + 6.0*rng.Float64()
	return &synthSignal{
		rng:       rng,
		omega:     2 * math.Pi * freqHz / rate,
		amplitude: 50000,
		noise:     300,
	}
}

// samples 渲染前 n 个采样点；调用方保证 n > 0。
func (s *synthSignal) samples(n int) []int32 {
	out := make([]int32, n)
	for i := range out {
		v := float64(s.amplitude)*math.Sin(float64(i)*s.omega) +
			float64(s.noise)*s.rng.NormFloat64()
		out[i] = clampInt32(int64(math.Round(v)))
	}
	return out
}

// encodeFixedField 把 ASCII 字段左对齐写入 dst，剩余字节补 \x00；
// 超长直接报错而不是静默截断，避免生成无法往返解码的帧。
func encodeFixedField(dst []byte, value string, width int) error {
	if len(value) > width {
		return fmt.Errorf("field %q exceeds fixed width %d", value, width)
	}
	if len(value) < len(dst) {
		for i := len(value); i < len(dst); i++ {
			dst[i] = 0
		}
	}
	copy(dst, value)
	return nil
}
