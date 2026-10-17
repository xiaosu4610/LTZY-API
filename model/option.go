package model

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	billingratio "github.com/xiaosu4610/LTZY-API/relay/billing/ratio"
)

type Option struct {
	Key   string `json:"key" gorm:"primaryKey"`
	Value string `json:"value"`
}

// optionMapInitialized 标记「启动期的 OptionMap 装载」是否已结束（E9）。
//
// 为什么需要这个标记：个别配置键在运行时切换时需要额外副作用（当前是
// MemoryCacheEnabled —— 打开时必须立刻重建渠道缓存索引并补上启动流程才会创建的
// 同步协程），而这些副作用在启动期由 main.go 负责。若不加区分，启动期装载数据库
// 配置时就会与 main.go 各建一份协程、各做一次全量同步。
var optionMapInitialized atomic.Bool

// memoryCacheSyncOnce 保证内存缓存的同步协程最多只创建一次（E9）：
// 管理员把 MemoryCacheEnabled 关掉再打开时，不能把 SyncOptions/SyncChannelCache
// 叠加成多份 —— 叠加不会报错，但会让同步频率成倍放大，属于难排查的性能问题。
var memoryCacheSyncOnce sync.Once

func AllOption() ([]*Option, error) {
	var options []*Option
	var err error
	err = DB.Find(&options).Error
	return options, err
}

