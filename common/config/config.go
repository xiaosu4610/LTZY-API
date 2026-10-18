package config

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xiaosu4610/LTZY-API/common/env"

	"github.com/google/uuid"
)

var SystemName = "LTZY-API"
var ServerAddress = "http://localhost:3000"
var Footer = ""
var Logo = ""
var TopUpLink = ""
var ChatLink = ""
// QuotaPerUnit 表示「1 个货币单位等于多少 quota」。
// LTZY-API 统一按人民币计价：1 元 = 1,000,000 quota（与原系统口径一致，便于迁移）。
var QuotaPerUnit = 1000 * 1000.0
var DisplayInCurrencyEnabled = true
var DisplayTokenStatEnabled = true

// Any options with "Secret", "Token" in its key won't be return by GetOptions

var SessionSecret = uuid.New().String()

var OptionMap map[string]string
var OptionMapRWMutex sync.RWMutex

var ItemsPerPage = 10
var MaxRecentItems = 100

var PasswordLoginEnabled = true
var PasswordRegisterEnabled = true
var EmailVerificationEnabled = false
// 登录邮箱验证码（LTZY-API 新增）：开启后用户可用「邮箱 + 验证码」免密登录。
// 与 PasswordLoginEnabled 相互独立，二者可同时开启或分别关闭。
var EmailCodeLoginEnabled = true

// ---- 渠道自动禁用语义 / 保活检测（LTZY-API 新增）----

// RateLimitStatusCodes 「限流语义」的 HTTP 状态码，逗号分隔（默认 429）。
// 命中这些状态码时，除非响应体出现明确的「密钥失效 / 欠费」信号，否则不自动禁用渠道。
// 典型场景：英伟达等免费额度渠道返回 429 只代表「此刻请求过多」，密钥仍然可用，
// 若按状态码一刀切判死，会把正常渠道误杀。
var RateLimitStatusCodes = "429"

// ChannelKeepAliveEnabled 渠道保活检测总开关。
var ChannelKeepAliveEnabled = false

// ChannelKeepAliveMinutes 保活检测周期（分钟），<=0 时按 10 分钟处理。
var ChannelKeepAliveMinutes = 10

// ChannelKeepAliveScope 保活检测范围：
//   - disabled：只检测已被自动/手动禁用的渠道，能救活就自动恢复（默认，开销最小）
//   - all：检测全部渠道
var ChannelKeepAliveScope = "disabled"

// ---- 易支付（epay）在线充值（LTZY-API 新增）----

var EpayEnabled = false
var EpayApiUrl = ""             // 网关地址，如 https://xnoo.cn/
var EpayPid = ""                // 商户 ID
var EpayMerchantPrivateKey = "" // 商户私钥（PKCS#1 / PKCS#8 均可，带不带 PEM 头尾都行）
var EpayPlatformPublicKey = ""  // 平台公钥（用于异步通知验签）
var EpaySignType = "RSA"        // RSA（实际为 SHA256withRSA）/ MD5
var EpayDefaultType = "alipay"  // 默认支付方式：alipay / wxpay / qqpay
// EpayMinTopup 单笔最低充值金额（元）。默认 0.01，即人民币可流通的最小单位。
var EpayMinTopup = 0.01
var EpaySitename = ""           // 网站名，部分平台要求回传

// 支付渠道费率（LTZY-API 新增）：平台/通道抽取的手续费比例。
// 下单时按费率算出「手续费」与「实收金额」落到订单上，用于成本核算与对账。
var EpayAlipayFeeRate = 0.05 // 支付宝 5%
var EpayWxpayFeeRate = 0.06  // 微信 6%

// EpayFeeDeductFromQuota 为 true 时，手续费由用户承担：
// 充值 100 元、微信 6% → 只到账 94 元对应的额度。
// 默认 false，即手续费由平台承担，用户实充 100 元就按 100 元到账。
var EpayFeeDeductFromQuota = false
var GitHubOAuthEnabled = false
var OidcEnabled = false
var WeChatAuthEnabled = false
var TurnstileCheckEnabled = false
var RegisterEnabled = true

var EmailDomainRestrictionEnabled = false
var EmailDomainWhitelist = []string{
	"gmail.com",
	"163.com",
	"126.com",
	"qq.com",
	"outlook.com",
	"hotmail.com",
	"icloud.com",
	"yahoo.com",
	"foxmail.com",
}

var DebugEnabled = strings.ToLower(os.Getenv("DEBUG")) == "true"
var DebugSQLEnabled = strings.ToLower(os.Getenv("DEBUG_SQL")) == "true"
var MemoryCacheEnabled = strings.ToLower(os.Getenv("MEMORY_CACHE_ENABLED")) == "true"

