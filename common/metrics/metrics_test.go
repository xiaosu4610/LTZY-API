package metrics

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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

// ---------------------------------------------------------------------------
// D8 多实例共享指标：以下用例不依赖真实 Redis，通过替换 relayRedisProvider
// 注入内存假实现来验证「写入键名/字段/增量」「汇总」与「失败降级」。
// ---------------------------------------------------------------------------

// fakeRelayRedis 是 relayRedisClient 的内存实现。
// 只保留被验证逻辑真正用到的语义（HINCRBY 累加、TTL 覆盖、String 未命中返回空），
// 这样断言的是 metrics 包拼出来的 key/field/增量，而不是假实现自身的行为。
type fakeRelayRedis struct {
	mu       sync.Mutex
	hashes   map[string]map[string]int64
	strings  map[string]string
	ttl      map[string]time.Duration
	writeErr error // 非 nil 时模拟写失败，用于验证降级
	readErr  error // 非 nil 时模拟读失败，用于验证降级
}

func newFakeRelayRedis() *fakeRelayRedis {
	return &fakeRelayRedis{
		hashes:  map[string]map[string]int64{},
		strings: map[string]string{},
		ttl:     map[string]time.Duration{},
	}
}

func (f *fakeRelayRedis) IncrHash(_ context.Context, key string, deltas map[string]int64, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	hash, ok := f.hashes[key]
	if !ok {
		hash = map[string]int64{}
		f.hashes[key] = hash
	}
	for field, delta := range deltas {
		hash[field] += delta
	}
	f.ttl[key] = ttl
	return nil
}

func (f *fakeRelayRedis) HGetAll(_ context.Context, key string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return nil, f.readErr
	}
	hash, ok := f.hashes[key]
	if !ok {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(hash))
	for field, value := range hash {
		out[field] = strconv.FormatInt(value, 10)
	}
	return out, nil
}

func (f *fakeRelayRedis) SetString(_ context.Context, key, value string, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.strings[key] = value
	f.ttl[key] = ttl
	return nil
}

func (f *fakeRelayRedis) GetString(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return "", f.readErr
	}
	return f.strings[key], nil
}

// hashField 读取假 Redis 中某字段的当前值（自带加锁，供并发用例断言）。
func (f *fakeRelayRedis) hashField(key, field string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hashes[key][field]
}

func (f *fakeRelayRedis) hashTTL(key string) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ttl[key]
}

// useFakeRedis 把包内 Redis provider 换成假实现，测试结束自动还原全局状态，
// 避免影响同包其他用例（尤其是断言纯内存路径的用例）。
func useFakeRedis(t *testing.T, fake *fakeRelayRedis) {
	t.Helper()
	original := relayRedisProvider
	relayRedisProvider = func() relayRedisClient { return fake }
	t.Cleanup(func() { relayRedisProvider = original })
}

// useMemoryOnly 显式固定到「未启用 Redis」的默认部署形态。
func useMemoryOnly(t *testing.T) {
	t.Helper()
	original := relayRedisProvider
	relayRedisProvider = func() relayRedisClient { return nil }
	t.Cleanup(func() { relayRedisProvider = original })
}

// snapshotField 读取快照中的某个字段，渠道不存在时按 0 处理
// （与 RenderPrometheus 的取值方式一致）。
func snapshotField(snapshot map[int]map[string]int64, channelId int, key string) int64 {
	if counters, ok := snapshot[channelId]; ok {
		return counters[key]
	}
	return 0
}

// TestMemoryPathChannelIsolation 无 Redis 路径：累加与按渠道隔离不变。
// 断言「前后差值」而不是绝对值：进程内计数是包级全局状态，用差值才能让
// -count>1 的重复运行（以及同包其他用例）互不污染。
func TestMemoryPathChannelIsolation(t *testing.T) {
	useMemoryOnly(t)
	before := SnapshotRelay()

	RecordRelay(9101, true)
	RecordRelay(9101, false)
	RecordRelayQuota(9101, 700)
	RecordRelay(9102, true)

	s := SnapshotRelay()
	if got := snapshotField(s, 9101, "total") - snapshotField(before, 9101, "total"); got != 2 {
		t.Fatalf("渠道 9101 total 增量应为 2, got %d: %+v", got, s[9101])
	}
	if got := snapshotField(s, 9101, "success") - snapshotField(before, 9101, "success"); got != 1 {
		t.Fatalf("渠道 9101 success 增量应为 1: %+v", s[9101])
	}
	if got := snapshotField(s, 9101, "fail") - snapshotField(before, 9101, "fail"); got != 1 {
		t.Fatalf("渠道 9101 fail 增量应为 1: %+v", s[9101])
	}
	if got := snapshotField(s, 9101, "quota") - snapshotField(before, 9101, "quota"); got != 700 {
		t.Fatalf("渠道 9101 quota 增量应为 700: %+v", s[9101])
	}
	// 按渠道隔离：9102 只受自己的写入影响
	if got := snapshotField(s, 9102, "total") - snapshotField(before, 9102, "total"); got != 1 {
		t.Fatalf("渠道 9102 total 增量应为 1: %+v", s[9102])
	}
	if got := snapshotField(s, 9102, "fail") - snapshotField(before, 9102, "fail"); got != 0 {
		t.Fatalf("渠道 9102 fail 不应变化: %+v", s[9102])
	}
}

