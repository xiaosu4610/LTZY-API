package metrics

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/logger"
)

// 多实例共享指标（LTZY-API 新增，D8）。
//
// 单实例部署靠 metrics.go 里的进程内计数就够；一旦横向扩容，
// 每个实例只看得见自己那份流量，/metrics 的数字会严重偏低。
// 这里在「启用 Redis」时把计数额外同步到 Redis，让所有实例共用一份；
// 未启用 Redis 时本文件所有函数都是空操作，进程内路径一个字节都不变。
//
// 数据结构（单 Hash + 复合字段）：
//
//	key   ltzy:metrics:relay                  (Hash)
//	field <channelId>:total|success|fail|quota
//
// 为什么不像「每渠道一个 Hash + 一个渠道集合」那样拆 key：拆分方案为了枚举渠道
// 必须再维护一个 Set（或 SCAN 前缀），汇总时还要对每个渠道各来一次 HGETALL，
// 而且「数据 key 的 TTL」和「集合成员的清理」很难对齐，集合里会长期残留已过期的渠道。
// 单 Hash 用一次 HGETALL 就能枚举全部渠道，TTL 也只有一把 key 要维护。
// 代价是热 key 集中（HINCRBY/EXPIRE 都是 O(1)，可接受）与 Hash 体积随渠道数线性增长。
//
// 语义提醒：Redis 模式下 /metrics 返回的是「集群总量」而不是「本实例量」。
// Prometheus 默认给每个抓取目标加上 instance 标签，多个实例同时被抓时
// 会出现数值相同的多条序列，sum() 会重复计算；应只抓取一个实例，
// 或用 max by (channel) 之类的聚合。这是共享指标的固有代价，不是实现缺陷。

const (
	// relayHashKey 中继指标的共享 Hash key。
	relayHashKey = "ltzy:metrics:relay"
	// relayLastFailKeyPrefix 「最近一次失败原因」的 String key 前缀，后缀是 channelId。
	relayLastFailKeyPrefix = "ltzy:metrics:relay:lastfail:"

	// metricsRedisTimeout 指标访问 Redis 的最长等待时间。
	// 指标是旁路数据：丢一个样本只是曲线缺一格，绝不能拖慢中继请求，
	// 所以超时取得比业务请求短得多，一旦超时立刻降级为进程内计数。
	metricsRedisTimeout = 800 * time.Millisecond

	// metricsKeyTTL 指标 key 的过期时间（每次写入都在同一个 Pipeline 里刷新，即滑动窗口）。
	//
	// 为什么是这个策略：
	//   - 按天分桶虽然能自然过期，但会让 Prometheus 计数器每天归零（counter reset），
	//     破坏累加语义，rate() 上会出现虚假的尖峰；
	//   - 固定 TTL 且只在创建时设置，会让长期活跃的渠道在运行中途丢计数；
	//   - 滑动 TTL 的含义是「沉寂 7 天的渠道自动回收」：活跃渠道持续累加，
	//     已下线渠道的残留 key 一周内消失，因此 key 数量不会无限增长。
	//
	// 选 7 天是为了覆盖常见的告警/对比窗口；需要更长历史应该交给 Prometheus
	// 的长期存储，而不是让 Redis 无限堆积。
	metricsKeyTTL = 7 * 24 * time.Hour

	// 指标字段名（与 metrics.go 快照输出的 key 保持一致）。
	metricTotal   = "total"
	metricSuccess = "success"
	metricFail    = "fail"
	metricQuota   = "quota"
)

