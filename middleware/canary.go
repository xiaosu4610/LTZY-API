package middleware

import (
	"context"
	"math/rand"
	"strconv"
	"strings"

	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"github.com/xiaosu4610/LTZY-API/model"
)

// 灰度路由（LTZY-API 新增，E10）：把一部分流量定向到某个渠道，用于新上游试跑、
// 新模型验证、渠道切换时的渐进放量。
//
// 设计原则：灰度是「锦上添花」的能力，链路上任何一步不成立都必须静默回退到
// 原有选渠道逻辑。管理员把键名写错、渠道填错、比例填错，最坏结果只能是灰度
// 不生效，绝不能让请求失败——否则一个可选的灰度开关会变成全局故障源。
//
// 配置全部从内存 config.OptionMap 读取，管理员在后台改完即时生效：既不需要
// 重启，也不需要为每次请求查一次数据库。键名见下方常量；未注册的键一样能读到，
// 注册（写入默认值）由 model/option.go 的维护者补上。

const (
	// OptionCanaryEnabled 灰度总开关，取值 "true" 才启用
	OptionCanaryEnabled = "CanaryEnabled"
	// OptionCanaryChannelId 灰度渠道 ID，必须是一个真实存在且启用的渠道
	OptionCanaryChannelId = "CanaryChannelId"
	// OptionCanaryPercent 灰度比例，0-100 的整数，>=100 等价于「全部走灰度渠道」
	OptionCanaryPercent = "CanaryPercent"
	// OptionCanaryGroups 只对这些分组生效，逗号分隔；留空表示全部分组生效
	OptionCanaryGroups = "CanaryGroups"
)

// canaryRand 抽象随机源（返回 [0,n) 的整数）。
//
// 必须做成可替换的包内变量：单测要断言「0% 一定不命中、100% 一定命中」这类
// 边界，直接调用 rand 的话断言会随概率抖动，只能写出不可靠的测试。
var canaryRand = rand.Intn

// canaryChannelSupportsModel 抽象「渠道能否承载该模型」的判定。
// 单测替换为内存实现，避免为了一个灰度判定去拉起数据库。
var canaryChannelSupportsModel = model.ChannelSupportsModel

// canaryGetChannel 抽象按 ID 取渠道（selectAll=true，灰度渠道需要 key 才能转发）。
var canaryGetChannel = model.GetChannelById

// canaryOptions 一次性读取灰度配置快照（已去掉首尾空白）。
// OptionMap 会被后台任务（model.loadOptionsFromDatabase）整体替换，必须与写侧
// 共用同一把锁读取，否则热更新与请求并发时会产生数据竞争；一次加锁取全量，
// 也避免每读一个键就锁一次。
func canaryOptions() (enabled string, channelId string, percent string, groups string) {
	config.OptionMapRWMutex.RLock()
	defer config.OptionMapRWMutex.RUnlock()
	if config.OptionMap == nil {
		return "", "", "", ""
	}
	return strings.TrimSpace(config.OptionMap[OptionCanaryEnabled]),
		strings.TrimSpace(config.OptionMap[OptionCanaryChannelId]),
		strings.TrimSpace(config.OptionMap[OptionCanaryPercent]),
		strings.TrimSpace(config.OptionMap[OptionCanaryGroups])
}

// canaryConfig 解析灰度配置，ok=false 表示本次请求不适用灰度，调用方走原逻辑。
//
// 所有键都做容错解析：解析失败（管理员填了非数字、开关写了 "True"、比例超范围）
// 一律视为「灰度不可用」，而不是报错。
func canaryConfig(group string) (channelId int, percent int, ok bool) {
	enabled, rawChannelId, rawPercent, rawGroups := canaryOptions()
	// 开关比较忽略大小写与空白，"True"/" true " 同样认，避免大小写导致开关失灵
	if !strings.EqualFold(enabled, "true") {
		return 0, 0, false
	}
	channelId, err := strconv.Atoi(rawChannelId)
	if err != nil || channelId <= 0 {
		return 0, 0, false
	}
	percent, err = strconv.Atoi(rawPercent)
	if err != nil || percent <= 0 {
		return 0, 0, false
	}
	if percent > 100 {
		// 比例写超 100 时按全量处理：管理员的意图显然是「全部走灰度渠道」
		percent = 100
	}
	if !canaryGroupMatched(rawGroups, group) {
		return 0, 0, false
	}
	return channelId, percent, true
}

// canaryGroupMatched 分组过滤：配置为空表示全部分组；比较时忽略首尾空白与大小写，
// 并兼容中文逗号/换行/分号——管理员在后台手填分组名时这些写法都很常见，
// 因为分隔符写错就静默不生效会非常难排查。
func canaryGroupMatched(rawGroups string, group string) bool {
	if strings.TrimSpace(rawGroups) == "" {
		return true
	}
	group = strings.TrimSpace(group)
	for _, item := range strings.FieldsFunc(rawGroups, func(r rune) bool {
		return r == ',' || r == '，' || r == '\n' || r == '\r' || r == ';'
	}) {
		if strings.EqualFold(strings.TrimSpace(item), group) {
			return true
		}
	}
	return false
}

// pickCanaryChannel 返回本次请求应当使用的灰度渠道；返回 nil 表示按原有逻辑选渠道。
//
// 命中灰度需要同时满足：开关打开、渠道 ID 合法、比例 > 0、分组命中、
// 概率命中、渠道确实支持当前模型、渠道存在且启用。任何一步失败都静默返回 nil。
func pickCanaryChannel(ctx context.Context, group string, modelName string) *model.Channel {
	channelId, percent, ok := canaryConfig(group)
	if !ok {
		return nil
	}
	// 先掷骰子再做渠道校验：未命中灰度的请求占绝大多数，只需一次随机数即可退出，
	// 不必为它们白跑一次能力查询。
	if canaryRand(100) >= percent {
		return nil
	}
	// 关键校验：灰度渠道未必支持当前请求的模型（新渠道常常只上了部分模型）。
	// 少了这一步，命中灰度的请求会被打到不支持该模型的上游上直接报错。
	if !canaryChannelSupportsModel(group, modelName, channelId) {
		logger.Debugf(ctx, "canary: 渠道 #%d 不支持模型 %s（分组 %s），回退原选渠道逻辑", channelId, modelName, group)
		return nil
	}
	channel, err := canaryGetChannel(channelId, true)
	if err != nil || channel == nil {
		logger.Debugf(ctx, "canary: 读取渠道 #%d 失败（%v），回退原选渠道逻辑", channelId, err)
		return nil
	}
	// 能力索引与渠道状态理论上同源，但渠道缓存是按周期同步的，状态可能短暂过期；
	// 这里再校验一次，避免把流量引到刚被停用的渠道上。
	if channel.Status != model.ChannelStatusEnabled {
		logger.Debugf(ctx, "canary: 渠道 #%d 当前未启用，回退原选渠道逻辑", channelId)
		return nil
	}
	logger.Debugf(ctx, "canary: 请求命中灰度，模型 %s（分组 %s）→ 渠道 #%d，比例 %d%%", modelName, group, channelId, percent)
	return channel
}
