package middleware

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/common/logger"
)

// 令牌级并发限制（LTZY-API 新增，C2）。
//
// 与 RPM/TPM 限速的区别：RPM 限的是「单位时间内的请求数」，属于流量整形；
// 并发限制的是「同一时刻的在途请求数」——流式（SSE）长连接场景下，
// 单个令牌可以在一分钟内只发 10 个请求却把连接挂住半小时，
// 此时只有并发限制能防止上游连接被单个令牌打爆。
//
// 实现：
//   - Redis：INCR 占位、DECR 释放，附 TTL 兜底（进程崩溃时槽位能自动回收）；
//   - 无 Redis：进程内 atomic 计数器（单实例部署足够；多实例场景请启用 Redis）。
//
// 语义：超限直接 429，**不排队** —— 排队会把内存和延迟一起放大，
// 客户端拿到明确的失败码后自行退避更安全。

const tokenConcurrencyKeyPrefix = "tokenConcurrency:"

// concurrencySlotTTL Redis 槽位的兜底过期时间。
// 正常路径由 DECR 释放；这个 TTL 只用于「进程被 kill -9 导致没来得及释放」的自愈。
// 取值远大于常规流式请求时长，避免正常长连接被误回收。
const concurrencySlotTTL = time.Hour

func tokenConcurrencyKey(tokenId int) string {
	return fmt.Sprintf("%s%d", tokenConcurrencyKeyPrefix, tokenId)
}

// TokenConcurrencyLimit 对当前令牌执行并发数限制。
// 必须放在 TokenAuth 之后（依赖 ctxkey.TokenId 与令牌并发配置）。
func TokenConcurrencyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenId := c.GetInt(ctxkey.TokenId)
		limit := c.GetInt(ctxkey.TokenConcurrency)
		if tokenId == 0 || limit <= 0 {
			c.Next()
			return
		}

		acquired, release, err := acquireConcurrencySlot(tokenId, limit)
		if err != nil {
			// 限流器自身异常时不阻断业务，仅记录日志（与 RPM 限流保持一致的降级策略）
			logger.SysError("token concurrency limiter error: " + err.Error())
			c.Next()
			return
		}
		if !acquired {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{
					"message": fmt.Sprintf("令牌并发数已达上限（%d），请稍后再试", limit),
					"type":    "one_api_error",
					"code":    "token_concurrency_limit_exceeded",
				},
			})
			c.Abort()
			return
		}
		// 请求全流程结束（含流式传输完成、后续中间件 abort）后释放槽位
		defer release()
		c.Next()
	}
}

// acquireConcurrencySlot 尝试占用一个并发槽位。
// 返回 (是否拿到, 释放函数, 错误)；未拿到时释放函数为 nil。
func acquireConcurrencySlot(tokenId int, limit int) (bool, func(), error) {
	if common.RedisEnabled {
		ctx := context.Background()
		key := tokenConcurrencyKey(tokenId)
		count, err := common.RDB.Incr(ctx, key).Result()
		if err != nil {
			return false, nil, err
		}
		// 刷新兜底 TTL：令牌活跃期间不过期，空闲一小时后自动清理
		_ = common.RDB.Expire(ctx, key, concurrencySlotTTL).Err()
		if count > int64(limit) {
			// 超限：把自己刚占的槽位还回去，避免计数被推高后长期虚占
			_ = common.RDB.Decr(ctx, key).Err()
			return false, nil, nil
		}
		return true, func() {
			_ = common.RDB.Decr(context.Background(), key).Err()
		}, nil
	}

	if inMemoryConcurrencyAcquire(tokenId, limit) {
		return true, func() { inMemoryConcurrencyRelease(tokenId) }, nil
	}
	return false, nil, nil
}

// ---------- 内存降级实现 ----------

// concurrencyCounters tokenId -> *int64（用 atomic 操作，避免全局锁）
var concurrencyCounters sync.Map

func inMemoryConcurrencyAcquire(tokenId int, limit int) bool {
	actual, _ := concurrencyCounters.LoadOrStore(tokenId, new(int64))
	counter := actual.(*int64)
	for {
		current := atomic.LoadInt64(counter)
		if current >= int64(limit) {
			return false
		}
		// CAS 失败说明有并发竞争，重读后重试
		if atomic.CompareAndSwapInt64(counter, current, current+1) {
			return true
		}
	}
}

func inMemoryConcurrencyRelease(tokenId int) {
	actual, ok := concurrencyCounters.Load(tokenId)
	if !ok {
		return
	}
	atomic.AddInt64(actual.(*int64), -1)
}