func InitOptionMap() {
	config.OptionMapRWMutex.Lock()
	config.OptionMap = make(map[string]string)
	config.OptionMap["PasswordLoginEnabled"] = strconv.FormatBool(config.PasswordLoginEnabled)
	config.OptionMap["PasswordRegisterEnabled"] = strconv.FormatBool(config.PasswordRegisterEnabled)
	config.OptionMap["EmailVerificationEnabled"] = strconv.FormatBool(config.EmailVerificationEnabled)
	config.OptionMap["GitHubOAuthEnabled"] = strconv.FormatBool(config.GitHubOAuthEnabled)
	config.OptionMap["OidcEnabled"] = strconv.FormatBool(config.OidcEnabled)
	config.OptionMap["WeChatAuthEnabled"] = strconv.FormatBool(config.WeChatAuthEnabled)
	config.OptionMap["TurnstileCheckEnabled"] = strconv.FormatBool(config.TurnstileCheckEnabled)
	config.OptionMap["RegisterEnabled"] = strconv.FormatBool(config.RegisterEnabled)
	config.OptionMap["AutomaticDisableChannelEnabled"] = strconv.FormatBool(config.AutomaticDisableChannelEnabled)
	config.OptionMap["AutomaticEnableChannelEnabled"] = strconv.FormatBool(config.AutomaticEnableChannelEnabled)
	config.OptionMap["ApproximateTokenEnabled"] = strconv.FormatBool(config.ApproximateTokenEnabled)
	config.OptionMap["LogConsumeEnabled"] = strconv.FormatBool(config.LogConsumeEnabled)
	config.OptionMap["DisplayInCurrencyEnabled"] = strconv.FormatBool(config.DisplayInCurrencyEnabled)
	config.OptionMap["DisplayTokenStatEnabled"] = strconv.FormatBool(config.DisplayTokenStatEnabled)
	config.OptionMap["ChannelDisableThreshold"] = strconv.FormatFloat(config.ChannelDisableThreshold, 'f', -1, 64)
	config.OptionMap["EmailDomainRestrictionEnabled"] = strconv.FormatBool(config.EmailDomainRestrictionEnabled)
	config.OptionMap["EmailDomainWhitelist"] = strings.Join(config.EmailDomainWhitelist, ",")
	config.OptionMap["SMTPServer"] = ""
	config.OptionMap["SMTPFrom"] = ""
	config.OptionMap["SMTPPort"] = strconv.Itoa(config.SMTPPort)
	config.OptionMap["SMTPAccount"] = ""
	config.OptionMap["SMTPToken"] = ""
	config.OptionMap["Notice"] = ""
	config.OptionMap["About"] = ""
	config.OptionMap["HomePageContent"] = ""
	config.OptionMap["Footer"] = config.Footer
	config.OptionMap["SystemName"] = config.SystemName
	config.OptionMap["Logo"] = config.Logo
	config.OptionMap["ServerAddress"] = ""
	config.OptionMap["GitHubClientId"] = ""
	config.OptionMap["GitHubClientSecret"] = ""
	config.OptionMap["WeChatServerAddress"] = ""
	config.OptionMap["WeChatServerToken"] = ""
	config.OptionMap["WeChatAccountQRCodeImageURL"] = ""
	config.OptionMap["MessagePusherAddress"] = ""
	config.OptionMap["MessagePusherToken"] = ""
	config.OptionMap["TurnstileSiteKey"] = ""
	config.OptionMap["TurnstileSecretKey"] = ""
	config.OptionMap["QuotaForNewUser"] = strconv.FormatInt(config.QuotaForNewUser, 10)
	config.OptionMap["QuotaForInviter"] = strconv.FormatInt(config.QuotaForInviter, 10)
	config.OptionMap["QuotaForInvitee"] = strconv.FormatInt(config.QuotaForInvitee, 10)
	config.OptionMap["QuotaRemindThreshold"] = strconv.FormatInt(config.QuotaRemindThreshold, 10)
	config.OptionMap["PreConsumedQuota"] = strconv.FormatInt(config.PreConsumedQuota, 10)
	config.OptionMap["ModelRatio"] = billingratio.ModelRatio2JSONString()
	config.OptionMap["GroupRatio"] = billingratio.GroupRatio2JSONString()
	config.OptionMap["CompletionRatio"] = billingratio.CompletionRatio2JSONString()
	// 分组×模型 二维定价矩阵（LTZY-API 新增，B3）：JSON 如 {"vip":{"gpt-4o":0.8}}
	config.OptionMap["ModelGroupRatio"] = billingratio.ModelGroupRatio2JSONString()
	// 上游成本倍率（LTZY-API 新增，B7）：JSON 如 {"gpt-4o":1.6,"*":2.0}
	config.OptionMap["CostRatio"] = billingratio.CostRatio2JSONString()
	// 内容审核敏感词（LTZY-API 新增，E3）：逗号/换行分隔；为空即关闭审核
	config.OptionMap["SensitiveWords"] = ""
	// 命中动作：block（默认，直接拒绝）或 log（只记日志，用于上线前观察误伤率）
	config.OptionMap["SensitiveWordsAction"] = "block"
	// Midjourney 上游代理（LTZY-API 新增，A4）：不填则 MJ 接口明确报"未配置"
	config.OptionMap["MidjourneyUpstreamURL"] = ""
	config.OptionMap["MidjourneyApiKey"] = ""
	// MJ 任务倍率：支持数字（1.5）或按动作区分（{"imagine":1.5,"upscale":0.5}）
	config.OptionMap["MidjourneyModelRatio"] = ""
	// 角色权限表（LTZY-API 新增，C4）：JSON 如 {"admin":["channel.read"],"20":["log.read"]}
	// 留空 = 使用与改造前完全一致的内置默认值
	config.OptionMap["RolePermissions"] = ""
	// 灰度路由（LTZY-API 新增，E10）：把一部分流量导向指定渠道，用于新上游试跑与渐进放量。
	// 灰度渠道必须已加入目标分组的渠道列表且 Models 含该模型，否则能力索引查不到、灰度不生效。
	config.OptionMap["CanaryEnabled"] = "false"
	config.OptionMap["CanaryChannelId"] = "0"
	config.OptionMap["CanaryPercent"] = "0"
	config.OptionMap["CanaryGroups"] = ""
	config.OptionMap["ReasoningRatio"] = "" // 推理 token 倍率（LTZY-API 新增）：JSON 如 {"o1":2.0}，空 = 未配置
	// 用量阶梯定价（LTZY-API 新增，B4）：JSON 如 {"gpt-4o":[{"threshold":0,"ratio":1.0}]}
	config.OptionMap["TieredRatio"] = ""
	// 多通知渠道（LTZY-API 新增，D5）：留空即跳过该渠道
	config.OptionMap["WebhookURL"] = ""
	config.OptionMap["LarkWebhookURL"] = ""
	config.OptionMap["DingTalkWebhookURL"] = ""
	config.OptionMap["DingTalkSecret"] = ""
	config.OptionMap["SlackWebhookURL"] = ""
	// 全局用户级预算默认值（LTZY-API 新增，B6）：单位与 quota 一致，0 = 不限制
	config.OptionMap["DefaultUserDailyQuotaLimit"] = "0"
	config.OptionMap["DefaultUserMonthlyQuotaLimit"] = "0"
	config.OptionMap["TopUpLink"] = config.TopUpLink
	config.OptionMap["ChatLink"] = config.ChatLink
	config.OptionMap["QuotaPerUnit"] = strconv.FormatFloat(config.QuotaPerUnit, 'f', -1, 64)
	config.OptionMap["RetryTimes"] = strconv.Itoa(config.RetryTimes)
	config.OptionMap["Theme"] = config.Theme
	// 内存缓存开关（E9 新增键）：原先只认环境变量 MEMORY_CACHE_ENABLED，
	// 管理员既看不到也改不了，且改了也没有任何同步逻辑（全局变量与键完全脱节）。
	// 注册成配置键后，GetOptions 能看到当前状态，UpdateOption 也能热切换。
	config.OptionMap["MemoryCacheEnabled"] = strconv.FormatBool(config.MemoryCacheEnabled)
	config.OptionMapRWMutex.Unlock()
	loadOptionsFromDatabase()
	// 标记装载结束：此后发生的配置变更是"运行中变更"，需要触发运行时副作用
	optionMapInitialized.Store(true)
}

