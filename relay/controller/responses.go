// Package controller is a package for handling the relay controller
package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/common/helper"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"github.com/xiaosu4610/LTZY-API/middleware"
	"github.com/xiaosu4610/LTZY-API/model"
	"github.com/xiaosu4610/LTZY-API/relay"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/openai"
	"github.com/xiaosu4610/LTZY-API/relay/billing"
	billingratio "github.com/xiaosu4610/LTZY-API/relay/billing/ratio"
	"github.com/xiaosu4610/LTZY-API/relay/meta"
	relaymodel "github.com/xiaosu4610/LTZY-API/relay/model"
)

// responsesUsage 对应 OpenAI Responses API 响应中的 usage 字段。
type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// responsesResponse 仅用于从响应体中提取 usage 与 model（其余字段透传）。
type responsesResponse struct {
	Model string          `json:"model"`
	Usage *responsesUsage `json:"usage"`
}

// RelayResponsesHelper 处理 OpenAI Responses API（/v1/responses）请求。
// 采用透传策略：请求体不做结构转换，直接转发至上游同名端点；
// 响应体原样回传给客户端，同时从中解析 usage 用于计费。
func RelayResponsesHelper(c *gin.Context) *relaymodel.ErrorWithStatusCode {
	ctx := c.Request.Context()
	meta := meta.GetByContext(c)

	// 读取原始请求体并解析模型名（仅用于路由/计费，不做转换）
	requestBody, err := common.GetRequestBody(c)
	if err != nil {
		return openai.ErrorWrapper(err, "read_request_body_failed", http.StatusBadRequest)
	}
	var probe struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(requestBody, &probe); err != nil {
		return openai.ErrorWrapper(err, "invalid_responses_request", http.StatusBadRequest)
	}
	if probe.Model == "" {
		return openai.ErrorWrapper(errors.New("model is required"), "missing_model", http.StatusBadRequest)
	}
	meta.IsStream = probe.Stream

	// 模型映射
	meta.OriginModelName = probe.Model
	probe.Model, _ = getMappedModelName(probe.Model, meta.ModelMapping)
	meta.ActualModelName = probe.Model

	// 倍率
	modelRatio := billingratio.GetModelRatio(probe.Model, meta.ChannelType)
	// 分组×模型 二维定价（LTZY-API 新增，B3）
	groupRatio := billingratio.GetGroupRatioForModel(meta.Group, probe.Model)
	ratio := modelRatio * groupRatio

	// 预扣额度（Responses 请求体非 chat messages，无法精确预估 token，
	// 仅按 PreConsumedQuota 兜底预估，最终以响应 usage 结算）
	preConsumedQuota, bizErr := preConsumeResponsesQuota(ctx, ratio, meta)
	if bizErr != nil {
		logger.Warnf(ctx, "preConsumeResponsesQuota failed: %+v", *bizErr)
		return bizErr
	}

	adaptor := relay.GetAdaptor(meta.APIType)
	if adaptor == nil {
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		return openai.ErrorWrapper(fmt.Errorf("invalid api type: %d", meta.APIType), "invalid_api_type", http.StatusBadRequest)
	}
	adaptor.Init(meta)

	// 透传请求（DoRequestHelper 会用 meta.RequestURLPath = /v1/responses 拼上游地址）
	resp, err := adaptor.DoRequest(c, meta, c.Request.Body)
	if err != nil {
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		logger.Errorf(ctx, "DoRequest failed: %s", err.Error())
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}
	if isErrorHappened(meta, resp) {
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		return RelayErrorHandler(resp)
	}

	// 响应处理：流式直接透传（含 SSE）；非流式解析 usage 后回传
	if meta.IsStream {
		if err := relayResponsesStream(c, resp); err != nil {
			logger.Errorf(ctx, "relayResponsesStream failed: %s", err.Error())
			return openai.ErrorWrapper(err, "do_response_failed", http.StatusInternalServerError)
		}
		// 流式响应在转发过程中难以稳定拿到最终 usage，这里以预扣为准不做二次结算。
		// 预扣额度即为本次消费（兜底），后续可增强为解析 SSE 中的 response.completed 事件。
		go postConsumeResponsesQuota(ctx, meta, probe.Model, ratio, preConsumedQuota, 0, 0, modelRatio, groupRatio, c.GetInt(ctxkey.TokenTpm))
		return nil
	}

	usage, err := relayResponsesResponse(c, resp)
	if err != nil {
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		logger.Errorf(ctx, "relayResponsesResponse failed: %s", err.Error())
		return openai.ErrorWrapper(err, "do_response_failed", http.StatusInternalServerError)
	}
	promptTokens, completionTokens := 0, 0
	if usage != nil {
		promptTokens = usage.InputTokens
		completionTokens = usage.OutputTokens
	}
	go postConsumeResponsesQuota(ctx, meta, probe.Model, ratio, preConsumedQuota, promptTokens, completionTokens, modelRatio, groupRatio, c.GetInt(ctxkey.TokenTpm))
	return nil
}

