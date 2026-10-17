package monitor

import (
	"sync"
	"testing"
	"time"
)

// resetAlertDedup 清空去重集合，避免用例间互相影响
func resetAlertDedup() {
	alertedOnce = sync.Map{}
}

// TestAlertDedup 同一目标同一天只告警一次（D6 的核心行为）
func TestAlertDedup(t *testing.T) {
	resetAlertDedup()
	key := "user:1:20261017"
	if _, loaded := alertedOnce.LoadOrStore(key, struct{}{}); loaded {
		t.Fatal("首次应未告警过")
	}
	if _, loaded := alertedOnce.LoadOrStore(key, struct{}{}); !loaded {
		t.Fatal("第二次应命中已告警标记（防止刷屏）")
	}
}

// TestAlertEnvInt64 阈值解析：非法/负数一律视为关闭，两侧空格容错
func TestAlertEnvInt64(t *testing.T) {
	cases := []struct {
		raw  string
		want int64
	}{
		{"", 0},
		{"0", 0},
		{"1000000", 1000000},
		{"-5", 0},
		{"abc", 0},
		{" 200", 200}, // 两侧空格容错：环境变量里带空格太常见
		{"12.5", 0},
	}
	for _, tc := range cases {
		t.Setenv("ALERT_TEST_THRESHOLD", tc.raw)
		if got := alertEnvInt64("ALERT_TEST_THRESHOLD"); got != tc.want {
			t.Fatalf("阈值 %q 应解析为 %d，实际 %d", tc.raw, tc.want, got)
		}
	}
}

// TestAlertThresholdsDisabled 两个阈值都为 0 时必须立即返回、不启动轮询 goroutine。
func TestAlertThresholdsDisabled(t *testing.T) {
	t.Setenv("ALERT_USER_DAILY_QUOTA", "0")
	t.Setenv("ALERT_GLOBAL_DAILY_QUOTA", "0")
	done := make(chan struct{})
	go func() {
		StartUsageAlert()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("阈值为 0 时 StartUsageAlert 应立即返回")
	}
}