func loadOptionsFromDatabase() {
	options, _ := AllOption()
	for _, option := range options {
		if option.Key == "ModelRatio" {
			option.Value = billingratio.AddNewMissingRatio(option.Value)
		}
		err := updateOptionMap(option.Key, option.Value)
		if err != nil {
			logger.SysError("failed to update option map: " + err.Error())
		}
	}
}

func SyncOptions(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		logger.SysLog("syncing options from database")
		loadOptionsFromDatabase()
	}
}

// UpdateOption 兼容旧调用点：不带操作者信息的配置写入（历史里 operator 记为匿名）。
// 管理面一律走 UpdateOptionWithOperator，匿名入口只为兼容既有调用方与单测。
func UpdateOption(key string, value string) error {
	_, err := UpdateOptionWithOperator(key, value, 0, "", "")
	return err
}

// UpdateOptionWithOperator 写入一条配置：先校验并热生效 → 落库 → 追加变更历史（E9）。
// 返回值为本次追加的历史记录（历史写入失败时 Id 为 0），回滚链路需要它来构造响应。
//
// 为什么把「校验并热生效」提到落库之前：TieredRatio / RolePermissions / ReasoningRatio
// 这类键自带校验，改造前是"先写库再校验"，于是接口返回失败、脏值却已经躺在 options 表里，
// 重启后还会被 loadOptionsFromDatabase 重新加载（表现为"说没成功，重启后却生效了，而且是坏值"）。
// 现在校验不通过就直接返回，库里不留痕；通过之后才落库，保证"库里存的 = 接口认可的"。
func UpdateOptionWithOperator(key string, value string, operatorId int, operatorName string, remark string) (*OptionHistory, error) {
	// 变更前的"活值"必须在热更新覆盖之前取，否则历史里的 old_value 会等于 new_value，
	// 回滚就再也回不到改前的状态了。
	oldValue, oldExisted := getOptionMapValue(key)

	if err := updateOptionMap(key, value); err != nil {
		return nil, err
	}

	// 落库（GORM 的 Save：主键已存在是 UPDATE，不存在是 INSERT，与改造前一致）
	option := Option{
		Key: key,
	}
	// https://gorm.io/docs/update.html#Save-All-Fields
	if err := DB.FirstOrCreate(&option, Option{Key: key}).Error; err != nil {
		revertOptionMapValue(key, oldValue, oldExisted)
		return nil, err
	}
	option.Value = value
	if err := DB.Save(&option).Error; err != nil {
		// 落库失败必须把内存值退回去：否则接口报失败、运行时却已是新值，
		// 而重启（或下一次 SyncOptions）会把它悄悄改回旧值，比直接报错更难排查。
		revertOptionMapValue(key, oldValue, oldExisted)
		return nil, err
	}

	// 追加变更历史。历史是旁路能力：写失败只记系统日志，不能反过来把配置写入搞失败
	// （与 D4 审计日志同一取舍）。回滚本身也走这条路径，因此链路天然可追溯。
	history := &OptionHistory{
		Key:          key,
		OldValue:     optionHistoryDisplayValue(key, oldValue),
		NewValue:     optionHistoryDisplayValue(key, value),
		OldValueRaw:  optionHistoryRawValue(key, oldValue),
		NewValueRaw:  optionHistoryRawValue(key, value),
		IsSensitive:  IsSensitiveOptionKey(key),
		IsRollback:   strings.HasPrefix(remark, optionRollbackRemarkPrefix),
		OperatorId:   operatorId,
		OperatorName: operatorName,
		Remark:       remark,
	}
	RecordOptionHistory(history)
	return history, nil
}

