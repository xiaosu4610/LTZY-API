package ratio

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/xiaosu4610/LTZY-API/common/logger"
)

// 上游成本核算（LTZY-API 新增，B7）。
//
// 目的：算出「上游实际成本」与「向用户收取的额度」之间的毛利。
// 计费侧的 ModelRatio 是**售价**，这里的 CostRatio 是**进价**（单位口径一致，
// 都是相对同一基准的倍率），因此 毛利 = 收入 − 成本 可直接相减。
//
// 配置形如（Option 名 CostRatio）：
//
//	{
//	  "gpt-4o": 1.6,
//	  "*": 2.0
//	}
//
// 语义：按模型名精确匹配；未配置的模型回退到 `*`（通配默认值）；
// 两者都没有时成本记为 0（表示"未知成本"）。
//
// ⚠️ 局限（有意为之，见文档）：成本按「模型」维度配置，**不区分渠道**。
// 同一模型从不同上游进货价不同时，本实现取同一进价；
// 若将来要按渠道区分，需要把渠道类型一起写入消费日志（当前 Log 只有 ChannelId）。
var (
	costRatioLock sync.RWMutex
	CostRatio     = map[string]float64{}
)

func CostRatio2JSONString() string {
	costRatioLock.RLock()
	defer costRatioLock.RUnlock()
	jsonBytes, err := json.Marshal(CostRatio)
	if err != nil {
		logger.SysError("error marshalling cost ratio: " + err.Error())
		return "{}"
	}
	return string(jsonBytes)
}

// UpdateCostRatioByJSONString 热更新成本倍率表。
// 与上游既有实现不同，这里先解析到临时变量、成功后再替换，
// 避免配置写错时把正在用的进价表清空（那会让毛利统计瞬间失真）。
func UpdateCostRatioByJSONString(jsonStr string) error {
	parsed := map[string]float64{}
	if strings.TrimSpace(jsonStr) != "" {
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
			return err
		}
	}
	if err := ValidateCostRatioMap(parsed); err != nil {
		return err
	}
	costRatioLock.Lock()
	defer costRatioLock.Unlock()
	CostRatio = parsed
	return nil
}

// ValidateCostRatioOption 校验管理员填写的成本倍率 JSON。
func ValidateCostRatioOption(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed := map[string]float64{}
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return err
	}
	return ValidateCostRatioMap(parsed)
}

// ValidateCostRatioMap 校验解析后的表：模型名非空、倍率为非负有限数。
func ValidateCostRatioMap(parsed map[string]float64) error {
	for model, ratio := range parsed {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("模型名不能为空")
		}
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
			return fmt.Errorf("模型 %s 的成本倍率必须是不小于 0 的数字", model)
		}
	}
	return nil
}

// GetCostRatio 取某模型的进价倍率；未配置时回退到通配键 `*`，都没有则返回 0。
func GetCostRatio(modelName string) float64 {
	costRatioLock.RLock()
	defer costRatioLock.RUnlock()
	if ratio, ok := CostRatio[modelName]; ok {
		return ratio
	}
	if ratio, ok := CostRatio["*"]; ok {
		return ratio
	}
	return 0
}

// ---- 渠道级进价（LTZY-API 新增）----
//
// 场景：同一模型从不同上游渠道进货价不同（例如某模型的官方价与某中转价差一倍），
// 全局 CostRatio 只能表达「按模型」一个价，无法体现渠道差异。
//
// 实现方式：由 model 包在启动时/渠道变更时把「渠道ID → {模型: 倍率}」推进来，
// 这里只做查询。方向是 model → ratio（model 已经引用 ratio），不会形成 import 环。
var (
	channelCostRatioLock sync.RWMutex
	channelCostRatio     = map[int]map[string]float64{}
)

// SetChannelCostRatios 全量替换渠道级进价表（由 model 包在渠道数据变化后调用）。
func SetChannelCostRatios(m map[int]map[string]float64) {
	channelCostRatioLock.Lock()
	defer channelCostRatioLock.Unlock()
	if m == nil {
		m = map[int]map[string]float64{}
	}
	channelCostRatio = m
}

// GetCostRatioForChannel 取「某渠道下某模型」的进价倍率。
// 查找顺序：渠道级精确匹配 → 渠道级通配 `*` → 全局 GetCostRatio。
func GetCostRatioForChannel(modelName string, channelID int) float64 {
	if channelID > 0 {
		channelCostRatioLock.RLock()
		perChannel, ok := channelCostRatio[channelID]
		channelCostRatioLock.RUnlock()
		if ok {
			if ratio, hit := perChannel[modelName]; hit {
				return ratio
			}
			if ratio, hit := perChannel["*"]; hit {
				return ratio
			}
		}
	}
	return GetCostRatio(modelName)
}

// HasChannelCostRatio 是否配置了任何渠道级进价（供热路径短路）。
func HasChannelCostRatio() bool {
	channelCostRatioLock.RLock()
	defer channelCostRatioLock.RUnlock()
	return len(channelCostRatio) > 0
}

// HasCostRatio 是否配置了任何进价（用于在结算热路径上做零开销短路）。
func HasCostRatio() bool {
	costRatioLock.RLock()
	defer costRatioLock.RUnlock()
	return len(CostRatio) > 0
}

// CalcCostQuota 计算一次消费对应的上游成本（额度单位）。
//
// 计算口径与售价保持同一形态：(prompt + completion × completionRatio) × costRatio，
// 这样「收入 − 成本」才是可比的毛利，而不是拿两个不同口径的数字相减。
// 未配置任何进价时返回 0（"未知成本"，而不是"零成本"）。
func CalcCostQuota(modelName string, promptTokens int, completionTokens int) int64 {
	return calcCostQuotaWithRatio(modelName, promptTokens, completionTokens, GetCostRatio(modelName))
}

// CalcCostQuotaForChannel 计算一次消费的上游成本，**优先采用渠道级进价**。
// 传 channelID<=0 时与 CalcCostQuota 等价。
func CalcCostQuotaForChannel(modelName string, promptTokens int, completionTokens int, channelID int) int64 {
	if !HasCostRatio() && !HasChannelCostRatio() {
		return 0
	}
	return calcCostQuotaWithRatio(modelName, promptTokens, completionTokens, GetCostRatioForChannel(modelName, channelID))
}

func calcCostQuotaWithRatio(modelName string, promptTokens int, completionTokens int, costRatio float64) int64 {
	if costRatio <= 0 {
		return 0
	}
	completionRatio := GetCompletionRatio(modelName, 0)
	// 与售价同一货币口径：成本倍率同样是美元基准，需与 CalcQuota 乘同一个换算系数，
	// 否则「收入 − 成本」会拿两个不同口径的数字相减，毛利统计彻底失真。
	cost := (float64(promptTokens) + float64(completionTokens)*completionRatio) * costRatio * RatioScale()
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost <= 0 {
		return 0
	}
	return int64(math.Ceil(cost))
}
