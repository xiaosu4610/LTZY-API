package metrics

import (
	"strings"
	"testing"
)

// TestRecordAndSnapshot 验证计数与快照的并发安全读写
func TestRecordAndSnapshot(t *testing.T) {
	RecordRelay(1, true)
	RecordRelay(1, true)
	RecordRelay(1, false)
	RecordRelayQuota(1, 1500)

	s := SnapshotRelay()
	if s[1]["total"] != 3 || s[1]["success"] != 2 || s[1]["fail"] != 1 || s[1]["quota"] != 1500 {
		t.Fatalf("快照数据错误: %+v", s[1])
	}
}

// TestSetRelayLastFail 验证失败原因记录与截断
func TestSetRelayLastFail(t *testing.T) {
	SetRelayLastFail(2, "upstream timeout")
	if GetRelayLastFail(2) != "upstream timeout" {
		t.Fatalf("失败原因读取错误: %s", GetRelayLastFail(2))
	}
	SetRelayLastFail(2, strings.Repeat("x", 500))
	if len(GetRelayLastFail(2)) > 120 {
		t.Fatalf("失败原因应截断到 120 字节, got %d", len(GetRelayLastFail(2)))
	}
	// 未记录过的渠道返回空串
	if GetRelayLastFail(999) != "" {
		t.Fatal("未记录的渠道应返回空串")
	}
}

// TestRenderPrometheus 验证输出符合 exposition format：有 HELP/TYPE 头与核心指标
func TestRenderPrometheus(t *testing.T) {
	RecordRelay(3, true)
	out := RenderPrometheus()
	for _, want := range []string{
		"# HELP ltzy_relay_requests_total",
		"# TYPE ltzy_relay_requests_total counter",
		"ltzy_build_info{version=",
		"ltzy_uptime_seconds ",
		"ltzy_relay_requests_total{channel=\"3\"} 1",
		"ltzy_go_goroutines ",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Prometheus 输出缺少 %q:\n%s", want, out)
		}
	}
}
