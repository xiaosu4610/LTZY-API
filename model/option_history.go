package model

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/helper"
	"github.com/xiaosu4610/LTZY-API/common/logger"
)

// 配置变更历史与回滚（LTZY-API 新增，E9）。
//
// 与 D4 审计日志的分工（刻意不重复造轮子）：
//   - audit_logs（LOG_DB）回答"谁在什么时候动了哪个配置"，是安全审计视角，
//     明细只记 {"value":"***"}，不保留旧值，也不提供任何写回能力；
//   - option_histories（主库）回答"这个配置上一步是什么、怎么退回去"，是运维视角，
//     必须保留旧值才能回滚，因此它落主库、跟随配置一起备份。
//
// 两者都记录 operator/created_at，但用途不同：删掉审计日志不影响回滚能力，
// 反之亦然。

// optionRollbackRemarkPrefix 回滚操作的备注前缀。
// 用前缀而不是额外字段做判定，是为了让 remark 本身就自解释（人读历史列表时一目了然）。
const optionRollbackRemarkPrefix = "回滚配置变更"

// OptionHistory 配置变更历史。
//
// 敏感值处理（取舍见下）：
//   - OldValue/NewValue 只存"可展示"的值，敏感键在这里是脱敏后的文本（接口回显的就是它）；
//   - OldValueRaw/NewValueRaw 存敏感键的原文，仅回滚时使用，json 标签为 "-"，
//     任何接口序列化都不会把原文带出去。
//
// 为什么敢把原文留在库里：配置项原文本来就以明文存在 options 表（SMTPToken 等），
// 历史表存一份不改变威胁模型（同一个库、同一套访问控制），却换来了"密钥类配置改错
// 也能回滚"的能力。反之若只存脱敏值，回滚会把密钥真的改成 "sk-a***klmn"。
// 若将来引入配置加密（KMS/主密钥），这里应同步改为存密文。
type OptionHistory struct {
	Id           int    `json:"id" gorm:"primaryKey"`
	Key          string `json:"key" gorm:"index"`             // 配置键
	OldValue     string `json:"old_value" gorm:"type:text"`   // 变更前（敏感键为脱敏值）
	NewValue     string `json:"new_value" gorm:"type:text"`   // 变更后（敏感键为脱敏值）
	OldValueRaw  string `json:"-" gorm:"type:text"`           // 变更前原文，仅敏感键填写，仅回滚使用
	NewValueRaw  string `json:"-" gorm:"type:text"`           // 变更后原文，仅敏感键填写，仅回滚使用
	IsSensitive  bool   `json:"is_sensitive" gorm:"default:false"`
	IsRollback   bool   `json:"is_rollback" gorm:"default:false"` // 该条目本身是否是一次回滚
	OperatorId   int    `json:"operator_id" gorm:"index"`
	OperatorName string `json:"operator_name" gorm:"default:''"`
	Remark       string `json:"remark" gorm:"default:''"`
	CreatedAt    int64  `json:"created_at" gorm:"index"` // unix 秒
}

// IsSensitiveOptionKey 判断配置键是否为密钥类。
//
// 判定口径与 controller.GetOptions 的隐藏规则保持一致（否则会出现"列表里看不到、
// 历史里却明文展示"的漏洞）。除 Token/Secret 结尾外，额外覆盖两个历史遗留的漏网之鱼：
// TurnstileSecretKey 与 MidjourneyApiKey —— 它们不以这两个词结尾，但同样是凭据。
func IsSensitiveOptionKey(key string) bool {
	if strings.HasSuffix(key, "Token") || strings.HasSuffix(key, "Secret") {
		return true
	}
	switch key {
	case "TurnstileSecretKey", "MidjourneyApiKey":
		return true
	}
	return false
}

// optionHistoryDisplayValue 生成历史列表里可安全展示的值。
func optionHistoryDisplayValue(key string, value string) string {
	if IsSensitiveOptionKey(key) {
		return common.MaskSecret(value)
	}
	return value
}

// optionHistoryRawValue 生成回滚用的原文：只有敏感键才需要额外存一份
// （非敏感键的展示值本身就是原文，再存一份纯属冗余）。
func optionHistoryRawValue(key string, value string) string {
	if IsSensitiveOptionKey(key) {
		return value
	}
	return ""
}

// RecordOptionHistory 追加一条配置变更历史。
//
// 与审计日志同取舍：失败只记系统日志、不影响配置写入 —— 历史是旁路能力，
// 不能因为历史表写不进去就把管理员的配置操作整体搞失败。
func RecordOptionHistory(history *OptionHistory) {
	if history == nil {
		return
	}
	history.CreatedAt = helper.GetTimestamp()
	if err := DB.Create(history).Error; err != nil {
		logger.SysError("failed to record option history: " + err.Error())
	}
}

// GetOptionHistories 分页查询配置变更历史（管理面）。
// key 非空时只看某个配置键的变更链路。
func GetOptionHistories(pageNum int, pageSize int, key string) (histories []*OptionHistory, total int64, err error) {
	tx := DB.Model(&OptionHistory{})
	if key != "" {
		// key 是 SQL 保留字：MySQL 要用反引号、PostgreSQL 要用双引号，
		// 与 model/cache.go 的取键列名口径保持一致，否则在 PG 上直接语法报错。
		keyCol := "`key`"
		if common.UsingPostgreSQL {
			keyCol = `"key"`
		}
		tx = tx.Where(keyCol+" = ?", key)
	}
	if err = tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = tx.Order("id desc").Limit(pageSize).Offset((pageNum - 1) * pageSize).Find(&histories).Error
	return histories, total, err
}

// RollbackOptionById 按历史记录 id 回滚该次变更：把配置恢复成这条记录里的旧值。
//
// 为什么恢复的是"旧值"而不是"新值"：历史列表的语义是"撤销这一次变更"。
// 回滚成功后会再写一条历史（IsRollback=true），因此"谁把配置改回去的、改回去了什么"
// 同样留痕，链路完整。
func RollbackOptionById(id int, operatorId int, operatorName string) (*OptionHistory, error) {
	var history OptionHistory
	if err := DB.First(&history, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("配置变更记录 %d 不存在", id)
		}
		return nil, err
	}

	// 只认环境变量/代码的键不参与回滚：回滚会"成功返回"却什么也没改变，
	// 属于任务里明确禁止的"假装能热更新"。这类键只能改环境变量（或改代码）后重启，
	// 具体处理方式见 RestartRequiredOptions() 返回的 reason/env_var。
	// 注意这里用 IsEnvOnlyOption 而不是 IsRestartRequiredOption：Theme 虽然也要重启才切换
	// 界面，但它的值是能热同步的，回滚 Theme 有意义（详见 model/option_restart.go 的说明）。
	if IsEnvOnlyOption(history.Key) {
		return nil, fmt.Errorf("配置 %s 需重启生效，不支持在线回滚（详见 restart_required 清单）", history.Key)
	}

	target := history.OldValue
	if history.IsSensitive && history.OldValueRaw != "" {
		// 敏感键必须用原文回滚：展示值是被打码的，写回去会把密钥真的改成 "sk-a***klmn"。
		target = history.OldValueRaw
	}

	remark := fmt.Sprintf("%s #%d", optionRollbackRemarkPrefix, history.Id)
	return UpdateOptionWithOperator(history.Key, target, operatorId, operatorName, remark)
}
