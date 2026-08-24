// 跨通道事件关联：把时间上贴近的 QC 事件按台站聚簇，并判断
// 一个簇是否更像“仪器共性故障”（多通道同时 critical）而不是
// 单通道偶发问题。聚类是纯内存计算，服务本身只持有 DB 句柄
// 以便后续扩展按台站补齐元数据。
package service

import (
	"sort"
	"time"

	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

// CorrelationService 对 QC 事件做跨通道关联分析。
type CorrelationService struct {
	db *store.DB
}

// NewCorrelationService 构造关联分析服务。
func NewCorrelationService(db *store.DB) *CorrelationService {
	return &CorrelationService{db: db}
}

// CorrelatedCluster 是同一台站在 window 时间跨度内聚到一起的事件集合。
// Window 记录生成该簇时使用的时间阈值，便于上层展示与复核。
type CorrelatedCluster struct {
	StationID int64
	Window    time.Duration
	Events    []*model.QCEvent
}

// ClusterByStation 把事件按台站分组后，在组内对 DetectedAt 排序并
// 线性扫描：相邻两事件的间隔 <= window 就并入当前簇，否则开启新簇。
// 返回的簇按 (StationID, 首事件时间) 升序排列；nil 事件被忽略。
func (s *CorrelationService) ClusterByStation(events []*model.QCEvent, window time.Duration) []CorrelatedCluster {
	sorted := nonNil(events)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].StationID != sorted[j].StationID {
			return sorted[i].StationID < sorted[j].StationID
		}
		return sorted[i].DetectedAt.Before(sorted[j].DetectedAt)
	})
	clusters := make([]CorrelatedCluster, 0, len(sorted)/2+1)
	for _, ev := range sorted {
		if n := len(clusters); n > 0 && extends(&clusters[n-1], ev, window) {
			clusters[n-1].Events = append(clusters[n-1].Events, ev)
			continue
		}
		clusters = append(clusters, CorrelatedCluster{
			StationID: ev.StationID,
			Window:    window,
			Events:    []*model.QCEvent{ev},
		})
	}
	return clusters
}

// extends 报告 ev 是否应并入 cluster：同台站且与簇尾事件的时间差
// 不超过 window。采用链式比较而非簇首比较，使长时间连续抖动不会
// 被无限拉长成一个簇。
func extends(cluster *CorrelatedCluster, ev *model.QCEvent, window time.Duration) bool {
	if cluster.StationID != ev.StationID {
		return false
	}
	tail := cluster.Events[len(cluster.Events)-1]
	return ev.DetectedAt.Sub(tail.DetectedAt) <= window
}

// LikelyCommonCause 判断一个簇是否提示共性故障：簇内涉及至少两个
// 不同通道、且全部事件都是 critical。此时问题大概率出在共享硬件
// （数采、供电、GPS）而非单一传感器通道。
func (s *CorrelationService) LikelyCommonCause(cluster CorrelatedCluster) bool {
	if len(cluster.Events) < 2 || !allCritical(cluster.Events) {
		return false
	}
	return len(distinctChannels(cluster.Events)) >= 2
}

// OpenClusters 只对未关闭（open/ack）的事件做聚簇，方便值班页
// 忽略已经 resolve 的历史噪声。
func (s *CorrelationService) OpenClusters(events []*model.QCEvent, window time.Duration) []CorrelatedCluster {
	open := make([]*model.QCEvent, 0, len(events))
	for _, ev := range events {
		if ev != nil && ev.Open() {
			open = append(open, ev)
		}
	}
	return s.ClusterByStation(open, window)
}

// CommonCauseClusters 是 LikelyCommonCause 的批量形式：返回输入中
// 所有疑似共性故障的簇，保持 ClusterByStation 的输出顺序。
func (s *CorrelationService) CommonCauseClusters(clusters []CorrelatedCluster) []CorrelatedCluster {
	out := make([]CorrelatedCluster, 0, len(clusters))
	for _, c := range clusters {
		if s.LikelyCommonCause(c) {
			out = append(out, c)
		}
	}
	return out
}

// Span 返回簇内首末事件的时间跨度；单事件簇跨度为 0。
func (c CorrelatedCluster) Span() time.Duration {
	if len(c.Events) < 2 {
		return 0
	}
	first := c.Events[0].DetectedAt
	last := c.Events[len(c.Events)-1].DetectedAt
	return last.Sub(first).Abs()
}

// Channels 返回簇内去重后的通道 ID，升序排列。
func (c CorrelatedCluster) Channels() []int64 {
	ids := distinctChannels(c.Events)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// CriticalCount 统计簇内 critical 级事件数量。
func (c CorrelatedCluster) CriticalCount() int {
	n := 0
	for _, ev := range c.Events {
		if ev.Severity == model.SeverityCritical {
			n++
		}
	}
	return n
}

// RuleIDs 返回簇内去重后的规则 ID，按字典序排列，用于快速判断
// 一个簇是单规则反复触发还是多规则同时命中。
func (c CorrelatedCluster) RuleIDs() []string {
	seen := make(map[string]bool, len(c.Events))
	var ids []string
	for _, ev := range c.Events {
		if !seen[ev.RuleID] {
			seen[ev.RuleID] = true
			ids = append(ids, ev.RuleID)
		}
	}
	sort.Strings(ids)
	return ids
}

// ClusterSummary 是簇的紧凑摘要，供报表与前端列表直接渲染。
type ClusterSummary struct {
	StationID     int64
	Window        time.Duration
	EventCount    int
	Channels      []int64
	RuleIDs       []string
	CriticalCount int
	CommonCause   bool
	Span          time.Duration
}

// Summarize 把聚簇结果压缩为摘要列表：按事件数降序、同数量按
// 台站 ID 升序排列，突出最可疑的台站；CommonCause 直接复用
// LikelyCommonCause 的判定口径。
func (s *CorrelationService) Summarize(clusters []CorrelatedCluster) []ClusterSummary {
	out := make([]ClusterSummary, 0, len(clusters))
	for _, c := range clusters {
		out = append(out, ClusterSummary{
			StationID:     c.StationID,
			Window:        c.Window,
			EventCount:    len(c.Events),
			Channels:      c.Channels(),
			RuleIDs:       c.RuleIDs(),
			CriticalCount: c.CriticalCount(),
			CommonCause:   s.LikelyCommonCause(c),
			Span:          c.Span(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EventCount != out[j].EventCount {
			return out[i].EventCount > out[j].EventCount
		}
		return out[i].StationID < out[j].StationID
	})
	return out
}

// nonNil 过滤 nil 元素并复制到底层数组，避免修改调用方的切片顺序。
func nonNil(events []*model.QCEvent) []*model.QCEvent {
	out := make([]*model.QCEvent, 0, len(events))
	for _, ev := range events {
		if ev != nil {
			out = append(out, ev)
		}
	}
	return out
}

// distinctChannels 统计事件涉及的不同 ChannelID 数量。
func distinctChannels(events []*model.QCEvent) []int64 {
	seen := make(map[int64]bool, len(events))
	var ids []int64
	for _, ev := range events {
		if !seen[ev.ChannelID] {
			seen[ev.ChannelID] = true
			ids = append(ids, ev.ChannelID)
		}
	}
	return ids
}

// allCritical 报告是否每个事件都是 critical 级别。
func allCritical(events []*model.QCEvent) bool {
	for _, ev := range events {
		if ev.Severity != model.SeverityCritical {
			return false
		}
	}
	return true
}
