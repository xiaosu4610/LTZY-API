// Package metrics 进程级运行指标（LTZY-API 新增，D3）。
//
// 设计目标：给 /metrics 端点提供 Prometheus 文本格式（text/plain; version=0.0.4），
// 供 Prometheus / Grafana / AI Agent 直接抓取，无需任何第三方依赖。
//
// 采集面（当前）：
//   - 中继请求：总数 / 成功数 / 失败数（按渠道分标签）
//   - 中继配额消耗：累计扣费额度（按渠道分标签）
//   - 渠道健康度：滑动窗口成功率与采样数（复用 monitor 的内存窗口）
//   - 进程基础信息：版本 / 启动时间 / Go 运行时指标
//
// 一期做进程内存计数器（单实例部署足够）；多实例共享指标（D8）后续接入 Redis。
package metrics

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/config"
)

var (
	relayMutex sync.RWMutex
	// 按渠道聚合的中继指标：channelId -> 指标。key 0 表示「渠道未定」（请求在选渠道前失败）
	relayByChannel = make(map[int]*relayCounters)
	startTime      = time.Now().Unix()
)

type relayCounters struct {
	total    int64
	success  int64
	fail     int64
	quota    int64
	lastFail string // 最近一次失败原因（限长，避免内存被恶意撑爆）
}

// RecordRelay 记录一次中继请求结果。
// channelId 为 0 表示请求在渠道分配前就失败（鉴权/限流/无可用渠道）。
func RecordRelay(channelId int, success bool) {
	relayMutex.Lock()
	defer relayMutex.Unlock()
	c, ok := relayByChannel[channelId]
	if !ok {
		c = &relayCounters{}
		relayByChannel[channelId] = c
	}
	c.total++
	if success {
		c.success++
	} else {
		c.fail++
	}
}

// RecordRelayQuota 累计一次中继请求消耗的额度（结算时调用，可能为负=退还预扣）。
func RecordRelayQuota(channelId int, quota int64) {
	relayMutex.Lock()
	defer relayMutex.Unlock()
	c, ok := relayByChannel[channelId]
	if !ok {
		c = &relayCounters{}
		relayByChannel[channelId] = c
	}
	c.quota += quota
}

// SetRelayLastFail 记录最近一次失败原因（截断到 120 字节）。
func SetRelayLastFail(channelId int, reason string) {
	if len(reason) > 120 {
		reason = reason[:120]
	}
	relayMutex.Lock()
	defer relayMutex.Unlock()
	c, ok := relayByChannel[channelId]
	if !ok {
		c = &relayCounters{}
		relayByChannel[channelId] = c
	}
	c.lastFail = reason
}

// SnapshotRelay 返回当前按渠道聚合的指标快照（channelId -> total/success/fail/quota），并发安全。
func SnapshotRelay() map[int]map[string]int64 {
	relayMutex.RLock()
	defer relayMutex.RUnlock()
	out := make(map[int]map[string]int64, len(relayByChannel))
	for id, c := range relayByChannel {
		out[id] = map[string]int64{
			"total":   c.total,
			"success": c.success,
			"fail":    c.fail,
			"quota":   c.quota,
		}
	}
	return out
}

// GetRelayLastFail 返回某渠道最近一次失败原因。
func GetRelayLastFail(channelId int) string {
	relayMutex.RLock()
	defer relayMutex.RUnlock()
	if c, ok := relayByChannel[channelId]; ok {
		return c.lastFail
	}
	return ""
}

// RenderPrometheus 输出 Prometheus 文本格式（ exposition format v0.0.4）。
func RenderPrometheus() string {
	var b strings.Builder
	writeHelp := func(name, help string) {
		fmt.Fprintf(&b, "# HELP %s %s\n", name, help)
		fmt.Fprintf(&b, "# TYPE %s counter\n", name)
	}
	// 版本信息（gauge）
	fmt.Fprintf(&b, "# HELP ltzy_build_info 构建版本信息\n")
	fmt.Fprintf(&b, "# TYPE ltzy_build_info gauge\n")
	fmt.Fprintf(&b, "ltzy_build_info{version=\"%s\"} 1\n", common.Version)
	// 进程运行秒数
	fmt.Fprintf(&b, "# HELP ltzy_uptime_seconds 进程已运行秒数\n")
	fmt.Fprintf(&b, "# TYPE ltzy_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "ltzy_uptime_seconds %d\n", time.Now().Unix()-startTime)
	// 中继指标（按渠道分标签，固定顺序输出保证 diff 友好）
	writeHelp("ltzy_relay_requests_total", "中继请求总数")
	writeHelp("ltzy_relay_success_total", "中继成功总数")
	writeHelp("ltzy_relay_fail_total", "中继失败总数")
	writeHelp("ltzy_relay_quota_total", "中继累计消耗额度（1 USD = 500000 额度）")
	snapshot := SnapshotRelay()
	ids := make([]int, 0, len(snapshot))
	for id := range snapshot {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		c := snapshot[id]
		fmt.Fprintf(&b, "ltzy_relay_requests_total{channel=\"%d\"} %d\n", id, c["total"])
	}
	for _, id := range ids {
		c := snapshot[id]
		fmt.Fprintf(&b, "ltzy_relay_success_total{channel=\"%d\"} %d\n", id, c["success"])
	}
	for _, id := range ids {
		c := snapshot[id]
		fmt.Fprintf(&b, "ltzy_relay_fail_total{channel=\"%d\"} %d\n", id, c["fail"])
	}
	for _, id := range ids {
		c := snapshot[id]
		fmt.Fprintf(&b, "ltzy_relay_quota_total{channel=\"%d\"} %d\n", id, c["quota"])
	}
	// Go 运行时
	fmt.Fprintf(&b, "# HELP ltzy_go_goroutines 当前 goroutine 数\n")
	fmt.Fprintf(&b, "# TYPE ltzy_go_goroutines gauge\n")
	fmt.Fprintf(&b, "ltzy_go_goroutines %d\n", runtime.NumGoroutine())
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Fprintf(&b, "# HELP ltzy_go_heap_alloc_bytes 堆内存已分配字节数\n")
	fmt.Fprintf(&b, "# TYPE ltzy_go_heap_alloc_bytes gauge\n")
	fmt.Fprintf(&b, "ltzy_go_heap_alloc_bytes %d\n", m.HeapAlloc)
	// 配置可见性（排查用）
	fmt.Fprintf(&b, "# HELP ltzy_config_enabled 配置开关状态（1=开）\n")
	fmt.Fprintf(&b, "# TYPE ltzy_config_enabled gauge\n")
	fmt.Fprintf(&b, "ltzy_config_enabled{name=\"metric\"} %v\n", boolToFloat(config.EnableMetric))
	fmt.Fprintf(&b, "ltzy_config_enabled{name=\"memory_cache\"} %v\n", boolToFloat(config.MemoryCacheEnabled))
	fmt.Fprintf(&b, "ltzy_config_enabled{name=\"batch_update\"} %v\n", boolToFloat(config.BatchUpdateEnabled))
	return b.String()
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
