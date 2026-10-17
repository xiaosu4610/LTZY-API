package middleware

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/env"
	"github.com/xiaosu4610/LTZY-API/common/helper"
	"github.com/xiaosu4610/LTZY-API/common/logger"
)

// 请求签名防重放（LTZY-API 新增，E4）。
//
// 为什么需要：TokenAuth 只证明「请求携带了合法令牌」，但令牌一旦在链路上被截获，
// 攻击者可以原样重放（或改掉 body 后重放）——尤其在明文 HTTP、日志留存、
// 中间代理等场景下。签名把「方法 + 路径 + 时间戳 + 随机数 + 请求体」整体绑定到
// 一个只有客户端与服务端知道的密钥上，并配合时间窗 + nonce 去重，
// 使被截获的请求无法二次使用。
//
// 设计取舍：
//   - 默认关闭（ENABLE_REQUEST_SIGNATURE=false），未启用时只读一个包级 bool，
//     对既有部署零开销、零行为变化；
//   - 验签密钥取环境变量 SIGNATURE_SECRET（全局密钥），而不是「该请求令牌的 Key」。
//     原因：TokenAuth 只把 TokenId / TokenName 写进 ctx，令牌明文 Key 不会进入
//     上下文，要用它就得改 common/ctxkey 与 relay 层（本次改动范围只允许 middleware/）；
//     而且数字 TokenId / 令牌名并不是客户端已知信息，派生出来的密钥客户端无法自行计算，
//     落地时无法交付。全局密钥可以由网关或客户端 SDK 统一持有，便于文档化与轮换。
//   - 校验顺序是「验签 → 时间窗 → nonce」：验签放在最前，非持钥者无法写入
//     nonce 存储，否则攻击者可以用随机 nonce 灌满 Redis / 内存，把防重放机制本身变成 DoS 面。
//   - 校验器自身异常（读 body 失败、Redis 故障）一律记日志放行，与 RPM 限流、
//     并发限制、预算中间件保持同一降级策略：安全能力故障不应让业务全线不可用。

const (
	// 请求头名称按约定固定，改名属于破坏性协议变更。
	signatureHeader     = "X-Signature"
	signatureTimestampH = "X-Timestamp"
	signatureNonceH     = "X-Nonce"

	signatureNonceKeyPrefix = "requestSignature:nonce:"

	// nonce 长度上限：nonce 直接进 Redis key / 内存 map，
	// 不设上限等于把「任意长 key」的写入权交给未通过验签的调用方（虽然验签在前，
	// 但仍能约束客户端实现质量，避免异常客户端制造巨型 key）。
	signatureMaxNonceLength = 128

	// 内存降级时触发整体清理的水位线。只用「水位线 + 阈值」而非每次写入都遍历，
	// 否则高并发下 O(n) 清理会被放大成明显的延迟抖动。
	signatureNonceCleanupThreshold = 4096

	// 默认允许的时间偏差（秒）。
	defaultSignatureTTLSeconds = 300
)

var (
	// signatureEnabled 由 ENABLE_REQUEST_SIGNATURE 控制，默认关闭。
	signatureEnabled = env.Bool("ENABLE_REQUEST_SIGNATURE", false)

	// signatureTTLSeconds 时间戳允许的偏差（秒），同时用作 nonce 的去重保留窗口。
	// 两个用途共用一个值是有意的：窗口外的请求已被时间戳拦下，nonce 无需留更久。
	signatureTTLSeconds = env.Int("SIGNATURE_TTL_SECONDS", defaultSignatureTTLSeconds)

	// signatureSecret 全局验签密钥；为空时视为未正确配置。
	signatureSecret = env.String("SIGNATURE_SECRET", "")

	// 配置缺失只在首次请求时告警一次，避免刷爆日志。
	signatureSecretWarnOnce sync.Once
)

