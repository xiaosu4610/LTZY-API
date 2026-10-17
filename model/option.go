package model

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	billingratio "github.com/xiaosu4610/LTZY-API/relay/billing/ratio"
)

type Option struct {
	Key   string `json:"key" gorm:"primaryKey"`
	Value string `json:"value"`
}

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
	config.OptionMapRWMutex.Unlock()
	loadOptionsFromDatabase()
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

func UpdateOption(key string, value string) error {
	// Save to database first
	option := Option{
		Key: key,
	}
	// https://gorm.io/docs/update.html#Save-All-Fields
	DB.FirstOrCreate(&option, Option{Key: key})
	option.Value = value
	// Save is a combination function.
	// If save value does not contain primary key, it will execute Create,
	// otherwise it will execute Update (with all fields).
	DB.Save(&option)
	// Update OptionMap
	return updateOptionMap(key, value)
}

func updateOptionMap(key string, value string) (err error) {
	config.OptionMapRWMutex.Lock()
	defer config.OptionMapRWMutex.Unlock()
	config.OptionMap[key] = value
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
		intValue, _ := strconv.Atoi(value)
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
		err = billingratio.UpdateModelRatioByJSONString(value)
	case "GroupRatio":
		err = billingratio.UpdateGroupRatioByJSONString(value)
	case "ModelGroupRatio":
		// 分组×模型 二维定价矩阵：解析失败或含负值时保留原矩阵，避免配置写错导致计费失控
		err = billingratio.UpdateModelGroupRatioByJSONString(value)
	case "CostRatio":
		// 上游成本倍率（B7）：同样在写入前校验，脏数据会让毛利报表失真
		err = billingratio.UpdateCostRatioByJSONString(value)
	case "CompletionRatio":
		err = billingratio.UpdateCompletionRatioByJSONString(value)
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
	}
	return err
}