// relayResponsesStream 将上游 SSE 流原样透传给客户端（Responses API 的流式事件格式
// 与 Chat Completions 不同，这里不做格式转换，保持原生事件类型）。
func relayResponsesStream(c *gin.Context, resp *http.Response) error {
	defer resp.Body.Close()
	common.SetEventStreamHeaders(c)
	c.Writer.WriteHeader(resp.StatusCode)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := c.Writer.Write(buf[:n]); werr != nil {
				return werr
			}
			c.Writer.Flush()
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// relayResponsesResponse 读取非流式响应体，解析 usage 后原样回传给客户端。
func relayResponsesResponse(c *gin.Context, resp *http.Response) (*responsesUsage, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var parsed responsesResponse
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Usage != nil {
		// 成功解析到 usage
	}
	for k, values := range resp.Header {
		// 跳过会与实际写出的 body 长度不一致的头
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") {
			continue
		}
		for _, v := range values {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err := c.Writer.Write(body); err != nil {
		return nil, err
	}
	if parsed.Usage != nil {
		return parsed.Usage, nil
	}
	return nil, nil
}

func preConsumeResponsesQuota(ctx context.Context, ratio float64, meta *meta.Meta) (int64, *relaymodel.ErrorWithStatusCode) {
	preConsumedQuota := int64(float64(config.PreConsumedQuota) * ratio)

	userQuota, err := model.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return preConsumedQuota, openai.ErrorWrapper(err, "get_user_quota_failed", http.StatusInternalServerError)
	}
	if userQuota-preConsumedQuota < 0 {
		return preConsumedQuota, openai.ErrorWrapper(errors.New("user quota is not enough"), "insufficient_user_quota", http.StatusForbidden)
	}
	err = model.CacheDecreaseUserQuota(meta.UserId, preConsumedQuota)
	if err != nil {
		return preConsumedQuota, openai.ErrorWrapper(err, "decrease_user_quota_failed", http.StatusInternalServerError)
	}
	if userQuota > 100*preConsumedQuota {
		preConsumedQuota = 0
		logger.Info(ctx, fmt.Sprintf("user %d has enough quota %d, trusted and no need to pre-consume", meta.UserId, userQuota))
	}
	if preConsumedQuota > 0 {
		if err := model.PreConsumeTokenQuota(meta.TokenId, preConsumedQuota); err != nil {
			return preConsumedQuota, openai.ErrorWrapper(err, "pre_consume_token_quota_failed", http.StatusForbidden)
		}
	}
	return preConsumedQuota, nil
}

func postConsumeResponsesQuota(ctx context.Context, meta *meta.Meta, modelName string, ratio float64, preConsumedQuota int64, promptTokens int, completionTokens int, modelRatio float64, groupRatio float64, tokenTpm int) {
	completionRatio := billingratio.GetCompletionRatio(modelName, meta.ChannelType)
	quota := int64(math.Ceil((float64(promptTokens) + float64(completionTokens)*completionRatio) * ratio))
	if quota <= 0 {
		// 无法解析 usage（如流式）时，以预扣额度作为本次消费
		quota = preConsumedQuota
	}
	if ratio != 0 && quota <= 0 {
		quota = 1
	}
	quotaDelta := quota - preConsumedQuota
	if err := model.PostConsumeTokenQuota(meta.TokenId, quotaDelta); err != nil {
		logger.Error(ctx, "error consuming token remain quota: "+err.Error())
	}
	if err := model.CacheUpdateUserQuota(ctx, meta.UserId); err != nil {
		logger.Error(ctx, "error update user quota cache: "+err.Error())
	}
	logContent := fmt.Sprintf("倍率：%.2f × %.2f × %.2f (responses)", modelRatio, groupRatio, completionRatio)
	model.RecordConsumeLog(ctx, &model.Log{
		UserId:           meta.UserId,
		ChannelId:        meta.ChannelId,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		ModelName:        modelName,
		TokenName:        meta.TokenName,
		Quota:            int(quota),
		Content:          logContent,
		IsStream:         meta.IsStream,
		ElapsedTime:      helper.CalcElapsedTime(meta.StartTime),
	})
	model.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
	model.UpdateChannelUsedQuota(meta.ChannelId, quota)
	// 预算计数（LTZY-API 新增，B6）
	model.RecordBudgetUsage(meta.UserId, meta.TokenId, quota)
	// 记录令牌本分钟 token 消耗（LTZY-API 新增）
	middleware.RecordTokenTPM(meta.TokenId, tokenTpm, promptTokens+completionTokens)
}