var LogConsumeEnabled = true

var SMTPServer = ""
var SMTPPort = 587
var SMTPAccount = ""
var SMTPFrom = ""
var SMTPToken = ""

var GitHubClientId = ""
var GitHubClientSecret = ""

var LarkClientId = ""
var LarkClientSecret = ""

var OidcClientId = ""
var OidcClientSecret = ""
var OidcWellKnown = ""
var OidcAuthorizationEndpoint = ""
var OidcTokenEndpoint = ""
var OidcUserinfoEndpoint = ""

var WeChatServerAddress = ""
var WeChatServerToken = ""
var WeChatAccountQRCodeImageURL = ""

var MessagePusherAddress = ""
var MessagePusherToken = ""

var TurnstileSiteKey = ""
var TurnstileSecretKey = ""

var QuotaForNewUser int64 = 0
var QuotaForInviter int64 = 0
var QuotaForInvitee int64 = 0
var ChannelDisableThreshold = 5.0
var AutomaticDisableChannelEnabled = false
var AutomaticEnableChannelEnabled = false
var QuotaRemindThreshold int64 = 1000
var PreConsumedQuota int64 = 500
var ApproximateTokenEnabled = false
var RetryTimes = 0

var RootUserEmail = ""

var IsMasterNode = os.Getenv("NODE_TYPE") != "slave"

var requestInterval, _ = strconv.Atoi(os.Getenv("POLLING_INTERVAL"))
var RequestInterval = time.Duration(requestInterval) * time.Second

var SyncFrequency = env.Int("SYNC_FREQUENCY", 10*60) // unit is second

var BatchUpdateEnabled = false
var BatchUpdateInterval = env.Int("BATCH_UPDATE_INTERVAL", 5)

var RelayTimeout = env.Int("RELAY_TIMEOUT", 0) // unit is second

var GeminiSafetySetting = env.String("GEMINI_SAFETY_SETTING", "BLOCK_NONE")

var Theme = env.String("THEME", "default")
var ValidThemes = map[string]bool{
	// LTZY-API 起前端收敛为单一现代化工程，产物目录固定为 web/build/default
	"default": true,
}

// All duration's unit is seconds
// Shouldn't larger then RateLimitKeyExpirationDuration
var (
	GlobalApiRateLimitNum            = env.Int("GLOBAL_API_RATE_LIMIT", 480)
	GlobalApiRateLimitDuration int64 = 3 * 60

	GlobalWebRateLimitNum            = env.Int("GLOBAL_WEB_RATE_LIMIT", 240)
	GlobalWebRateLimitDuration int64 = 3 * 60

	UploadRateLimitNum            = 10
	UploadRateLimitDuration int64 = 60

	DownloadRateLimitNum            = 10
	DownloadRateLimitDuration int64 = 60

	CriticalRateLimitNum            = 20
	CriticalRateLimitDuration int64 = 20 * 60
)

var RateLimitKeyExpirationDuration = 20 * time.Minute

var EnableMetric = env.Bool("ENABLE_METRIC", false)
var MetricQueueSize = env.Int("METRIC_QUEUE_SIZE", 10)
var MetricSuccessRateThreshold = env.Float64("METRIC_SUCCESS_RATE_THRESHOLD", 0.8)
var MetricSuccessChanSize = env.Int("METRIC_SUCCESS_CHAN_SIZE", 1024)
var MetricFailChanSize = env.Int("METRIC_FAIL_CHAN_SIZE", 128)

var InitialRootToken = os.Getenv("INITIAL_ROOT_TOKEN")

var InitialRootAccessToken = os.Getenv("INITIAL_ROOT_ACCESS_TOKEN")

var GeminiVersion = env.String("GEMINI_VERSION", "v1")

var OnlyOneLogFile = env.Bool("ONLY_ONE_LOG_FILE", false)

var RelayProxy = env.String("RELAY_PROXY", "")
var UserContentRequestProxy = env.String("USER_CONTENT_REQUEST_PROXY", "")
var UserContentRequestTimeout = env.Int("USER_CONTENT_REQUEST_TIMEOUT", 30)

var EnforceIncludeUsage = env.Bool("ENFORCE_INCLUDE_USAGE", false)
var TestPrompt = env.String("TEST_PROMPT", "Output only your specific model name with no additional text.")

// CorsAllowOrigins 跨域白名单（LTZY-API 新增）。
// 逗号分隔的域名列表，例如 "https://ltzy.top,https://admin.ltzy.top"；
// 留空表示沿用历史行为（允许所有来源），便于开箱即用与平滑迁移。
var CorsAllowOrigins = env.String("CORS_ALLOW_ORIGINS", "")