// relayRedisClient 抽象「共享指标同步」所需的最小 Redis 命令集。
//
// 故意用「值 + error」而不是直接暴露 go-redis 的 Cmd 类型：单元测试可以注入
// 纯内存假实现来验证键名拼接、累加与汇总逻辑，既不需要真实 Redis 服务，
// 也不需要构造 go-redis 的 Cmd 对象（其字段不可导出，构造起来很别扭）。
type relayRedisClient interface {
	// IncrHash 对同一 Hash 的多个字段做 HINCRBY 并刷新 TTL。
	// 真实实现用一个 Pipeline 合并成一次网络往返：指标写入在每次中继请求的热路径上，
	// 往返次数比单条命令数量更值钱；这些命令彼此独立、不需要 MULTI/EXEC 的事务语义，
	// 因此用 Pipeline 而不是 TxPipeline（少一轮 RTT 和一次服务端事务开销）。
	// 用 HINCRBY 而不是「读出来加一再写回」，是为了让多实例并发累加天然原子，不会丢计数。
	IncrHash(ctx context.Context, key string, deltas map[string]int64, ttl time.Duration) error
	// HGetAll 读取整个 Hash（快照汇总用）。
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	// SetString 写入带 TTL 的 String（最近一次失败原因）。
	SetString(ctx context.Context, key, value string, ttl time.Duration) error
	// GetString 读取 String；未命中返回空串且 err 为 nil，便于调用方回落内存。
	GetString(ctx context.Context, key string) (string, error)
}

// relayRedisProvider 返回当前可用的指标 Redis 客户端，返回 nil 表示走纯内存路径。
// 做成包内变量而不是普通函数，是为了单元测试能把它替换成内存假实现（不依赖真实 Redis）；
// 这里用惰性判断而不是 init 期固定值，是因为 common.RDB 由 main 在 init 之后才初始化。
var relayRedisProvider = func() relayRedisClient {
	if !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	return cmdableRelayRedisClient{cmd: common.RDB}
}

// cmdableRelayRedisClient 把 go-redis 的 Cmdable 适配成 relayRedisClient。
type cmdableRelayRedisClient struct {
	cmd redis.Cmdable
}

func (c cmdableRelayRedisClient) IncrHash(ctx context.Context, key string, deltas map[string]int64, ttl time.Duration) error {
	pipe := c.cmd.Pipeline()
	for field, delta := range deltas {
		pipe.HIncrBy(ctx, key, field, delta)
	}
	// 每次写入都刷新 TTL：只要有流量就不过期，没有流量才回收（滑动窗口）
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (c cmdableRelayRedisClient) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return c.cmd.HGetAll(ctx, key).Result()
}

func (c cmdableRelayRedisClient) SetString(ctx context.Context, key, value string, ttl time.Duration) error {
	return c.cmd.Set(ctx, key, value, ttl).Err()
}

func (c cmdableRelayRedisClient) GetString(ctx context.Context, key string) (string, error) {
	value, err := c.cmd.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		// key 不存在不是故障：调用方据此回落内存，不该被当成 Redis 异常打日志
		return "", nil
	}
	return value, err
}

// relayMetricField Hash 里的字段名：<channelId>:<metric>。
func relayMetricField(channelId int, metric string) string {
	return strconv.Itoa(channelId) + ":" + metric
}

// relayLastFailKey 「最近一次失败原因」的 key。
// 用 String 而不是塞进 Hash：失败原因只在出错时更新，放 Hash 会把它一起卷进
// 每次汇总的 HGETALL，且无法单独设 TTL。
func relayLastFailKey(channelId int) string {
	return relayLastFailKeyPrefix + strconv.Itoa(channelId)
}

// recordRelayRedis 把一次中继结果同步到共享存储。
// 未启用 Redis 时直接返回；写入失败只记日志——调用方已经写过进程内计数，
// 失败仅影响跨实例可见性，绝不阻断业务（与限流中间件的降级策略一致）。
func recordRelayRedis(channelId int, success bool) {
	client := relayRedisProvider()
	if client == nil {
		return
	}
	resultField := metricFail
	if success {
		resultField = metricSuccess
	}
	// total 与 success/fail 必须在同一次调用里累加，否则 Prometheus 上会出现
	// total != success + fail 的漂移；Pipeline 保证它们一起发出。
	ctx, cancel := context.WithTimeout(context.Background(), metricsRedisTimeout)
	defer cancel()
	err := client.IncrHash(ctx, relayHashKey, map[string]int64{
		relayMetricField(channelId, metricTotal): 1,
		relayMetricField(channelId, resultField): 1,
	}, metricsKeyTTL)
	if err != nil {
		logger.SysError("metrics: record relay to redis failed, keep memory counter only: " + err.Error())
	}
}

