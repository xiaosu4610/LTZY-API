package middleware

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/common/logger"
)

// 令牌级限速（LTZY-API 新增）
//
// 设计：
//   - 以「分钟」为窗口，Redis 计数器（INCR + EXPIRE）实现分布式限流；
//   - 未启用 Redis 时降级为进程内内存计数器；
//   - RPM（每分钟请求数）在 TokenAuth 之后、转发之前即时判定；
//   - TPM（每分钟 token 数）在本请求完成后按实际消耗累加（由 relay 层调用）。
//
// 说明：这里采用固定窗口而非滑动窗口，实现简单、开销低；
// 如需更平滑的限速可后续替换为令牌桶。

const tokenRateLimitKeyPrefix = "tokenRateLimit:"

func tokenRPMKey(tokenId int) string {
	return fmt.Sprintf("%s:rpm:%d:%s", tokenRateLimitKeyPrefix, tokenId, time.Now().Format("200601021504"))
}

func tokenTPMKey(tokenId int) string {
	return fmt.Sprintf("%s:tpm:%d:%s", tokenRateLimitKeyPrefix, tokenId, time.Now().Format("200601021504"))
}

// TokenRateLimit 对当前令牌执行 RPM 限流检查。
// 必须在 TokenAuth 之后使用（依赖 ctxkey.TokenId 与令牌限速配置）。
func TokenRateLimit() func(c *gin.Context) {
	return func(c *gin.Context) {
		tokenId := c.GetInt(ctxkey.TokenId)
		rpm := c.GetInt(ctxkey.TokenRpm)
		if tokenId == 0 || rpm <= 0 {
			c.Next()
			return
		}

		count, err := increaseCounter(tokenRPMKey(tokenId), 2*time.Minute)
		if err != nil {
			// 限流器自身异常时不阻断业务，仅记录日志
			logger.SysError("token rate limiter error: " + err.Error())
			c.Next()
			return
		}
		if count > int64(rpm) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{
					"message": fmt.Sprintf("令牌请求频率超限（%d 次/分钟），请稍后再试", rpm),
					"type":    "one_api_error",
					"code":    "token_rpm_limit_exceeded",
				},
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// RecordTokenTPM 累加令牌本分钟的 token 消耗量。
// 由 relay 层在请求完成后调用；超过上限时仅记录告警，不回溯阻断（当前请求已产生费用）。
func RecordTokenTPM(tokenId int, tpm int, tokens int) {
	if tokenId == 0 || tpm <= 0 || tokens <= 0 {
		return
	}
	count, err := increaseCounterBy(tokenTPMKey(tokenId), int64(tokens), 2*time.Minute)
	if err != nil {
		logger.SysError("record token tpm error: " + err.Error())
		return
	}
	if count > int64(tpm) {
		logger.SysError(fmt.Sprintf("token %d exceeded TPM limit: %d > %d", tokenId, count, tpm))
	}
}

// increaseCounter 将计数 +1 并返回新值；首次创建时设置过期时间。
func increaseCounter(key string, ttl time.Duration) (int64, error) {
	return increaseCounterBy(key, 1, ttl)
}

func increaseCounterBy(key string, delta int64, ttl time.Duration) (int64, error) {
	if common.RedisEnabled {
		ctx := context.Background()
		pipe := common.RDB.TxPipeline()
		incr := pipe.IncrBy(ctx, key, delta)
		pipe.Expire(ctx, key, ttl)
		if _, err := pipe.Exec(ctx); err != nil {
			return 0, err
		}
		return incr.Val(), nil
	}
	return inMemoryCounterIncrease(key, delta, ttl), nil
}

// ---------- 内存降级实现 ----------

type memoryCounter struct {
	mu        sync.Mutex
	value     int64
	expiresAt time.Time
}

var memoryCounters sync.Map // key -> *memoryCounter

func inMemoryCounterIncrease(key string, delta int64, ttl time.Duration) int64 {
	now := time.Now()
	actual, _ := memoryCounters.LoadOrStore(key, &memoryCounter{})
	counter := actual.(*memoryCounter)
	counter.mu.Lock()
	defer counter.mu.Unlock()
	if now.After(counter.expiresAt) {
		counter.value = 0
		counter.expiresAt = now.Add(ttl)
	}
	counter.value += delta
	return counter.value
}
