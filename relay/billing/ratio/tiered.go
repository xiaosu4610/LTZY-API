package ratio

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/logger"
)

// 用量阶梯定价（LTZY-API 新增，B4）。
//
// 目标：同一模型按「用户本月累计已消费额度」自动进入不同价格档位，
// 用量越大单价越低（量价阶梯），用于替代"一刀切"的固定倍率。
//
// 配置形如（Option 名 TieredRatio）：
//
//	{
//	  "gpt-4o": [
//	    {"threshold": 0,       "ratio": 1.0},
//	    {"threshold": 1000000, "ratio": 0.8},
//	    {"threshold": 5000000, "ratio": 0.5}
//	  ]
//	}
//
// 语义：threshold 为「本月累计已消费额度」的下界，取最后一个满足
// usedQuota >= threshold 的档位倍率；未配置阶梯的模型恒为 1.0（不影响既有计费）。
//
// 放在 ratio 包而不是 billing 包：billing 引用了 model，而 model/option.go
// 需要在写入前调用本文件的校验函数，若校验函数放 billing 会形成 import cycle。
//
// 说明：套餐/包月属于商业规则（定价、续费、退款策略需产品决策），
// 本文件只实现与定价引擎强相关的量价阶梯部分。

// PricingTier 单个价格档位。
type PricingTier struct {
	Threshold int64   `json:"threshold"` // 该档生效的累计用量下界（额度）
	Ratio     float64 `json:"ratio"`     // 该档的定价倍率
}

var (
	tieredRatioCacheLock sync.RWMutex
	tieredRatioCache     = map[string][]PricingTier{}
	tieredRatioOptionRaw string
)

// parseTieredRatioOption 解析 TieredRatio Option，结果按 threshold 升序归一化后缓存。
// 缓存以「Option 原文」为键，配置变更后自动失效（与 ReasoningRatio 的处理一致）。
func parseTieredRatioOption(value string) map[string][]PricingTier {
	tieredRatioCacheLock.RLock()
	if tieredRatioOptionRaw == value {
		defer tieredRatioCacheLock.RUnlock()
		return tieredRatioCache
	}
	tieredRatioCacheLock.RUnlock()

	tieredRatioCacheLock.Lock()
	defer tieredRatioCacheLock.Unlock()
	if tieredRatioOptionRaw == value {
		return tieredRatioCache
	}
	parsed := map[string][]PricingTier{}
	tieredRatioOptionRaw = value
	if strings.TrimSpace(value) != "" {
		if err := json.Unmarshal([]byte(value), &parsed); err != nil {
			logger.SysError("error unmarshalling TieredRatio option: " + err.Error())
			parsed = map[string][]PricingTier{}
		}
	}
	// 归一化为升序，管理员不必按顺序书写
	for model, tiers := range parsed {
		sort.Slice(tiers, func(i, j int) bool { return tiers[i].Threshold < tiers[j].Threshold })
		parsed[model] = tiers
	}
	tieredRatioCache = parsed
	return tieredRatioCache
}

func readTieredRatioOption() string {
	config.OptionMapRWMutex.RLock()
	defer config.OptionMapRWMutex.RUnlock()
	return config.OptionMap["TieredRatio"]
}

// HasTieredRatio 是否配置了任何阶梯。
// 用于在计费热路径上做「零开销短路」：未配置时不去查用户累计用量。
func HasTieredRatio() bool {
	return len(parseTieredRatioOption(readTieredRatioOption())) > 0
}

// GetTieredRatio 取某模型在当前累计用量下的阶梯倍率。
// 未配置阶梯的模型返回 1.0，保证与既有计费完全一致。
func GetTieredRatio(modelName string, periodUsedQuota int64) float64 {
	tiers, ok := parseTieredRatioOption(readTieredRatioOption())[modelName]
	if !ok || len(tiers) == 0 {
		return 1.0
	}
	ratio := 1.0
	for _, tier := range tiers {
		if periodUsedQuota >= tier.Threshold {
			ratio = tier.Ratio
			continue
		}
		break // 列表已升序，后续档位只会更高
	}
	return ratio
}

// ValidateTieredRatioOption 校验管理员填写的阶梯配置。
// 拦截负数阈值、负倍率、重复阈值与空模型名，避免脏数据流进计费公式。
func ValidateTieredRatioOption(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed := map[string][]PricingTier{}
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return err
	}
	for model, tiers := range parsed {
		if strings.TrimSpace(model) == "" {
			return ratioOptionError("模型名不能为空")
		}
		seenThresholds := map[int64]bool{}
		for _, tier := range tiers {
			if tier.Threshold < 0 {
				return ratioOptionError("模型 " + model + " 的档位阈值不能为负数")
			}
			if tier.Ratio < 0 {
				return ratioOptionError("模型 " + model + " 的档位倍率不能为负数")
			}
			if seenThresholds[tier.Threshold] {
				return ratioOptionError("模型 " + model + " 存在重复的档位阈值")
			}
			seenThresholds[tier.Threshold] = true
		}
	}
	return nil
}

// ratioOptionError 倍率配置类错误（便于上层统一加前缀，不引入额外依赖）
type ratioOptionError string

func (e ratioOptionError) Error() string { return string(e) }
