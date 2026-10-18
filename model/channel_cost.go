package model

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xiaosu4610/LTZY-API/common/logger"
	billingratio "github.com/xiaosu4610/LTZY-API/relay/billing/ratio"
)

// 渠道级上游进价（LTZY-API 新增）。
//
// 计费包（relay/billing/ratio）只做查询，数据由这里推给它 ——
// 方向是 model → ratio，而 ratio 不引用 model，因此不会形成 import 环。
//
// 何时刷新：
//   - 进程启动时（main.go 调一次）
//   - 渠道新增 / 修改 / 删除后（渠道控制器里调）
//   - 批量导入后
//
// 渠道数量通常只有几十到几百，全量重建的成本可以忽略，
// 换来的是「不会出现某条渠道改了进价却没生效」这类难查的问题。
func RefreshChannelCostRatios() {
	var channels []*Channel
	if err := DB.Where("cost_ratio <> ''").Find(&channels).Error; err != nil {
		logger.SysError("failed to load channel cost ratios: " + err.Error())
		return
	}
	result := make(map[int]map[string]float64, len(channels))
	for _, channel := range channels {
		if strings.TrimSpace(channel.CostRatio) == "" {
			continue
		}
		parsed := map[string]float64{}
		if err := json.Unmarshal([]byte(channel.CostRatio), &parsed); err != nil {
			// 单条渠道配置写错不应影响其它渠道，跳过并留日志
			logger.SysError(fmt.Sprintf("channel #%d cost_ratio is not valid JSON, ignored: %s", channel.Id, err.Error()))
			continue
		}
		result[channel.Id] = parsed
	}
	billingratio.SetChannelCostRatios(result)
	logger.SysLog(fmt.Sprintf("channel cost ratios loaded: %d", len(result)))
}

// ValidateChannelCostRatio 校验渠道级进价 JSON，供渠道增改接口在落库前拦截脏配置。
func ValidateChannelCostRatio(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed := map[string]float64{}
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return fmt.Errorf("渠道进价必须是形如 {\"gpt-4o\":1.6,\"*\":2.0} 的 JSON：%s", err.Error())
	}
	return billingratio.ValidateCostRatioMap(parsed)
}