// SignatureVerify 校验请求签名并按时间窗 + nonce 拦截重放。
//
// 启用后要求请求携带 X-Signature / X-Timestamp / X-Nonce 三个头：
//
//	X-Signature = hex(HMAC-SHA256(secret, method + "\n" + path + "\n" + timestamp + "\n" + nonce + "\n" + body))
//
// 其中 method 为全大写 HTTP 方法，path 为不含 query 的请求路径（URL.Path），
// timestamp 为 Unix 秒（十进制字符串），body 为原始请求体字节（GET 等无体请求按空串参与）。
// 失败统一返回 401，error.code 区分 signature_invalid / signature_expired / nonce_replayed。
//
// 挂载位置建议见交付说明：必须位于 GzipDecodeMiddleware 之后（签名针对解压后的明文 body），
// 建议位于 TokenAuth 之前（把伪造请求挡在数据库查询之前）。
func SignatureVerify() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !signatureEnabled {
			c.Next()
			return
		}
		if signatureSecret == "" {
			// 开关打开但密钥没配：属于「校验器不可用」，按既有降级策略放行而非全量 401，
			// 否则一次环境变量漏配会让整个中继入口不可用。
			signatureSecretWarnOnce.Do(func() {
				logger.SysError("request signature is enabled but SIGNATURE_SECRET is empty, all /v1 requests are passed through")
			})
			c.Next()
			return
		}

		timestamp := c.Request.Header.Get(signatureTimestampH)
		nonce := c.Request.Header.Get(signatureNonceH)
		signature := c.Request.Header.Get(signatureHeader)
		if timestamp == "" || nonce == "" || signature == "" {
			abortWithSignatureError(c, "signature_invalid", "缺少请求签名头（X-Signature / X-Timestamp / X-Nonce）")
			return
		}
		if len(nonce) > signatureMaxNonceLength {
			abortWithSignatureError(c, "signature_invalid", "请求签名 nonce 过长")
			return
		}

		ts, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			// 时间戳本身参与签名，格式错误说明客户端实现有误，归为签名无效而非过期。
			abortWithSignatureError(c, "signature_invalid", "请求签名时间戳格式非法，应为 Unix 秒")
			return
		}

		body, err := readBodyForSignature(c)
		if err != nil {
			logger.SysError("request signature middleware failed to read body: " + err.Error())
			c.Next()
			return
		}

		expected := computeRequestSignature(signatureSecret, c.Request.Method, c.Request.URL.Path, timestamp, nonce, body)
		// hmac.Equal 而非 == ：按字节定长比较，避免时序侧信道被用来逐字节猜签名。
		if !hmac.Equal([]byte(expected), []byte(signature)) {
			abortWithSignatureError(c, "signature_invalid", "请求签名校验失败")
			return
		}

		ttlSeconds := signatureTTLSeconds
		if ttlSeconds <= 0 {
			// 配成 0 或负数会让 Redis 的 SET NX 变成「永不过期的 key」，
			// 相当于把 nonce 存储变成无界内存泄漏；这里回落到默认窗口。
			ttlSeconds = defaultSignatureTTLSeconds
		}
		ttl := time.Duration(ttlSeconds) * time.Second
		if skew := time.Now().Unix() - ts; skew > int64(ttlSeconds) || skew < -int64(ttlSeconds) {
			abortWithSignatureError(c, "signature_expired", "请求签名已过期，请检查客户端时间与时区设置")
			return
		}

		fresh, err := consumeSignatureNonce(nonce, ttl)
		if err != nil {
			// 去重存储故障（如 Redis 抖动）不阻断业务，只降级为「本次不做重放判断」
			logger.SysError("request signature nonce store error: " + err.Error())
			c.Next()
			return
		}
		if !fresh {
			abortWithSignatureError(c, "nonce_replayed", "请求签名已被使用（疑似重放），请重新生成签名")
			return
		}

		c.Next()
	}
}

// computeRequestSignature 计算规范串的 HMAC-SHA256 十六进制摘要。
// 各字段用 \n 分隔而不是直接拼接，避免「不同字段组合拼出同一字符串」的歧义。
func computeRequestSignature(secret string, method string, path string, timestamp string, nonce string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(method))
	mac.Write([]byte("\n"))
	mac.Write([]byte(path))
	mac.Write([]byte("\n"))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write([]byte(nonce))
	mac.Write([]byte("\n"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// readBodyForSignature 读取原始请求体并原样放回。
// 复用 common.GetRequestBody 的上下文缓存，避免与后续 TokenAuth / relay 重复读流；
// 但它只读不还原（gin 的 body 是一次性 Reader），所以这里必须补上还原，
// 否则签名中间件会让下游读到空 body——这是「校验类中间件」最容易踩的坑。
func readBodyForSignature(c *gin.Context) ([]byte, error) {
	if c.Request.Body == nil {
		return nil, nil
	}
	body, err := common.GetRequestBody(c)
	if err != nil {
		return nil, err
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
	return body, nil
}

// consumeSignatureNonce 占用一个 nonce；返回 false 表示该 nonce 在有效窗口内已出现过。
func consumeSignatureNonce(nonce string, ttl time.Duration) (bool, error) {
	key := signatureNonceKeyPrefix + nonce
	if common.RedisEnabled {
		// SetNX 带过期时间即原子的「SET NX EX」，等价于 SETNX + EXPIRE 且不会出现
		// 「占位成功但 EXPIRE 未执行 → key 永不过期」的中间态。
		return common.RDB.SetNX(context.Background(), key, 1, ttl).Result()
	}
	return inMemoryNonceConsume(key, ttl), nil
}

// abortWithSignatureError 输出统一的 401 错误结构，并留下安全审计线索。
func abortWithSignatureError(c *gin.Context, code string, message string) {
	if requestId := c.GetString(helper.RequestIdKey); requestId != "" {
		message = helper.MessageWithRequestId(message, requestId)
	}
	c.JSON(http.StatusUnauthorized, gin.H{
		"error": gin.H{
			"message": message,
			"type":    "one_api_error",
			"code":    code,
		},
	})
	c.Abort()
	// 被拒请求值得留痕（凭证泄露 / 客户端时钟错乱 / 攻击探测都靠这条线索发现）
	logger.SysWarnf("request signature rejected (%s): %s %s", code, c.Request.Method, c.Request.URL.Path)
}

// ---------- 内存降级实现（无 Redis 时使用） ----------

// memoryNonceStore nonce -> 过期时间，用互斥锁而非 sync.Map：
// 这里每次写入都可能伴随一次清理遍历，整体加锁比 LockOrStore + 分段清理更简单可靠。
type memoryNonceStore struct {
	mu    sync.Mutex
	items map[string]time.Time
}

var nonceStore = memoryNonceStore{items: make(map[string]time.Time)}

func inMemoryNonceConsume(key string, ttl time.Duration) bool {
	now := time.Now()
	nonceStore.mu.Lock()
	defer nonceStore.mu.Unlock()

	if expiresAt, ok := nonceStore.items[key]; ok && now.Before(expiresAt) {
		return false
	}
	if len(nonceStore.items) >= signatureNonceCleanupThreshold {
		for k, expiresAt := range nonceStore.items {
			if now.After(expiresAt) {
				delete(nonceStore.items, k)
			}
		}
	}
	nonceStore.items[key] = now.Add(ttl)
	return true
}
