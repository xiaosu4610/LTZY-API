package controller

import (
	"context"
	"fmt"
	"net/http"
	"math"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"github.com/xiaosu4610/LTZY-API/model"
	"github.com/xiaosu4610/LTZY-API/monitor"
	relay "github.com/xiaosu4610/LTZY-API/relay"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/openai"
	"github.com/xiaosu4610/LTZY-API/relay/apitype"
	billingratio "github.com/xiaosu4610/LTZY-API/relay/billing/ratio"
	"github.com/xiaosu4610/LTZY-API/relay/channeltype"
	"github.com/xiaosu4610/LTZY-API/relay/meta"
	relaymodel "github.com/xiaosu4610/LTZY-API/relay/model"
)

// https://platform.openai.com/docs/api-reference/models/list

type OpenAIModelPermission struct {
	Id                 string  `json:"id"`
	Object             string  `json:"object"`
	Created            int     `json:"created"`
	AllowCreateEngine  bool    `json:"allow_create_engine"`
	AllowSampling      bool    `json:"allow_sampling"`
	AllowLogprobs      bool    `json:"allow_logprobs"`
	AllowSearchIndices bool    `json:"allow_search_indices"`
	AllowView          bool    `json:"allow_view"`
	AllowFineTuning    bool    `json:"allow_fine_tuning"`
	Organization       string  `json:"organization"`
	Group              *string `json:"group"`
	IsBlocking         bool    `json:"is_blocking"`
}

type OpenAIModels struct {
	Id         string                  `json:"id"`
	Object     string                  `json:"object"`
	Created    int                     `json:"created"`
	OwnedBy    string                  `json:"owned_by"`
	Permission []OpenAIModelPermission `json:"permission"`
	Root       string                  `json:"root"`
	Parent     *string                 `json:"parent"`
}

var models []OpenAIModels
var modelsMap map[string]OpenAIModels
var channelId2Models map[int][]string

func init() {
	var permission []OpenAIModelPermission
	permission = append(permission, OpenAIModelPermission{
		Id:                 "modelperm-LwHkVFn8AcMItP432fKKDIKJ",
		Object:             "model_permission",
		Created:            1626777600,
		AllowCreateEngine:  true,
		AllowSampling:      true,
		AllowLogprobs:      true,
		AllowSearchIndices: false,
		AllowView:          true,
		AllowFineTuning:    false,
		Organization:       "*",
		Group:              nil,
		IsBlocking:         false,
	})
	// https://platform.openai.com/docs/models/model-endpoint-compatibility
	for i := 0; i < apitype.Dummy; i++ {
		if i == apitype.AIProxyLibrary {
			continue
		}
		adaptor := relay.GetAdaptor(i)
		channelName := adaptor.GetChannelName()
		modelNames := adaptor.GetModelList()
		for _, modelName := range modelNames {
			models = append(models, OpenAIModels{
				Id:         modelName,
				Object:     "model",
				Created:    1626777600,
				OwnedBy:    channelName,
				Permission: permission,
				Root:       modelName,
				Parent:     nil,
			})
		}
	}
	for _, channelType := range openai.CompatibleChannels {
		if channelType == channeltype.Azure {
			continue
		}
		channelName, channelModelList := openai.GetCompatibleChannelMeta(channelType)
		for _, modelName := range channelModelList {
			models = append(models, OpenAIModels{
				Id:         modelName,
				Object:     "model",
				Created:    1626777600,
				OwnedBy:    channelName,
				Permission: permission,
				Root:       modelName,
				Parent:     nil,
			})
		}
	}
	modelsMap = make(map[string]OpenAIModels)
	for _, model := range models {
		modelsMap[model.Id] = model
	}
	channelId2Models = make(map[int][]string)
	for i := 1; i < channeltype.Dummy; i++ {
		adaptor := relay.GetAdaptor(channeltype.ToAPIType(i))
		meta := &meta.Meta{
			ChannelType: i,
		}
		adaptor.Init(meta)
		channelId2Models[i] = adaptor.GetModelList()
	}
}

func DashboardListModels(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channelId2Models,
	})
}

func ListAllModels(c *gin.Context) {
	c.JSON(200, gin.H{
		"object": "list",
		"data":   models,
	})
}

