package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"github.com/xiaosu4610/LTZY-API/model"
)

// 预算与熔断中间件（LTZY-API 新增，B6）。
//
// 校验顺序：令牌日 → 令牌月 → 用户日 → 用户月，任一超限即熔断（403）。
// 用户级预算来自全局 Option（DefaultUserDaily/MonthlyQuotaLimit），
// 因此不产生任何额外数据库查询；令牌级预算来自 TokenAuth 写入的上下文。
//
// 未配置任何预算时直接放行（零开销），保证不影响未使用该功能的部署。
//
// ⚠️ 注意：gin 的 GetInt 只识别 int，int64 必须用 GetInt64，
// 否则预算配置读出来永远是 0（该功能会静默失效）。
func BudgetLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		userId := c.GetInt(ctxkey.Id)
		tokenId := c.GetInt(ctxkey.TokenId)
		tokenDailyLimit := c.GetInt64(ctxkey.TokenDailyLimit)
		tokenMonthlyLimit := c.GetInt64(ctxkey.TokenMonthlyLimit)
		userDailyLimit, userMonthlyLimit := model.GetDefaultUserBudgetLimits()

		if tokenDailyLimit <= 0 && tokenMonthlyLimit <= 0 && userDailyLimit <= 0 && userMonthlyLimit <= 0 {
			c.Next()
			return
		}

		now := time.Now()
		// 令牌用量：仅在有令牌级预算时查询，避免无谓的点查
		var tokenDailyUsed, tokenMonthlyUsed int64
		if tokenId > 0 && (tokenDailyLimit > 0 || tokenMonthlyLimit > 0) {
			var err error
			tokenDailyUsed, tokenMonthlyUsed, err = model.GetBudgetUsages(model.BudgetSubjectToken, tokenId, now)
			if err != nil {
				logger.SysError("failed to read token budget usage: " + err.Error())
				c.Next() // 预算旁路故障不阻断业务
				return
			}
		}
		var userDailyUsed, userMonthlyUsed int64
		if userId > 0 && (userDailyLimit > 0 || userMonthlyLimit > 0) {
			var err error
			userDailyUsed, userMonthlyUsed, err = model.GetBudgetUsages(model.BudgetSubjectUser, userId, now)
			if err != nil {
				logger.SysError("failed to read user budget usage: " + err.Error())
				c.Next()
				return
			}
		}

		if message := checkBudgets(
			tokenDailyLimit, tokenDailyUsed, "令牌",
			tokenMonthlyLimit, tokenMonthlyUsed, "令牌",
			userDailyLimit, userDailyUsed, "账户",
			userMonthlyLimit, userMonthlyUsed, "账户",
		); message != "" {
			c.JSON(http.StatusForbidden, gin.H{
				"error": gin.H{
					"message": message,
					"type":    "one_api_error",
					"code":    "budget_limit_exceeded",
				},
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// checkBudgets 依次校验四组预算，返回第一条超限提示（空串表示全部通过）。
func checkBudgets(
	tokenDailyLimit int64, tokenDailyUsed int64, tokenDailyScope string,
	tokenMonthlyLimit int64, tokenMonthlyUsed int64, tokenMonthlyScope string,
	userDailyLimit int64, userDailyUsed int64, userDailyScope string,
	userMonthlyLimit int64, userMonthlyUsed int64, userMonthlyScope string,
) string {
	if tokenDailyLimit > 0 && tokenDailyUsed >= tokenDailyLimit {
		return budgetExceededMessage(tokenDailyScope, "本日", tokenDailyUsed, tokenDailyLimit)
	}
	if tokenMonthlyLimit > 0 && tokenMonthlyUsed >= tokenMonthlyLimit {
		return budgetExceededMessage(tokenMonthlyScope, "本月", tokenMonthlyUsed, tokenMonthlyLimit)
	}
	if userDailyLimit > 0 && userDailyUsed >= userDailyLimit {
		return budgetExceededMessage(userDailyScope, "本日", userDailyUsed, userDailyLimit)
	}
	if userMonthlyLimit > 0 && userMonthlyUsed >= userMonthlyLimit {
		return budgetExceededMessage(userMonthlyScope, "本月", userMonthlyUsed, userMonthlyLimit)
	}
	return ""
}

func budgetExceededMessage(scope string, period string, used int64, limit int64) string {
	return fmt.Sprintf("%s%s预算已用完（已用 %s / 上限 %s），请联系管理员调整或稍后再试",
		scope, period, common.LogQuota(used), common.LogQuota(limit))
}
