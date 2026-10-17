package ratio

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xiaosu4610/LTZY-API/common/logger"
	"sync"
)

var groupRatioLock sync.RWMutex
var GroupRatio = map[string]float64{
	"default": 1,
	"vip":     1,
	"svip":    1,
}

func GroupRatio2JSONString() string {
	jsonBytes, err := json.Marshal(GroupRatio)
	if err != nil {
		logger.SysError("error marshalling model ratio: " + err.Error())
	}
	return string(jsonBytes)
}

func UpdateGroupRatioByJSONString(jsonStr string) error {
	groupRatioLock.Lock()
	defer groupRatioLock.Unlock()
	GroupRatio = make(map[string]float64)
	return json.Unmarshal([]byte(jsonStr), &GroupRatio)
}

func GetGroupRatio(name string) float64 {
	groupRatioLock.RLock()
	defer groupRatioLock.RUnlock()
	ratio, ok := GroupRatio[name]
	if !ok {
		logger.SysError("group ratio not found: " + name)
		return 1
	}
	return ratio
}

// ---------- 分组 × 模型 二维定价矩阵（LTZY-API 新增，B3） ----------

// ModelGroupRatio 二维定价矩阵：分组 → 模型 → 倍率。
// JSON 形如 {"vip":{"gpt-4o":0.8,"claude-3-5-sonnet":0.9}}，
// 用于给「某个分组下的某个模型」单独定价，例如 VIP 组用 gpt-4o 打八折。
//
// 设计说明：未命中矩阵的组合一律回退到一维 GroupRatio，
// 因此空矩阵（默认）与改造前的行为**完全一致**，不会影响既有部署。
var (
	modelGroupRatioLock sync.RWMutex
	ModelGroupRatio     = map[string]map[string]float64{}
)

func ModelGroupRatio2JSONString() string {
	modelGroupRatioLock.RLock()
	defer modelGroupRatioLock.RUnlock()
	jsonBytes, err := json.Marshal(ModelGroupRatio)
	if err != nil {
		logger.SysError("error marshalling model group ratio: " + err.Error())
		return "{}"
	}
	return string(jsonBytes)
}

// UpdateModelGroupRatioByJSONString 热更新二维定价矩阵。
// 与上游既有的 UpdateGroupRatioByJSONString 不同，这里先解析到临时变量、
// 成功后才替换，避免配置写错时把正常运行的矩阵清空。
func UpdateModelGroupRatioByJSONString(jsonStr string) error {
	parsed := map[string]map[string]float64{}
	if strings.TrimSpace(jsonStr) != "" {
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
			return err
		}
	}
	if err := validateModelGroupRatio(parsed); err != nil {
		return err
	}
	modelGroupRatioLock.Lock()
	defer modelGroupRatioLock.Unlock()
	ModelGroupRatio = parsed
	return nil
}

// validateModelGroupRatio 校验矩阵内容：分组名/模型名非空、倍率为非负数。
// 放在写入前拦截脏数据，避免 NaN/负数流进计费公式。
func validateModelGroupRatio(matrix map[string]map[string]float64) error {
	for group, byModel := range matrix {
		if strings.TrimSpace(group) == "" {
			return fmt.Errorf("分组名不能为空")
		}
		for model, ratio := range byModel {
			if strings.TrimSpace(model) == "" {
				return fmt.Errorf("分组 %s 下的模型名不能为空", group)
			}
			if ratio < 0 {
				return fmt.Errorf("分组 %s 模型 %s 的倍率不能为负数", group, model)
			}
		}
	}
	return nil
}

// GetGroupRatioForModel 取「分组 + 模型」的生效倍率（B3 核心入口）。
// 优先查二维矩阵，未命中回退一维 GroupRatio —— 与改造前行为一致。
func GetGroupRatioForModel(group string, model string) float64 {
	// 先释放矩阵锁再走回退路径，避免与 groupRatioLock 形成嵌套加锁
	modelGroupRatioLock.RLock()
	byModel, ok := ModelGroupRatio[group]
	if ok {
		if ratio, hit := byModel[model]; hit {
			modelGroupRatioLock.RUnlock()
			return ratio
		}
	}
	modelGroupRatioLock.RUnlock()
	return GetGroupRatio(group)
}