func ListModels(c *gin.Context) {
	ctx := c.Request.Context()
	var availableModels []string
	if c.GetString(ctxkey.AvailableModels) != "" {
		availableModels = strings.Split(c.GetString(ctxkey.AvailableModels), ",")
	} else {
		userId := c.GetInt(ctxkey.Id)
		userGroup, _ := model.CacheGetUserGroup(userId)
		availableModels, _ = model.CacheGetGroupModels(ctx, userGroup)
	}
	modelSet := make(map[string]bool)
	for _, availableModel := range availableModels {
		modelSet[availableModel] = true
	}
	availableOpenAIModels := make([]OpenAIModels, 0)
	for _, model := range models {
		if _, ok := modelSet[model.Id]; ok {
			modelSet[model.Id] = false
			availableOpenAIModels = append(availableOpenAIModels, model)
		}
	}
	for modelName, ok := range modelSet {
		if ok {
			availableOpenAIModels = append(availableOpenAIModels, OpenAIModels{
				Id:      modelName,
				Object:  "model",
				Created: 1626777600,
				OwnedBy: "custom",
				Root:    modelName,
				Parent:  nil,
			})
		}
	}
	c.JSON(200, gin.H{
		"object": "list",
		"data":   availableOpenAIModels,
	})
}

func RetrieveModel(c *gin.Context) {
	modelId := c.Param("model")
	if model, ok := modelsMap[modelId]; ok {
		c.JSON(200, model)
	} else {
		Error := relaymodel.Error{
			Message: fmt.Sprintf("The model '%s' does not exist", modelId),
			Type:    "invalid_request_error",
			Param:   "model",
			Code:    "model_not_found",
		}
		c.JSON(200, gin.H{
			"error": Error,
		})
	}
}

func GetUserAvailableModels(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.GetInt(ctxkey.Id)
	userGroup, err := model.CacheGetUserGroup(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	models, err := model.CacheGetGroupModels(ctx, userGroup)
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
		"data":    models,
	})
	return
}

// ModelPlazaItem 模型广场条目（LTZY-API 新增）。
//
// 只暴露「能不能用 / 多少钱 / 快不快」，不含渠道明细与任何上游凭据：
// 这是一个普通登录用户（以及游客）就能访问的页面。
type ModelPlazaItem struct {
	ModelName            string  `json:"model_name"`
	ChannelCount         int     `json:"channel_count"` // 承载该模型的启用渠道数
	Ratio                float64 `json:"ratio"`         // 模型倍率（相对官方报价的倍数）
	CompletionRatio      float64 `json:"completion_ratio"`
	PromptPricePer1K     float64 `json:"prompt_price_per_1k"`     // 元 / 千输入 token
	CompletionPricePer1K float64 `json:"completion_price_per_1k"` // 元 / 千输出 token
	AvgResponseTime      int64   `json:"avg_response_time"`       // 毫秒，-1 表示暂无测速样本
	AvgTokensPerSecond   float64 `json:"avg_tokens_per_second"`   // 生成速率，-1 表示暂无消费样本
	SuccessRate          float64 `json:"success_rate"`            // 0~1，-1 表示暂无采样
	SampleCount          int     `json:"sample_count"`
	// Health 由成功率推导的可用状态，前端只管展示不用再自己算：
	//   online   正常（无采样时也按正常展示——渠道在册即代表可调度）
	//   degraded 波动（成功率 60%~90%）
	//   unstable 不稳定（成功率低于 60%）
	Health string `json:"health"`
}

const (
	ModelHealthOnline   = "online"
	ModelHealthDegraded = "degraded"
	ModelHealthUnstable = "unstable"
)

// modelHealth 把成功率折算成展示用状态。无采样（<0）时按正常处理：
// 「没数据」不应该被渲染成「有故障」，那是最容易误导用户的呈现方式。
func modelHealth(successRate float64) string {
	switch {
	case successRate < 0 || successRate >= 0.9:
		return ModelHealthOnline
	case successRate >= 0.6:
		return ModelHealthDegraded
	default:
		return ModelHealthUnstable
	}
}