// getOptionMapValue 读取 OptionMap 中的当前值。
// 第二个返回值表示该键此前是否存在 —— 回滚时用它区分「值为空」与「键本来就没有」。
func getOptionMapValue(key string) (string, bool) {
	config.OptionMapRWMutex.RLock()
	defer config.OptionMapRWMutex.RUnlock()
	value, ok := config.OptionMap[key]
	return value, ok
}

// revertOptionMapValue 把内存中的配置值退回旧值（校验失败或落库失败后的自愈）。
// 为什么要连全局变量一起退：失败应当是原子的，只退 OptionMap 会留下
// "接口说失败、运行时已经换了新值"的半生效状态。
func revertOptionMapValue(key string, oldValue string, existed bool) {
	config.OptionMapRWMutex.Lock()
	if existed {
		config.OptionMap[key] = oldValue
	} else {
		delete(config.OptionMap, key)
	}
	config.OptionMapRWMutex.Unlock()
	if !existed {
		// 键原本不存在，说明没有可退回的旧值；此时各全局变量仍是原来的默认值，
		// 保持不动比用空串覆盖更安全。
		return
	}
	if err := applyOptionToRuntime(key, oldValue); err != nil {
		logger.SysError("failed to revert option " + key + ": " + err.Error())
	}
}

// updateOptionMap 更新内存 OptionMap，并把新值同步到运行时全局变量。
//
// 校验失败时把 OptionMap 退回旧值：原实现先写 OptionMap 再校验，一次失败的
// TieredRatio 提交会留下"接口说失败、内存里已是脏值"的状态，而脏值会被计费路径读到。
func updateOptionMap(key string, value string) error {
	config.OptionMapRWMutex.Lock()
	oldValue, existed := config.OptionMap[key]
	config.OptionMap[key] = value
	config.OptionMapRWMutex.Unlock()

	// 注意：applyOptionToRuntime 在锁外执行。它可能触发数据库全量同步
	// （MemoryCacheEnabled 打开时重建渠道缓存），而同步协程自己也要拿这把锁，
	// 持锁调用会形成自锁。
	if err := applyOptionToRuntime(key, value); err != nil {
		config.OptionMapRWMutex.Lock()
		if existed {
			config.OptionMap[key] = oldValue
		} else {
			delete(config.OptionMap, key)
		}
		config.OptionMapRWMutex.Unlock()
		return err
	}
	return nil
}