// recordRelayQuotaRedis 把一次额度消耗同步到共享存储（可能为负=退还预扣）。
func recordRelayQuotaRedis(channelId int, quota int64) {
	client := relayRedisProvider()
	if client == nil || quota == 0 {
		// quota 为 0 时不必占用一次网络往返
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), metricsRedisTimeout)
	defer cancel()
	err := client.IncrHash(ctx, relayHashKey, map[string]int64{
		relayMetricField(channelId, metricQuota): quota,
	}, metricsKeyTTL)
	if err != nil {
		logger.SysError("metrics: record relay quota to redis failed, keep memory counter only: " + err.Error())
	}
}

// setRelayLastFailRedis 写入共享的「最近一次失败原因」。
func setRelayLastFailRedis(channelId int, reason string) {
	client := relayRedisProvider()
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), metricsRedisTimeout)
	defer cancel()
	err := client.SetString(ctx, relayLastFailKey(channelId), reason, metricsKeyTTL)
	if err != nil {
		logger.SysError("metrics: set relay last fail to redis failed, keep memory value only: " + err.Error())
	}
}

// getRelayLastFailRedis 读取共享的「最近一次失败原因」。
// ok=false 表示未启用 Redis、key 未命中或读取失败，调用方应回落进程内计数。
func getRelayLastFailRedis(channelId int) (string, bool) {
	client := relayRedisProvider()
	if client == nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), metricsRedisTimeout)
	defer cancel()
	reason, err := client.GetString(ctx, relayLastFailKey(channelId))
	if err != nil {
		logger.SysError("metrics: get relay last fail from redis failed, fallback to memory: " + err.Error())
		return "", false
	}
	if reason == "" {
		return "", false
	}
	return reason, true
}

// snapshotRelayRedis 从 Redis 汇总所有实例的中继指标。
// ok=false 表示未启用 Redis 或读取失败，调用方应回落本进程内存计数。
func snapshotRelayRedis() (map[int]map[string]int64, bool) {
	client := relayRedisProvider()
	if client == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), metricsRedisTimeout)
	defer cancel()
	fields, err := client.HGetAll(ctx, relayHashKey)
	if err != nil {
		logger.SysError("metrics: read relay metrics from redis failed, fallback to memory: " + err.Error())
		return nil, false
	}
	out := make(map[int]map[string]int64, len(fields)/4+1)
	broken := 0
	for field, raw := range fields {
		separator := strings.LastIndex(field, ":")
		if separator <= 0 {
			broken++
			continue
		}
		channelId, err := strconv.Atoi(field[:separator])
		if err != nil {
			broken++
			continue
		}
		metric := field[separator+1:]
		if !isRelayMetric(metric) {
			// 未知后缀静默忽略而不是记为脏数据：以后新增指标时，旧实例读到新数据
			// 属于正常的版本共存，不该每 15 秒就往日志里刷一条错误
			continue
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			broken++
			continue
		}
		counters, ok := out[channelId]
		if !ok {
			// 四个字段预置 0：消费方（RenderPrometheus）按固定字段取值，
			// 缺字段会静默变成 0，宁可在源头补全，避免输出「看起来正常」的错误数据
			counters = map[string]int64{
				metricTotal:   0,
				metricSuccess: 0,
				metricFail:    0,
				metricQuota:   0,
			}
			out[channelId] = counters
		}
		counters[metric] = value
	}
	if broken > 0 {
		// 只汇总成一条日志：脏数据可能很多，逐条打日志会把日志刷爆
		logger.SysError(fmt.Sprintf("metrics: ignored %d malformed relay metric fields in redis", broken))
	}
	return out, true
}

// isRelayMetric 判断 Hash 字段后缀是否是已知指标名。
func isRelayMetric(metric string) bool {
	switch metric {
	case metricTotal, metricSuccess, metricFail, metricQuota:
		return true
	}
	return false
}
