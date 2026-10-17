package monitor

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"github.com/xiaosu4610/LTZY-API/model"
)

// 用量/成本告警（LTZY-API 新增，D6）。
//
// 目标：在「有人把额度烧穿」或「整体成本异常」时主动通知管理员，
// 而不是等用户来投诉。与 B6 预算的区别：预算是**拦截**，告警是**预警/通报**，
// 两者互补——预算按令牌/用户设上限，告警则覆盖"没设预算但用量异常"的场景。
//
// 设计：
//   - 周期性轮询（默认 5 分钟）读取 quota_budgets 的当日计数，不扫日志表；
//   - 两级阈值：单用户日用量、全局日用量（均为 0 = 关闭）；
//   - 去重：同一目标同一天只告警一次（用内存集合记录，重启后重置）；
//   - 全部走既有通知渠道（邮件 / MessagePusher / Webhook / 飞书 / 钉钉 / Slack）。

const (
	alertCheckInterval = 5 * time.Minute
	// alertMaxUsersPerRound 单轮最多通报的用户数，防止一次刷屏
	alertMaxUsersPerRound = 10
)

var (
	alertedOnce sync.Map // "user:<id>:<yyyymmdd>" / "global:<yyyymmdd>" -> struct{}
)

// StartUsageAlert 启动用量告警轮询。两个阈值都为 0 时不启动（零开销）。
func StartUsageAlert() {
	userThreshold := alertEnvInt64("ALERT_USER_DAILY_QUOTA")
	globalThreshold := alertEnvInt64("ALERT_GLOBAL_DAILY_QUOTA")
	if userThreshold <= 0 && globalThreshold <= 0 {
		logger.SysLog("usage alert disabled (both thresholds are 0)")
		return
	}
	logger.SysLog(fmt.Sprintf("usage alert enabled: user threshold %d, global threshold %d, interval %s",
		userThreshold, globalThreshold, alertCheckInterval))
	go func() {
		// 启动后先等一个周期，避免与启动期数据库迁移/缓存预热抢资源
		ticker := time.NewTicker(alertCheckInterval)
		defer ticker.Stop()
		for range ticker.C {
			checkUsageAlert(userThreshold, globalThreshold)
		}
	}()
}

func alertEnvInt64(name string) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		logger.SysLog(fmt.Sprintf("invalid %s, usage alert for this threshold is disabled", name))
		return 0
	}
	return value
}

// checkUsageAlert 执行一轮检查。抽成独立函数便于单测。
func checkUsageAlert(userThreshold int64, globalThreshold int64) {
	today := time.Now().Format("20060102")

	if globalThreshold > 0 {
		total, err := model.GlobalDailyUsage()
		if err != nil {
			logger.SysError("usage alert: failed to read global daily usage: " + err.Error())
		} else if total >= globalThreshold {
			key := "global:" + today
			if _, loaded := alertedOnce.LoadOrStore(key, struct{}{}); !loaded {
				notifyRootUser("【LTZY-API 告警】全局用量超阈值",
					fmt.Sprintf("今日全局消费已达 %s，超过阈值 %s。",
						common.LogQuota(total), common.LogQuota(globalThreshold)))
			}
		}
	}

	if userThreshold > 0 {
		users, err := model.UserDailyUsageOver(userThreshold, alertMaxUsersPerRound)
		if err != nil {
			logger.SysError("usage alert: failed to read user daily usage: " + err.Error())
			return
		}
		for _, row := range users {
			key := fmt.Sprintf("user:%d:%s", row.SubjectId, today)
			if _, loaded := alertedOnce.LoadOrStore(key, struct{}{}); loaded {
				continue
			}
			notifyRootUser("【LTZY-API 告警】用户用量超阈值",
				fmt.Sprintf("用户 #%d 今日消费已达 %s，超过单用户阈值 %s。",
					row.SubjectId, common.LogQuota(row.UsedQuota), common.LogQuota(userThreshold)))
		}
	}
}