// TestMemoryPathPrometheusUnchanged 无 Redis 路径：Prometheus 输出格式与数值不变。
func TestMemoryPathPrometheusUnchanged(t *testing.T) {
	useMemoryOnly(t)
	before := SnapshotRelay()

	RecordRelay(9103, true)
	RecordRelay(9103, true)
	wantTotal := snapshotField(before, 9103, "total") + 2
	wantSuccess := snapshotField(before, 9103, "success") + 2

	out := RenderPrometheus()
	for _, want := range []string{
		"# HELP ltzy_relay_requests_total 中继请求总数",
		"# TYPE ltzy_relay_requests_total counter",
		"ltzy_relay_requests_total{channel=\"9103\"} " + strconv.FormatInt(wantTotal, 10),
		"ltzy_relay_success_total{channel=\"9103\"} " + strconv.FormatInt(wantSuccess, 10),
		"ltzy_relay_fail_total{channel=\"9103\"} " + strconv.FormatInt(snapshotField(before, 9103, "fail"), 10),
		"ltzy_relay_quota_total{channel=\"9103\"} " + strconv.FormatInt(snapshotField(before, 9103, "quota"), 10),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("无 Redis 路径 Prometheus 输出缺少 %q:\n%s", want, out)
		}
	}
}

// TestRedisPathAccumulationAndTTL Redis 路径：计数按约定 key/field 累加，且每次写入都刷新 TTL。
func TestRedisPathAccumulationAndTTL(t *testing.T) {
	fake := newFakeRelayRedis()
	useFakeRedis(t, fake)

	RecordRelay(9201, true)
	RecordRelay(9201, true)
	RecordRelay(9201, false)
	RecordRelayQuota(9201, 1500)
	// 负数是退还预扣，必须照样累减
	RecordRelayQuota(9201, -200)

	if got := fake.hashField(relayHashKey, relayMetricField(9201, metricTotal)); got != 3 {
		t.Fatalf("total 应为 3, got %d", got)
	}
	if got := fake.hashField(relayHashKey, relayMetricField(9201, metricSuccess)); got != 2 {
		t.Fatalf("success 应为 2, got %d", got)
	}
	if got := fake.hashField(relayHashKey, relayMetricField(9201, metricFail)); got != 1 {
		t.Fatalf("fail 应为 1, got %d", got)
	}
	if got := fake.hashField(relayHashKey, relayMetricField(9201, metricQuota)); got != 1300 {
		t.Fatalf("quota 应为 1300, got %d", got)
	}
	if got := fake.hashTTL(relayHashKey); got != metricsKeyTTL {
		t.Fatalf("Hash TTL 应为 %v, got %v", metricsKeyTTL, got)
	}

	// 快照取自 Redis（跨实例汇总），字段齐全
	s := SnapshotRelay()
	if s[9201]["total"] != 3 || s[9201]["success"] != 2 || s[9201]["fail"] != 1 || s[9201]["quota"] != 1300 {
		t.Fatalf("Redis 路径快照错误: %+v", s[9201])
	}
}

// TestRedisPathSnapshotAggregatesAllChannels Redis 路径：汇总其他实例写入的数据，并容忍脏字段。
func TestRedisPathSnapshotAggregatesAllChannels(t *testing.T) {
	fake := newFakeRelayRedis()
	useFakeRedis(t, fake)

	// 模拟「本实例之外的另一个实例」已经写入的数据
	fake.hashes[relayHashKey] = map[string]int64{
		"9301:total":   10,
		"9301:success": 8,
		"9301:fail":    2,
		"9301:quota":   500000,
		// 脏数据：渠道号非法 / 指标名未知，都应被忽略而不是报错或写进快照
		"not-a-channel:total": 7,
		"9302:unknown":        7,
		"9303:total":          7,
	}

	RecordRelay(9301, true)
	RecordRelayQuota(9301, 1000)

	s := SnapshotRelay()
	if s[9301]["total"] != 11 || s[9301]["success"] != 9 || s[9301]["fail"] != 2 || s[9301]["quota"] != 501000 {
		t.Fatalf("跨实例汇总错误: %+v", s[9301])
	}
	if _, ok := s[9302]; ok {
		t.Fatalf("未知指标名的渠道不应出现在快照里: %+v", s[9302])
	}
	if _, ok := s[9303]; !ok {
		t.Fatal("只有一个字段的渠道也应被补全后出现在快照里")
	}
	// 只有一个字段的渠道，其余字段补 0（否则会被静默当成缺失）
	if s[9303]["success"] != 0 || s[9303]["fail"] != 0 || s[9303]["quota"] != 0 {
		t.Fatalf("缺失字段应补 0: %+v", s[9303])
	}

	out := RenderPrometheus()
	if !strings.Contains(out, "ltzy_relay_requests_total{channel=\"9301\"} 11") {
		t.Fatalf("Prometheus 输出未反映 Redis 汇总值:\n%s", out)
	}
}