// buildModelPlazaItems 组装某个分组的模型广场数据。价格口径与扣费完全一致
// （都由 RatioScale 换算），避免出现「广场上显示的价与实际扣费不符」这类最伤信任的问题。
func buildModelPlazaItems(ctx context.Context, group string) ([]ModelPlazaItem, error) {
	modelNames, err := model.CacheGetGroupModels(ctx, group)
	if err != nil {
		return nil, err
	}
	snapshot, err := model.GetModelChannelSnapshot(group)
	if err != nil {
		return nil, err
	}

	// 渠道实时成功率（未开启指标采集时为空表，前端按「无采样」展示）
	metricMap := make(map[int]monitor.ChannelMetric)
	for _, metric := range monitor.SnapshotMetrics() {
		metricMap[metric.ChannelId] = metric
	}
	// 生成速率取最近 24 小时的消费日志；查不到就不显示，不猜
	throughputMap, err := model.GetModelThroughput(24 * 3600)
	if err != nil {
		logger.SysError("failed to load model throughput: " + err.Error())
		throughputMap = map[string]float64{}
	}

	// 每千 token 人民币价 = 倍率 × 货币换算系数 × 1000 / 每单位额度
	scale := billingratio.RatioScale()
	yuanPer1KPerRatio := scale * 1000 / config.QuotaPerUnit

	items := make([]ModelPlazaItem, 0, len(modelNames))
	for _, modelName := range modelNames {
		channelIds := snapshot.Channels[modelName]
		if len(channelIds) == 0 {
			continue
		}
		ratio := billingratio.GetModelRatio(modelName, 0)
		completionRatio := billingratio.GetCompletionRatio(modelName, 0)

		item := ModelPlazaItem{
			ModelName:            modelName,
			ChannelCount:         len(channelIds),
			Ratio:                ratio,
			CompletionRatio:      completionRatio,
			PromptPricePer1K:     ratio * yuanPer1KPerRatio,
			CompletionPricePer1K: ratio * completionRatio * yuanPer1KPerRatio,
			AvgResponseTime:      -1,
			AvgTokensPerSecond:   -1,
			SuccessRate:          -1,
		}

		// 响应时间：取可用渠道最近一次测速的平均值；全部没测过则保持 -1
		var timeSum int64
		var timeCount int64
		// 成功率：按采样数加权平均，避免「一个渠道只有 1 次采样」直接拉偏整模型
		var sampleSum, successWeight int64
		for _, channelId := range channelIds {
			if rt := snapshot.ResponseTime[channelId]; rt > 0 {
				timeSum += int64(rt)
				timeCount++
			}
			if metric, ok := metricMap[channelId]; ok && metric.SampleCount > 0 {
				sampleSum += int64(metric.SampleCount)
				successWeight += int64(metric.SuccessRate * float64(metric.SampleCount))
			}
		}
		if timeCount > 0 {
			item.AvgResponseTime = timeSum / timeCount
		}
		if sampleSum > 0 {
			item.SuccessRate = float64(successWeight) / float64(sampleSum)
			item.SampleCount = int(sampleSum)
		}
		if tps, ok := throughputMap[modelName]; ok && tps > 0 {
			item.AvgTokensPerSecond = math.Round(tps*10) / 10
		}
		item.Health = modelHealth(item.SuccessRate)
		items = append(items, item)
	}
	return items, nil
}

func respondModelPlaza(c *gin.Context, group string) {
	items, err := buildModelPlazaItems(c.Request.Context(), group)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"group": group,
			"items": items,
		},
	})
	return
}

// GetModelPlaza 登录用户的模型广场：按该用户所属分组返回。
func GetModelPlaza(c *gin.Context) {
	userId := c.GetInt(ctxkey.Id)
	group, err := model.CacheGetUserGroup(userId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	respondModelPlaza(c, group)
}

// GetModelPlazaPublic 游客也能看的模型广场，用于站点首页展示。
//
// 固定看 default 分组（可用 ?group= 指定），不涉及任何用户私有信息，
// 因此不需要登录态 —— 首页是公开页面，若这里强制登录，游客就看不到广场了。
func GetModelPlazaPublic(c *gin.Context) {
	group := strings.TrimSpace(c.Query("group"))
	if group == "" {
		group = "default"
	}
	respondModelPlaza(c, group)
}
