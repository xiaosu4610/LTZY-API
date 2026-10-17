package ratio

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// ValidateReasoningRatioOption 校验管理员填写的 ReasoningRatio JSON（LTZY-API 新增）。
// 必须是「模型名 → 不小于 0 的数字」的对象，防止热更新把脏数据带进计费。
// 放在 ratio 包而不放 billing 包：model/option.go 需要在写入前调用它做校验，
// 而 billing 包引用了 model，从 model 引 billing 会形成 import cycle。
func ValidateReasoningRatioOption(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed := map[string]float64{}
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return err
	}
	for name, r := range parsed {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("模型名不能为空")
		}
		if math.IsNaN(r) || math.IsInf(r, 0) || r < 0 {
			return fmt.Errorf("模型 %s 的推理倍率必须是不小于 0 的数字", name)
		}
	}
	return nil
}
