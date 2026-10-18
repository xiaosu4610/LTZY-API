package monitor

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/relay/model"
)

// isRateLimitStatus 判断该 HTTP 状态码是否属于「限流语义」（默认配置为 429）。
//
// 为什么需要它：很多上游把「限流」和「密钥失效」用同一个状态码表达。
// 最典型的是英伟达这类免费额度渠道——配额用尽或瞬时并发过高时都返回 429，
// 但密钥本身完全有效。若按状态码一刀切禁用渠道，会把还能用的渠道误杀。
func isRateLimitStatus(statusCode int) bool {
	if statusCode <= 0 {
		return false
	}
	for _, part := range strings.Split(config.RateLimitStatusCodes, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if code, err := strconv.Atoi(part); err == nil && code == statusCode {
			return true
		}
	}
	return false
}

// hasHardDeadSignal 判断错误体里是否出现「明确表示密钥不可再用」的硬信号。
// 这类信号无论状态码是什么都应当禁用渠道（例如 429 但响应体写着 insufficient quota）。
func hasHardDeadSignal(err *model.Error) bool {
	switch err.Type {
	case "insufficient_quota", "authentication_error", "permission_error", "forbidden":
		return true
	}
	if err.Code == "invalid_api_key" || err.Code == "account_deactivated" {
		return true
	}
	lowerMessage := strings.ToLower(err.Message)
	for _, keyword := range []string{
		"your access was terminated",
		"violation of our policies",
		"your credit balance is too low",
		"organization has been disabled",
		"credit",
		"balance",
		"permission denied",
		"organization has been restricted", // groq
		"api key not valid",                // gemini
		"api key expired",                  // gemini
		"已欠费",
	} {
		if strings.Contains(lowerMessage, keyword) {
			return true
		}
	}
	return false
}

func ShouldDisableChannel(err *model.Error, statusCode int) bool {
	if !config.AutomaticDisableChannelEnabled {
		return false
	}
	if err == nil {
		return false
	}
	// 限流语义的状态码（默认 429）：只有响应体出现「明确的密钥失效/欠费」信号才判死，
	// 否则一律按「临时限流」处理，只让本次请求失败/重试，不动渠道状态。
	if isRateLimitStatus(statusCode) {
		return hasHardDeadSignal(err)
	}
	if statusCode == http.StatusUnauthorized {
		return true
	}
	return hasHardDeadSignal(err)
}

func ShouldEnableChannel(err error, openAIErr *model.Error) bool {
	if !config.AutomaticEnableChannelEnabled {
		return false
	}
	if err != nil {
		return false
	}
	if openAIErr != nil {
		return false
	}
	return true
}