// applyOptionToRuntime 把配置值同步到真正生效的地方 —— config 包里的全局变量（E9）。
//
// 为什么需要这个函数：config.OptionMap 只是"配置的展示与持久化视图"，
// 运行时代码绝大多数读的是 config 包全局变量（LogConsumeEnabled、Theme、SMTP* …）。
// 只改 OptionMap 不改全局变量，就是"改了不生效、必须重启"的根因。
// 因此所有「键 → 全局变量」的映射全部集中在这里，以便：
//  1. 新增配置键时只有一处要改，漏改一眼可见；
//  2. 单测可以直接断言"改完键之后全局变量确实变了"；
//  3. 与 RestartRequiredOptions() 互补：本函数负责"把值搬进内存"，
//     而"值搬进去了、行为却仍要重启"（如 Theme 决定服务端静态目录）以及
//     "值根本搬不进去"（只认环境变量的键）两类，由那份清单显式告诉管理员。
//
// 返回值只用于"写入前校验"（例如 JSON 结构非法、端口号不是数字）：失败时调用方
// （updateOptionMap）会把 OptionMap 回滚，实现"坏配置不落地"。
func applyOptionToRuntime(key string, value string) (err error) {
	if strings.HasSuffix(key, "Enabled") {
		boolValue := value == "true"
		switch key {
		case "PasswordRegisterEnabled":
			config.PasswordRegisterEnabled = boolValue
		case "PasswordLoginEnabled":
			config.PasswordLoginEnabled = boolValue
		case "EmailVerificationEnabled":
			config.EmailVerificationEnabled = boolValue
		case "GitHubOAuthEnabled":
			config.GitHubOAuthEnabled = boolValue
		case "OidcEnabled":
			config.OidcEnabled = boolValue
		case "WeChatAuthEnabled":
			config.WeChatAuthEnabled = boolValue
		case "TurnstileCheckEnabled":
			config.TurnstileCheckEnabled = boolValue
		case "RegisterEnabled":
			config.RegisterEnabled = boolValue
		case "EmailDomainRestrictionEnabled":
			config.EmailDomainRestrictionEnabled = boolValue
		case "AutomaticDisableChannelEnabled":
			config.AutomaticDisableChannelEnabled = boolValue
		case "AutomaticEnableChannelEnabled":
			config.AutomaticEnableChannelEnabled = boolValue
		case "ApproximateTokenEnabled":
			config.ApproximateTokenEnabled = boolValue
		case "LogConsumeEnabled":
			config.LogConsumeEnabled = boolValue
		case "DisplayInCurrencyEnabled":
			config.DisplayInCurrencyEnabled = boolValue
		case "DisplayTokenStatEnabled":
			config.DisplayTokenStatEnabled = boolValue
		}
	}
	switch key {
	case "EmailDomainWhitelist":
		config.EmailDomainWhitelist = strings.Split(value, ",")
	case "SMTPServer":
		config.SMTPServer = value
	case "SMTPPort":
		// 端口解析失败不能静默变成 0：那会让邮件发送在运行期才报"连不上"，
		// 而管理员看到的是"保存成功"。直接拒绝，坏值不落地。
		intValue, parseErr := strconv.Atoi(value)
		if parseErr != nil {
			err = errors.New("SMTPPort 必须是数字")
			break
		}
		config.SMTPPort = intValue
	case "SMTPAccount":
		config.SMTPAccount = value
	case "SMTPFrom":
		config.SMTPFrom = value
	case "SMTPToken":
		config.SMTPToken = value
	case "ServerAddress":
		config.ServerAddress = value
	case "GitHubClientId":
		config.GitHubClientId = value
	case "GitHubClientSecret":
		config.GitHubClientSecret = value
	case "LarkClientId":
		config.LarkClientId = value
	case "LarkClientSecret":
		config.LarkClientSecret = value
	case "OidcClientId":
		config.OidcClientId = value
	case "OidcClientSecret":
		config.OidcClientSecret = value
	case "OidcWellKnown":
		config.OidcWellKnown = value
	case "OidcAuthorizationEndpoint":
		config.OidcAuthorizationEndpoint = value
	case "OidcTokenEndpoint":
		config.OidcTokenEndpoint = value
	case "OidcUserinfoEndpoint":
		config.OidcUserinfoEndpoint = value
	case "Footer":
		config.Footer = value
	case "SystemName":
		config.SystemName = value
	case "Logo":
		config.Logo = value
	case "WeChatServerAddress":
		config.WeChatServerAddress = value
	case "WeChatServerToken":
		config.WeChatServerToken = value
	case "WeChatAccountQRCodeImageURL":
		config.WeChatAccountQRCodeImageURL = value
	case "MessagePusherAddress":
		config.MessagePusherAddress = value
	case "MessagePusherToken":
		config.MessagePusherToken = value
	case "TurnstileSiteKey":
		config.TurnstileSiteKey = value
	case "TurnstileSecretKey":
		config.TurnstileSecretKey = value
	case "QuotaForNewUser":
		config.QuotaForNewUser, _ = strconv.ParseInt(value, 10, 64)
	case "QuotaForInviter":
		config.QuotaForInviter, _ = strconv.ParseInt(value, 10, 64)
	case "QuotaForInvitee":
		config.QuotaForInvitee, _ = strconv.ParseInt(value, 10, 64)
	case "QuotaRemindThreshold":
		config.QuotaRemindThreshold, _ = strconv.ParseInt(value, 10, 64)
	case "PreConsumedQuota":
		config.PreConsumedQuota, _ = strconv.ParseInt(value, 10, 64)
	case "RetryTimes":
		config.RetryTimes, _ = strconv.Atoi(value)
	case "ModelRatio":
		err = applyRatioOption(key, value, billingratio.UpdateModelRatioByJSONString, billingratio.ModelRatio2JSONString)
	case "GroupRatio":
		err = applyRatioOption(key, value, billingratio.UpdateGroupRatioByJSONString, billingratio.GroupRatio2JSONString)
	case "ModelGroupRatio":
		// 分组×模型 二维定价矩阵：解析失败或含负值时保留原矩阵，避免配置写错导致计费失控
		err = applyRatioOption(key, value, billingratio.UpdateModelGroupRatioByJSONString, billingratio.ModelGroupRatio2JSONString)
	case "CostRatio":
		// 上游成本倍率（B7）：同样在写入前校验，脏数据会让毛利报表失真
		err = applyRatioOption(key, value, billingratio.UpdateCostRatioByJSONString, billingratio.CostRatio2JSONString)
	case "RolePermissions":
		// 角色权限表（C4）：未知权限点直接拒绝，避免"配了却不生效"的诡异现象
		if validationErr := ValidateRolePermissionsOption(value); validationErr != nil {
			err = errors.Join(errors.New("RolePermissions 格式错误"), validationErr)
		}
	case "CompletionRatio":
		err = applyRatioOption(key, value, billingratio.UpdateCompletionRatioByJSONString, billingratio.CompletionRatio2JSONString)
	case "ReasoningRatio":
		// 写入前校验 JSON 结构与数值合法性，防止热更新把脏数据带进计费（LTZY-API 新增）。
		// 校验函数放在 ratio 包：billing 包引用了 model，从 model 引 billing 会成环。
		if validationErr := billingratio.ValidateReasoningRatioOption(value); validationErr != nil {
			err = errors.Join(errors.New("ReasoningRatio 格式错误"), validationErr)
		}
	case "TieredRatio":
		// 用量阶梯定价（LTZY-API 新增，B4）：拦截非法 JSON、负阈值/负倍率与重复阈值
		if validationErr := billingratio.ValidateTieredRatioOption(value); validationErr != nil {
			err = errors.Join(errors.New("TieredRatio 配置错误"), validationErr)
		}
	case "TopUpLink":
		config.TopUpLink = value
	case "ChatLink":
		config.ChatLink = value
	case "ChannelDisableThreshold":
		config.ChannelDisableThreshold, _ = strconv.ParseFloat(value, 64)
	case "QuotaPerUnit":
		config.QuotaPerUnit, _ = strconv.ParseFloat(value, 64)
	case "Theme":
		config.Theme = value
	case "MemoryCacheEnabled":
		// 为什么单独处理、不放进上面的通用 bool 分支：它除了赋值还需要副作用。
		// 只把开关改成 true 是不够的 —— 渠道缓存索引是启动流程建立的，运行中打开后
		// group2model2channels 仍是空表，选渠道路径会直接返回 channel not found，
		// 比不打开更糟。因此这里在"关 → 开"的瞬间重建索引（见 ensureMemoryCacheRuntime）。
		enabled := value == "true"
		previous := config.MemoryCacheEnabled
		config.MemoryCacheEnabled = enabled
		if enabled && !previous && optionMapInitialized.Load() && DB != nil {
			ensureMemoryCacheRuntime()
		}
	}
	return err
}

