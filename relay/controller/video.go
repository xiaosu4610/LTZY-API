// Package controller is a package for handling the relay controller
package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"github.com/xiaosu4610/LTZY-API/model"
	"github.com/xiaosu4610/LTZY-API/relay"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/openai"
	billingratio "github.com/xiaosu4610/LTZY-API/relay/billing/ratio"
	"github.com/xiaosu4610/LTZY-API/relay/meta"
	relaymodel "github.com/xiaosu4610/LTZY-API/relay/model"
)

// 视频生成按次计费（LTZY-API 新增）。
//
// 设计说明：
//   - 视频生成不按 token 计费，而是按「次」计费，额度 = 模型倍率 × 分组倍率 × videoQuotaUnit × 数量；
//   - 请求体除核心字段外，未识别字段原样透传，兼容各家私有参数；
//   - 响应体完全透传（不解析、不改写），兼容同步返回视频与异步返回任务 ID 两种模式。

// videoQuotaUnit 单次视频生成的额度基数（与图片生成保持一致，均为 1000）
const videoQuotaUnit = 1000

// RelayVideoHelper 处理 /v1/videos 视频生成请求。
func RelayVideoHelper(c *gin.Context) *relaymodel.ErrorWithStatusCode {
	ctx := c.Request.Context()
	meta := meta.GetByContext(c)

	requestBody, err := common.GetRequestBody(c)
	if err != nil {
		return openai.ErrorWrapper(err, "read_request_body_failed", http.StatusBadRequest)
	}
	videoRequest, err := parseVideoRequest(requestBody)
	if err != nil {
		return openai.ErrorWrapper(err, "invalid_video_request", http.StatusBadRequest)
	}

	// 模型映射
	meta.OriginModelName = videoRequest.Model
	videoRequest.Model, _ = getMappedModelName(videoRequest.Model, meta.ModelMapping)
	meta.ActualModelName = videoRequest.Model

	// 计费
	modelRatio := billingratio.GetModelRatio(videoRequest.Model, meta.ChannelType)
	// 分组×模型 二维定价（LTZY-API 新增，B3）
	groupRatio := billingratio.GetGroupRatioForModel(meta.Group, videoRequest.Model)
	// 用量阶梯倍率（LTZY-API 新增，B4）
	// RatioScale：倍率表以美元为基准，本系统按人民币计价，扣费前统一换算
	ratio := modelRatio * groupRatio * tieredRatio(videoRequest.Model, meta.UserId) * billingratio.RatioScale()
	quota := int64(ratio * videoQuotaUnit * float64(videoRequest.N))

	userQuota, err := model.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "get_user_quota_failed", http.StatusInternalServerError)
	}
	if userQuota-quota < 0 {
		return openai.ErrorWrapper(errors.New("user quota is not enough"), "insufficient_user_quota", http.StatusForbidden)
	}

	adaptor := relay.GetAdaptor(meta.APIType)
	if adaptor == nil {
		return openai.ErrorWrapper(fmt.Errorf("invalid api type: %d", meta.APIType), "invalid_api_type", http.StatusBadRequest)
	}
	adaptor.Init(meta)

	// 若模型名被映射，需要用改写后的请求体转发
	var body io.Reader = c.Request.Body
	if videoRequest.Model != meta.OriginModelName {
		mapped, err := json.Marshal(videoRequest.BuildForwardBody())
		if err != nil {
			return openai.ErrorWrapper(err, "marshal_video_request_failed", http.StatusInternalServerError)
		}
		body = strings.NewReader(string(mapped))
	}

	resp, err := adaptor.DoRequest(c, meta, body)
	if err != nil {
		logger.Errorf(ctx, "DoRequest failed: %s", err.Error())
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}
	if isErrorHappened(meta, resp) {
		return RelayErrorHandler(resp)
	}

	if err := relayVideoResponse(c, resp); err != nil {
		logger.Errorf(ctx, "relayVideoResponse failed: %s", err.Error())
		return openai.ErrorWrapper(err, "do_response_failed", http.StatusInternalServerError)
	}

	go postConsumeVideoQuota(ctx, meta, videoRequest.Model, quota, modelRatio, groupRatio)
	return nil
}

// parseVideoRequest 解析视频请求。只会话在 JSON 对象且含 model 时成功。
func parseVideoRequest(body []byte) (*relaymodel.VideoRequest, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, errors.New("请求体必须是合法的 JSON 对象")
	}
	videoRequest := &relaymodel.VideoRequest{Extra: map[string]any{}}
	if err := json.Unmarshal(body, videoRequest); err != nil {
		return nil, err
	}
	if videoRequest.Model == "" {
		return nil, errors.New("model is required")
	}
	if videoRequest.Prompt == "" && videoRequest.Image == "" {
		return nil, errors.New("prompt 或 image 至少需要一个")
	}
	if videoRequest.N < 1 {
		videoRequest.N = 1
	}
	// 收集未识别的扩展字段，原样透传
	known := map[string]bool{
		"model": true, "prompt": true, "negative_prompt": true, "image": true,
		"duration": true, "size": true, "fps": true, "seed": true, "n": true,
		"response_format": true, "user": true,
	}
	for key, value := range raw {
		if known[key] {
			continue
		}
		var v any
		if err := json.Unmarshal(value, &v); err == nil {
			videoRequest.Extra[key] = v
		}
	}
	return videoRequest, nil
}

// relayVideoResponse 原样透传上游响应（状态码、响应头与响应体）。
func relayVideoResponse(c *gin.Context, resp *http.Response) error {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	for k, values := range resp.Header {
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") {
			continue
		}
		for _, v := range values {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(body)
	return err
}

// postConsumeVideoQuota 结算视频生成额度并按次记录消费日志。
func postConsumeVideoQuota(ctx context.Context, meta *meta.Meta, modelName string, quota int64, modelRatio float64, groupRatio float64) {
	if err := model.PostConsumeTokenQuota(meta.TokenId, quota); err != nil {
		logger.Error(ctx, "error consuming token remain quota: "+err.Error())
	}
	if err := model.CacheUpdateUserQuota(ctx, meta.UserId); err != nil {
		logger.Error(ctx, "error update user quota cache: "+err.Error())
	}
	if quota == 0 {
		return
	}
	model.RecordConsumeLog(ctx, &model.Log{
		UserId:      meta.UserId,
		ChannelId:   meta.ChannelId,
		ModelName:   modelName,
		TokenName:   meta.TokenName,
		Quota:       int(quota),
		Content:     fmt.Sprintf("倍率：%.2f × %.2f (video)", modelRatio, groupRatio),
		IsStream:    false,
		ElapsedTime: 0,
	})
	model.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
	model.UpdateChannelUsedQuota(meta.ChannelId, quota)
	// 预算计数（LTZY-API 新增，B6）
	model.RecordBudgetUsage(meta.UserId, meta.TokenId, quota)
}
