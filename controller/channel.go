package controller

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/common/helper"
	"github.com/xiaosu4610/LTZY-API/model"
	"github.com/xiaosu4610/LTZY-API/monitor"
)

func GetAllChannels(c *gin.Context) {
	p, _ := strconv.Atoi(c.Query("p"))
	if p < 0 {
		p = 0
	}
	channels, err := model.GetAllChannels(p*config.ItemsPerPage, config.ItemsPerPage, "limited")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	// 多密钥渠道（LTZY-API 新增）：补上「共几把密钥 / 当前几把在冷却」供前端展示。
	// 这两个字段不落库，只在返回前计算。
	model.FillChannelKeyStats(channels)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channels,
	})
	return
}

func SearchChannels(c *gin.Context) {
	keyword := c.Query("keyword")
	channels, err := model.SearchChannels(keyword)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	model.FillChannelKeyStats(channels)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channels,
	})
	return
}

func GetChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	channel, err := model.GetChannelById(id, false)
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
		"data":    channel,
	})
	return
}

// ChannelHealth 渠道健康度条目（LTZY-API 新增）。
type ChannelHealth struct {
	ChannelId        int     `json:"channel_id"`
	Name             string  `json:"name"`
	Type             int     `json:"type"`
	Status           int     `json:"status"`
	RequestCount     int64   `json:"request_count"`
	Quota            int64   `json:"quota"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	AvgElapsedTime   int64   `json:"avg_elapsed_time"`
	SuccessRate      float64 `json:"success_rate"`
	SampleCount      int     `json:"sample_count"`
}

// GetChannelHealth 返回渠道健康度看板数据。
// 参数：hours（统计窗口小时数，默认 24）。
// 数据来源：消费日志（请求数/额度/token/平均耗时）+ 实时监控（成功率）。
func GetChannelHealth(c *gin.Context) {
	hours, _ := strconv.Atoi(c.Query("hours"))
	if hours <= 0 {
		hours = 24
	}
	startTimestamp := helper.GetTimestamp() - int64(hours)*3600

	channels, err := model.GetAllChannels(0, 0, "all")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	stats, err := model.GetChannelStatistics(startTimestamp, 0, c.GetInt(ctxkey.TenantId))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	statMap := make(map[int]*model.ChannelStatistic, len(stats))
	for _, s := range stats {
		statMap[s.ChannelId] = s
	}
	metricMap := make(map[int]monitor.ChannelMetric)
	for _, m := range monitor.SnapshotMetrics() {
		metricMap[m.ChannelId] = m
	}

	result := make([]*ChannelHealth, 0, len(channels))
	for _, channel := range channels {
		item := &ChannelHealth{
			ChannelId: channel.Id,
			Name:      channel.Name,
			Type:      channel.Type,
			Status:    channel.Status,
			// 默认无实时采样时成功率记为 -1，前端据此显示「无数据」
			SuccessRate: -1,
		}
		if s, ok := statMap[channel.Id]; ok {
			item.RequestCount = s.RequestCount
			item.Quota = s.Quota
			item.PromptTokens = s.PromptTokens
			item.CompletionTokens = s.CompletionTokens
			item.AvgElapsedTime = s.AvgElapsedTime
		}
		if m, ok := metricMap[channel.Id]; ok {
			item.SuccessRate = m.SuccessRate
			item.SampleCount = m.SampleCount
		}
		result = append(result, item)
	}
	// 请求量降序，便于一眼看出活跃渠道
	sort.Slice(result, func(i, j int) bool {
		return result[i].RequestCount > result[j].RequestCount
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"hours": hours,
			"items": result,
		},
	})
	return
}

func AddChannel(c *gin.Context) {
	channel := model.Channel{}
	err := c.ShouldBindJSON(&channel)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	channel.CreatedTime = helper.GetTimestamp()
	// 多密钥（LTZY-API 改）：Key 字段里的多行密钥**不再拆成多条渠道**，
	// 而是存进同一条渠道、由调度层轮询使用。拆成多条会让「英伟达 500 把密钥」
	// 变成 500 条渠道，模型列表要重复维护几百遍，渠道页也没法看。
	channel.Key = strings.Join(model.SplitChannelKeys(channel.Key), "\n")
	if channel.Key == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "密钥不能为空",
		})
		return
	}
	err = model.BatchInsertChannels([]model.Channel{channel})
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
	})
	return
}

func DeleteChannel(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	channel := model.Channel{Id: id}
	err := channel.Delete()
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
	})
	return
}

func DeleteDisabledChannel(c *gin.Context) {
	rows, err := model.DeleteDisabledChannel()
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
		"data":    rows,
	})
	return
}

func UpdateChannel(c *gin.Context) {
	channel := model.Channel{}
	err := c.ShouldBindJSON(&channel)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	err = channel.Update()
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
		"data":    channel,
	})
	return
}