// applyRatioOption 处理"整表替换型"的倍率配置：失败时把旧表写回去（E9）。
//
// 为什么需要这层包装：ratio 包里几个 Update*ByJSONString 采用"先清空全局 map、
// 再反序列化"的写法（upstream 遗留），一次非法 JSON 就会把正在计费的倍率表清空 ——
// 表现为全站模型悄悄回落到默认倍率，而接口只返回一句"格式错误"，管理员很难把两者
// 联系起来。这里在调用前取一份当前表的快照，失败即写回，语义上等价于"什么都没发生"。
// （不直接改 ratio 包：relay/** 正在并行开发。）
func applyRatioOption(key string, value string, update func(string) error, snapshot func() string) error {
	previous := snapshot()
	if err := update(value); err != nil {
		if restoreErr := update(previous); restoreErr != nil {
			logger.SysError("failed to restore " + key + " after rejecting invalid value: " + restoreErr.Error())
		}
		return err
	}
	return nil
}

// ensureMemoryCacheRuntime 让「运行中打开内存缓存」真正可用（E9）。
//
// 做两件事：
//  1. 立刻全量重建一次渠道缓存索引，避免"开关打开、索引为空"的窗口期；
//  2. 补上启动流程才会创建的同步协程（否则索引只有这一次快照，之后永远不刷新）。
//
// 关闭方向不需要任何副作用：各读取点都会退化成直接查库（见 model/cache.go），
// 同步协程即使继续跑也只是周期性做一次无人读取的重建，无正确性风险。
func ensureMemoryCacheRuntime() {
	InitChannelCache()
	memoryCacheSyncOnce.Do(func() {
		logger.SysLog("memory cache enabled at runtime, starting option/channel sync")
		go SyncOptions(config.SyncFrequency)
		go SyncChannelCache(config.SyncFrequency)
	})
}
