package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/channelpreset"
	"github.com/xiaosu4610/LTZY-API/common/helper"
	"github.com/xiaosu4610/LTZY-API/model"
)

// GetChannelPresets 返回渠道预设列表（LTZY-API 新增）。
//
// 预设只提供「上游地址 + 常用模型 + 文档链接 + 注意事项」，**不含渠道类型**：
// 同一家上游在本项目里可能对应不同适配器，硬塞一个 Type 反而容易建出不能用的渠道。
func GetChannelPresets(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channelpreset.All(),
	})
	return
}

// batchChannelItem 批量导入里的单个渠道。
// 除 key 外都可省略，未填的字段从请求顶层的默认值继承。
type batchChannelItem struct {
	Name         string  `json:"name"`
	Type         *int    `json:"type"`
	BaseURL      string  `json:"base_url"`
	Key          string  `json:"key"`
	Models       string  `json:"models"`
	Group        string  `json:"group"`
	Priority     *int64  `json:"priority"`
	Weight       *uint   `json:"weight"`
	Balance      float64 `json:"balance"`
	ModelMapping string  `json:"model_mapping"`
	SystemPrompt string  `json:"system_prompt"`
	CostRatio    string  `json:"cost_ratio"`
	PlanType     string  `json:"plan_type"`
	PlanExpireAt int64   `json:"plan_expire_at"`
}

type batchChannelRequest struct {
	// 以下是所有行的默认值，单行填了就以单行为准
	Type     *int               `json:"type"`
	BaseURL  string             `json:"base_url"`
	Group    string             `json:"group"`
	Models   string             `json:"models"`
	Priority *int64             `json:"priority"`
	Weight   *uint              `json:"weight"`
	Channels []batchChannelItem `json:"channels"`
}

// BatchAddChannels 批量新建渠道（LTZY-API 新增）。
//
// 为什么需要它：一个上游网关往往配了多把密钥，每把密钥能用的模型不同、
// 进货价也不同。在本项目的模型里，这些差异只能靠「一把密钥 = 一条渠道」来表达，
// 于是新建渠道的重复劳动量很大。本接口接受一个数组，一次建完。
//
// 约定：
//   - 单个 item 的 key 里可以换行放多把密钥，会被拆成多条渠道（与单条新增行为一致）；
//   - cost_ratio 是渠道级上游进价 JSON，落库后立即刷新计费侧缓存；
//   - 任一条校验不过就整体拒绝，避免「导入一半」这种难收拾的中间状态。
func BatchAddChannels(c *gin.Context) {
	req := batchChannelRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if len(req.Channels) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "没有需要导入的渠道"})
		return
	}
	if len(req.Channels) > 500 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "一次最多导入 500 条，请分批"})
		return
	}

	now := helper.GetTimestamp()
	channels := make([]model.Channel, 0, len(req.Channels))
	for idx, item := range req.Channels {
		channelType := 0
		if item.Type != nil {
			channelType = *item.Type
		} else if req.Type != nil {
			channelType = *req.Type
		}
		if channelType <= 0 {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "第 " + strconv.Itoa(idx+1) + " 条缺少渠道类型"})
			return
		}
		baseURL := strings.TrimSpace(item.BaseURL)
		if baseURL == "" {
			baseURL = strings.TrimSpace(req.BaseURL)
		}
		models := item.Models
		if strings.TrimSpace(models) == "" {
			models = req.Models
		}
		if strings.TrimSpace(models) == "" {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "第 " + strconv.Itoa(idx+1) + " 条缺少模型列表"})
			return
		}
		if err := model.ValidateChannelCostRatio(item.CostRatio); err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "第 " + strconv.Itoa(idx+1) + " 条 " + err.Error()})
			return
		}
		group := item.Group
		if group == "" {
			group = req.Group
		}
		if group == "" {
			group = "default"
		}
		priority := item.Priority
		if priority == nil {
			priority = req.Priority
		}
		if priority == nil {
			zero := int64(0)
			priority = &zero
		}
		weight := item.Weight
		if weight == nil {
			weight = req.Weight
		}
		if weight == nil {
			zero := uint(0)
			weight = &zero
		}

		// 一把密钥一条渠道；item.key 里换行会被拆开（与单条新增行为一致）
		for _, key := range strings.Split(item.Key, "\n") {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			baseURLValue := baseURL
			modelMapping := item.ModelMapping
			systemPrompt := item.SystemPrompt
			name := item.Name
			if name == "" {
				name = key
			}
			channels = append(channels, model.Channel{
				Type:         channelType,
				Key:          key,
				Status:       model.ChannelStatusEnabled,
				Name:         name,
				CreatedTime:  now,
				BaseURL:      &baseURLValue,
				Models:       models,
				Group:        group,
				Priority:     priority,
				Weight:       weight,
				Balance:      item.Balance,
				ModelMapping: &modelMapping,
				SystemPrompt: &systemPrompt,
				CostRatio:    item.CostRatio,
				PlanType:     item.PlanType,
				PlanExpireAt: item.PlanExpireAt,
			})
		}
	}
	if len(channels) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "所有条目都没有有效密钥"})
		return
	}

	if err := model.BatchInsertChannels(channels); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	// 渠道级进价刚变了，立刻同步给计费侧，否则新导入的渠道会先按全局进价记账
	model.RefreshChannelCostRatios()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"created": len(channels)},
	})
	return
}
