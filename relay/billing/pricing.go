package billing

import (
	"encoding/json"
	"math"
	"sync"

	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"github.com/xiaosu4610/LTZY-API/relay/billing/ratio"
)

// PricingMode 计费模式
type PricingMode string

const (
	PricingModeToken        PricingMode = "token"         // 按 token 计费
	PricingModePerRequest   PricingMode = "per_request"   // 按次计费（图片/视频/rerank）
	PricingModePerCharacter PricingMode = "per_character" // 按字符（TTS）
	PricingModePerSecond    PricingMode = "per_second"    // 按秒（视频时长）
)

// PricingModel 统一定价模型
type PricingModel struct {
	Mode            PricingMode // 计费模式
	ModelRatio      float64     // 模型倍率
	CompletionRatio float64     // 补全倍率
	PromptRatio     float64     // 输入倍率，默认 1.0
	ReasoningRatio  float64     // 推理 token 倍率
	PerUnitQuota    float64     // 按次/按秒/按字符的单价
}

// UsageInput 一次请求的用量输入
type UsageInput struct {
	PromptTokens     int     // 输入 token
	CompletionTokens int     // 输出 token
	ReasoningTokens  int     // 推理 token（属于输出 token 的一部分）
	Count            int     // 次数（图片张数/rerank 次数等）
	Characters       int     // 字符数（TTS）
	Seconds          float64 // 时长秒数（视频）
}

// CalcQuota 根据定价模型计算本次请求消耗的额度
func (m PricingModel) CalcQuota(input UsageInput, groupRatio float64) int64 {
	var quota float64
	switch m.Mode {
	case PricingModePerRequest:
		quota = float64(input.Count) * m.PerUnitQuota * groupRatio
	case PricingModePerCharacter:
		quota = float64(input.Characters) * m.PerUnitQuota * groupRatio
	case PricingModePerSecond:
		quota = input.Seconds * m.PerUnitQuota * groupRatio
	default:
		// 按 token 计费：兼容旧逻辑 (prompt + completion*completionRatio) * modelRatio * groupRatio
		if m.ModelRatio == 0 {
			// 倍率为 0（如免费模型）时无需计费
			return 0
		}
		promptRatio := m.PromptRatio
		if promptRatio == 0 {
			promptRatio = 1.0
		}
		completionRatio := m.CompletionRatio
		reasoningRatio := m.ReasoningRatio
		if reasoningRatio == 0 {
			// reasoning 倍率未配置时回退到补全倍率
			reasoningRatio = completionRatio
		}
		// 推理 token 包含在 completion token 中，按 reasoningRatio 替换其 completionRatio 部分
		completionPart := float64(input.CompletionTokens)*completionRatio +
			float64(input.ReasoningTokens)*(reasoningRatio-completionRatio)
		if completionPart < 0 {
			completionPart = 0
		}
		quota = (float64(input.PromptTokens)*promptRatio + completionPart) * m.ModelRatio * groupRatio
		if math.IsNaN(quota) || math.IsInf(quota, 0) || quota <= 0 {
			return 0
		}
		// 与现有逻辑一致：结果不足 1 时按 1 计算
		if quota < 1 {
			quota = 1
		}
		return int64(math.Ceil(quota))
	}
	if math.IsNaN(quota) || math.IsInf(quota, 0) || quota < 0 {
		return 0
	}
	if m.PerUnitQuota == 0 {
		return 0
	}
	// 与现有逻辑一致：结果不足 1 时按 1 计算
	if quota < 1 {
		quota = 1
	}
	return int64(math.Ceil(quota))
}

// GetPricingModel 从既有倍率表推导默认的 token 计费模型
func GetPricingModel(modelName string, channelType int) PricingModel {
	return PricingModel{
		Mode:            PricingModeToken,
		ModelRatio:      ratio.GetModelRatio(modelName, channelType),
		CompletionRatio: ratio.GetCompletionRatio(modelName, channelType),
		PromptRatio:     1.0,
		ReasoningRatio:  GetReasoningRatio(modelName),
	}
}

var (
	reasoningRatioCacheLock sync.RWMutex
	reasoningRatioCache     = make(map[string]float64) // 缓存解析后的 ReasoningRatio Option
	reasoningRatioOptionRaw string                     // 已解析的 Option 原文
)

// parseReasoningRatioOption 解析 ReasoningRatio Option（JSON map，如 {"o1":2.0}），结果缓存在内存中
func parseReasoningRatioOption(value string) map[string]float64 {
	reasoningRatioCacheLock.RLock()
	if reasoningRatioOptionRaw == value {
		defer reasoningRatioCacheLock.RUnlock()
		return reasoningRatioCache
	}
	reasoningRatioCacheLock.RUnlock()

	reasoningRatioCacheLock.Lock()
	defer reasoningRatioCacheLock.Unlock()
	if reasoningRatioOptionRaw == value {
		return reasoningRatioCache
	}
	parsed := make(map[string]float64)
	reasoningRatioOptionRaw = value
	reasoningRatioCache = parsed
	if value == "" {
		return parsed
	}
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		// 解析失败时回退为空表（即使用默认值）
		logger.SysError("error unmarshalling ReasoningRatio option: " + err.Error())
		reasoningRatioCache = make(map[string]float64)
	}
	return reasoningRatioCache
}

// GetReasoningRatio 获取推理 token 倍率：优先读取 DB Option，未配置时回退默认值 1.0
func GetReasoningRatio(modelName string) float64 {
	config.OptionMapRWMutex.RLock()
	optionValue := config.OptionMap["ReasoningRatio"]
	config.OptionMapRWMutex.RUnlock()

	ratios := parseReasoningRatioOption(optionValue)
	if r, ok := ratios[modelName]; ok {
		return r
	}
	return 1.0
}
