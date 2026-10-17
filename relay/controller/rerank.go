// Package controller is a package for handling the relay controller
package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"

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

// RelayRerankHelper 处理重排（Rerank）请求：语义重排文档并返回相关性排序。
// 计费方式：按 query + documents 的 token 数计费（无补全 token）。
func RelayRerankHelper(c *gin.Context) *relaymodel.ErrorWithStatusCode {
	ctx := c.Request.Context()
	meta := meta.GetByContext(c)

	// 解析并校验请求
	rerankRequest := &relaymodel.RerankRequest{}
	if err := common.UnmarshalBodyReusable(c, rerankRequest); err != nil {
		return openai.ErrorWrapper(err, "invalid_rerank_request", http.StatusBadRequest)
	}
	if rerankRequest.Model == "" {
		return openai.ErrorWrapper(errors.New("model is required"), "missing_model", http.StatusBadRequest)
	}
	if rerankRequest.Query == "" {
		return openai.ErrorWrapper(errors.New("query is required"), "missing_query", http.StatusBadRequest)
	}
	if len(rerankRequest.Documents) == 0 {
		return openai.ErrorWrapper(errors.New("documents is required"), "missing_documents", http.StatusBadRequest)
	}

	// 模型映射
	meta.OriginModelName = rerankRequest.Model
	rerankRequest.Model, _ = getMappedModelName(rerankRequest.Model, meta.ModelMapping)
	meta.ActualModelName = rerankRequest.Model

	// 倍率
	modelRatio := billingratio.GetModelRatio(rerankRequest.Model, meta.ChannelType)
	groupRatio := billingratio.GetGroupRatio(meta.Group)
	ratio := modelRatio * groupRatio

	// token 统计：query + 所有 documents
	promptTokens := openai.CountTokenInput(rerankRequest.RerankAllText(), rerankRequest.Model)
	meta.PromptTokens = promptTokens

	// 预扣额度
	preConsumedQuota, bizErr := preConsumeRerankQuota(ctx, promptTokens, ratio, meta)
	if bizErr != nil {
		logger.Warnf(ctx, "preConsumeRerankQuota failed: %+v", *bizErr)
		return bizErr
	}

	adaptor := relay.GetAdaptor(meta.APIType)
	if adaptor == nil {
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		return openai.ErrorWrapper(fmt.Errorf("invalid api type: %d", meta.APIType), "invalid_api_type", http.StatusBadRequest)
	}
	adaptor.Init(meta)

	requestBody, err := json.Marshal(rerankRequest)
	if err != nil {
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		return openai.ErrorWrapper(err, "marshal_request_failed", http.StatusInternalServerError)
	}

	// 发起请求（上游 URL 由适配器按 /v1/rerank 路径拼装）
	resp, err := adaptor.DoRequest(c, meta, bytes.NewBuffer(requestBody))
	if err != nil {
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		logger.Errorf(ctx, "DoRequest failed: %s", err.Error())
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}
	if isErrorHappened(meta, resp) {
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		return RelayErrorHandler(resp)
	}

	// 响应透传（Rerank 为非流式 JSON 响应）
	if err := relayRerankResponse(c, resp); err != nil {
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		logger.Errorf(ctx, "relayRerankResponse failed: %s", err.Error())
		return openai.ErrorWrapper(err, "do_response_failed", http.StatusInternalServerError)
	}

	// 结算（仅 prompt token）
	usage := &relaymodel.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: 0,
		TotalTokens:      promptTokens,
	}
	go postConsumeRerankQuota(ctx, usage, meta, rerankRequest.Model, ratio, preConsumedQuota, modelRatio, groupRatio, c.GetInt(ctxkey.TokenTpm))
	return nil
}

// relayRerankResponse 原样透传上游响应体、状态码与响应头。
func relayRerankResponse(c *gin.Context, resp *http.Response) error {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	for k, values := range resp.Header {
		for _, v := range values {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(body)
	return err
}

func preConsumeRerankQuota(ctx context.Context, promptTokens int, ratio float64, meta *meta.Meta) (int64, *relaymodel.ErrorWithStatusCode) {
	preConsumedTokens := config.PreConsumedQuota + int64(promptTokens)
	preConsumedQuota := int64(float64(preConsumedTokens) * ratio)

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
		// 用户额度充裕，信任并跳过预扣
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

func postConsumeRerankQuota(ctx context.Context, usage *relaymodel.Usage, meta *meta.Meta, modelName string, ratio float64, preConsumedQuota int64, modelRatio float64, groupRatio float64, tokenTpm int) {
	if usage == nil {
		logger.Error(ctx, "usage is nil, which is unexpected")
		return
	}
	quota := int64(math.Ceil(float64(usage.PromptTokens) * ratio))
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
	logContent := fmt.Sprintf("倍率：%.2f × %.2f (rerank)", modelRatio, groupRatio)
	model.RecordConsumeLog(ctx, &model.Log{
		UserId:           meta.UserId,
		ChannelId:        meta.ChannelId,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: 0,
		ModelName:        modelName,
		TokenName:        meta.TokenName,
		Quota:            int(quota),
		Content:          logContent,
		IsStream:         false,
		ElapsedTime:      helper.CalcElapsedTime(meta.StartTime),
	})
	model.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
	model.UpdateChannelUsedQuota(meta.ChannelId, quota)
	// 预算计数（LTZY-API 新增，B6）
	model.RecordBudgetUsage(meta.UserId, meta.TokenId, quota)
	// 记录令牌本分钟 token 消耗（LTZY-API 新增）
	middleware.RecordTokenTPM(meta.TokenId, tokenTpm, usage.PromptTokens)
}