// TestRedisPathLastFailShared Redis 路径：失败原因走带 TTL 的 String，可读到别的实例写的值。
func TestRedisPathLastFailShared(t *testing.T) {
	fake := newFakeRelayRedis()
	useFakeRedis(t, fake)

	SetRelayLastFail(9501, "upstream timeout")
	if fake.strings[relayLastFailKey(9501)] != "upstream timeout" {
		t.Fatalf("失败原因未写入 Redis: %+v", fake.strings)
	}
	if got := fake.hashTTL(relayLastFailKey(9501)); got != metricsKeyTTL {
		t.Fatalf("失败原因 TTL 应为 %v, got %v", metricsKeyTTL, got)
	}

	// 另一个实例写入的失败原因，本实例也要能读到（这是共享的意义）
	fake.strings[relayLastFailKey(9502)] = "from another instance"
	if got := GetRelayLastFail(9502); got != "from another instance" {
		t.Fatalf("应读到共享失败原因, got %q", got)
	}
	// Redis 未命中的渠道回落内存（这里两者都为空）
	if got := GetRelayLastFail(9599); got != "" {
		t.Fatalf("未记录渠道应返回空串, got %q", got)
	}
}

// TestRedisFailureDegradesToMemory Redis 读写失败时不能阻断业务：计数留在内存，且只记日志。
func TestRedisFailureDegradesToMemory(t *testing.T) {
	// 基线取「未启用 Redis」时的内存快照，随后注入一个永远失败的假 Redis
	before := SnapshotRelay()

	fake := newFakeRelayRedis()
	fake.writeErr = errors.New("redis write down")
	fake.readErr = errors.New("redis read down")
	useFakeRedis(t, fake)

	RecordRelay(9401, true)
	RecordRelay(9401, false)
	RecordRelayQuota(9401, 42)
	SetRelayLastFail(9401, "upstream 502")

	s := SnapshotRelay()
	if got := snapshotField(s, 9401, "total") - snapshotField(before, 9401, "total"); got != 2 {
		t.Fatalf("Redis 故障时 total 仍应记入内存, 增量 %d: %+v", got, s[9401])
	}
	if got := snapshotField(s, 9401, "success") - snapshotField(before, 9401, "success"); got != 1 {
		t.Fatalf("Redis 故障时 success 仍应记入内存: %+v", s[9401])
	}
	if got := snapshotField(s, 9401, "fail") - snapshotField(before, 9401, "fail"); got != 1 {
		t.Fatalf("Redis 故障时 fail 仍应记入内存: %+v", s[9401])
	}
	if got := snapshotField(s, 9401, "quota") - snapshotField(before, 9401, "quota"); got != 42 {
		t.Fatalf("Redis 故障时 quota 仍应记入内存: %+v", s[9401])
	}
	if got := GetRelayLastFail(9401); got != "upstream 502" {
		t.Fatalf("Redis 故障时应回落内存失败原因, got %q", got)
	}
}

// TestConcurrentRecordRelayMemory 并发写入（无 Redis 路径）后总数必须精确。
func TestConcurrentRecordRelayMemory(t *testing.T) {
	useMemoryOnly(t)
	before := SnapshotRelay()
	const goroutines, perGoroutine = 8, 500

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				RecordRelay(9601, j%2 == 0)
			}
		}()
	}
	wg.Wait()

	s := SnapshotRelay()
	want := int64(goroutines * perGoroutine)
	if got := snapshotField(s, 9601, "total") - snapshotField(before, 9601, "total"); got != want {
		t.Fatalf("并发写入 total 增量应为 %d, got %d", want, got)
	}
	if snapshotField(s, 9601, "success")+snapshotField(s, 9601, "fail") != snapshotField(s, 9601, "total") {
		t.Fatalf("success+fail 应等于 total: %+v", s[9601])
	}
}

// TestConcurrentRecordRelayRedis 并发写入（Redis 路径）后共享总数必须精确（HINCRBY 累加语义）。
func TestConcurrentRecordRelayRedis(t *testing.T) {
	fake := newFakeRelayRedis()
	useFakeRedis(t, fake)
	const goroutines, perGoroutine = 8, 500

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				RecordRelay(9701, j%2 == 0)
				RecordRelayQuota(9701, 1)
			}
		}()
	}
	wg.Wait()

	want := int64(goroutines * perGoroutine)
	if got := fake.hashField(relayHashKey, relayMetricField(9701, metricTotal)); got != want {
		t.Fatalf("共享 total 应为 %d, got %d", want, got)
	}
	if got := fake.hashField(relayHashKey, relayMetricField(9701, metricSuccess)); got != want/2 {
		t.Fatalf("共享 success 应为 %d, got %d", want/2, got)
	}
	if got := fake.hashField(relayHashKey, relayMetricField(9701, metricQuota)); got != want {
		t.Fatalf("共享 quota 应为 %d, got %d", want, got)
	}
	if s := SnapshotRelay(); s[9701]["total"] != want {
		t.Fatalf("快照 total 应为 %d, got %d", want, s[9701]["total"])
	}
}
