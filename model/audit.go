package model

import (
	"context"

	"github.com/xiaosu4610/LTZY-API/common/helper"
	"github.com/xiaosu4610/LTZY-API/common/logger"
)

// AuditLog 审计日志（LTZY-API 新增，D4）。
//
// 与消费 Log 的区别：Log 面向「计费流水」（用户/模型/额度），AuditLog 面向
// 「谁在什么时候对系统做了什么管理操作」，用于安全审计与问题回溯。
// 落库到 LOG_DB（与主库分离），不建消费侧索引以外的额外索引，写多读少。
type AuditLog struct {
	Id        int    `json:"id"`
	UserId    int    `json:"user_id" gorm:"index"`
	Username  string `json:"username" gorm:"index"`
	Action    string `json:"action" gorm:"index"`        // 动作类型：channel.create / token.delete / option.update ...
	Target    string `json:"target" gorm:"default:''"`   // 操作对象标识（渠道ID、令牌ID、Option key）
	Detail    string `json:"detail" gorm:"type:text"`    // 变更明细（脱敏后的 JSON 或可读描述）
	IP        string `json:"ip" gorm:"default:''"`       // 来源 IP
	CreatedAt int64  `json:"created_at" gorm:"index"`    // unix 秒
}

// RecordAuditLog 写一条审计日志。
// 失败只记录错误、不影响业务响应 —— 审计是旁路能力，不能因审计故障阻断管理操作；
// 但错误会进系统日志，便于发现审计断流。
func RecordAuditLog(ctx context.Context, log *AuditLog) {
	if log == nil {
		return
	}
	log.CreatedAt = helper.GetTimestamp()
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.Error(ctx, "failed to record audit log: "+err.Error())
	}
}

// GetAuditLogs 分页查询审计日志（管理面）。
func GetAuditLogs(pageNum int, pageSize int, action string) (logs []*AuditLog, total int64, err error) {
	tx := LOG_DB.Model(&AuditLog{})
	if action != "" {
		tx = tx.Where("action = ?", action)
	}
	err = tx.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	err = tx.Order("id desc").Limit(pageSize).Offset((pageNum - 1) * pageSize).Find(&logs).Error
	return logs, total, err
}

// DeleteOldAuditLogs 清理指定时间戳之前的审计日志（对接既有日志清理策略）。
func DeleteOldAuditLogs(targetTimestamp int64) (int64, error) {
	result := LOG_DB.Where("created_at < ?", targetTimestamp).Delete(&AuditLog{})
	return result.RowsAffected, result.Error
}
