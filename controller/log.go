package controller

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/common/helper"
	"github.com/xiaosu4610/LTZY-API/model"
	billingratio "github.com/xiaosu4610/LTZY-API/relay/billing/ratio"
)

func GetAllLogs(c *gin.Context) {
	p, _ := strconv.Atoi(c.Query("p"))
	if p < 0 {
		p = 0
	}
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	logs, err := model.GetAllLogs(logType, startTimestamp, endTimestamp, modelName, username, tokenName, p*config.ItemsPerPage, config.ItemsPerPage, channel, c.GetInt(ctxkey.TenantId))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    logs,
	})
	return
}

func GetUserLogs(c *gin.Context) {
	p, _ := strconv.Atoi(c.Query("p"))
	if p < 0 {
		p = 0
	}
	userId := c.GetInt(ctxkey.Id)
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	logs, err := model.GetUserLogs(userId, logType, startTimestamp, endTimestamp, modelName, tokenName, p*config.ItemsPerPage, config.ItemsPerPage)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    logs,
	})
	return
}

func SearchAllLogs(c *gin.Context) {
	keyword := c.Query("keyword")
	logs, err := model.SearchAllLogs(keyword, c.GetInt(ctxkey.TenantId))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    logs,
	})
	return
}

func SearchUserLogs(c *gin.Context) {
	keyword := c.Query("keyword")
	userId := c.GetInt(ctxkey.Id)
	logs, err := model.SearchUserLogs(userId, keyword)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    logs,
	})
	return
}

func GetLogsStat(c *gin.Context) {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	username := c.Query("username")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	quotaNum := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel)
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, "")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": quotaNum,
			//"token": tokenNum,
		},
	})
	return
}

func GetLogsSelfStat(c *gin.Context) {
	username := c.GetString(ctxkey.Username)
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	quotaNum := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel)
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, tokenName)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": quotaNum,
			//"token": tokenNum,
		},
	})
	return
}

// GetCostAnalysis 返回成本分析大盘数据（LTZY-API 新增）。
// 参数：hours（统计窗口小时数，默认 24）、top（TopN 条数，默认 10）。
// 维度：总额概览 + 按模型 + 按用户 + 按渠道 + 按天趋势。
func GetCostAnalysis(c *gin.Context) {
	hours, _ := strconv.Atoi(c.Query("hours"))
	if hours <= 0 {
		hours = 24
	}
	top, _ := strconv.Atoi(c.Query("top"))
	if top <= 0 {
		top = 10
	}
	startTimestamp := helper.GetTimestamp() - int64(hours)*3600

	modelStats, err := model.GetModelCostStatistics(startTimestamp, 0, top, c.GetInt(ctxkey.TenantId))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	userStats, err := model.GetUserCostStatistics(startTimestamp, 0, top, c.GetInt(ctxkey.TenantId))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	channelStats, err := model.GetChannelStatistics(startTimestamp, 0, c.GetInt(ctxkey.TenantId))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	dailyStats, err := model.GetDailyCostStatistics(startTimestamp, 0, c.GetInt(ctxkey.TenantId))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	// 总额概览：对模型维度求和即可覆盖全部消费
	var totalQuota, totalRequests, totalPromptTokens, totalCompletionTokens, totalCostQuota int64
	for _, s := range modelStats {
		totalQuota += s.Quota
		totalRequests += s.RequestCount
		totalPromptTokens += s.PromptTokens
		totalCompletionTokens += s.CompletionTokens
		totalCostQuota += s.CostQuota
	}
	// 毛利（LTZY-API 新增，B7）：收入 − 上游成本。
	// 注意语义：cost_quota 全为 0 表示「未配置进价」，此时毛利等于收入，
	// 并不代表真的无成本 —— 因此额外返回 cost_configured 让前端能区分这两种情况。
	costConfigured := billingratio.HasCostRatio()
	grossProfit := totalQuota - totalCostQuota

	// 渠道维度按请求量降序后取 TopN
	sort.Slice(channelStats, func(i, j int) bool {
		return channelStats[i].RequestCount > channelStats[j].RequestCount
	})
	if len(channelStats) > top {
		channelStats = channelStats[:top]
	}

	// 统一返回空数组而非 null，便于前端直接渲染
	if modelStats == nil {
		modelStats = []*model.ModelCostStatistic{}
	}
	if userStats == nil {
		userStats = []*model.UserCostStatistic{}
	}
	if channelStats == nil {
		channelStats = []*model.ChannelStatistic{}
	}
	if dailyStats == nil {
		dailyStats = []*model.DailyCostStatistic{}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"hours": hours,
			"summary": gin.H{
				"quota":             totalQuota,
				"request_count":     totalRequests,
				"prompt_tokens":     totalPromptTokens,
				"completion_tokens": totalCompletionTokens,
				// 成本与毛利（B7）
				"cost_quota":       totalCostQuota,
				"gross_profit":     grossProfit,
				"cost_configured":  costConfigured,
			},
			"models":   modelStats,
			"users":    userStats,
			"channels": channelStats,
			"daily":    dailyStats,
		},
	})
	return
}

func DeleteHistoryLogs(c *gin.Context) {
	targetTimestamp, _ := strconv.ParseInt(c.Query("target_timestamp"), 10, 64)
	if targetTimestamp == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "target timestamp is required",
		})
		return
	}
	count, err := model.DeleteOldLog(targetTimestamp)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    count,
	})
	return
}
