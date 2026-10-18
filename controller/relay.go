package controller

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/common/helper"
	"github.com/xiaosu4610/LTZY-API/common/i18n"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"github.com/xiaosu4610/LTZY-API/common/metrics"
	"github.com/xiaosu4610/LTZY-API/middleware"
	dbmodel "github.com/xiaosu4610/LTZY-API/model"
	"github.com/xiaosu4610/LTZY-API/monitor"
	"github.com/xiaosu4610/LTZY-API/relay/controller"
	"github.com/xiaosu4610/LTZY-API/relay/model"
	"github.com/xiaosu4610/LTZY-API/relay/relaymode"
)

// https://platform.openai.com/docs/api-reference/chat

func relayHelper(c *gin.Context, relayMode int) *model.ErrorWithStatusCode {
	var err *model.ErrorWithStatusCode
	switch relayMode {
	case relaymode.ImagesGenerations:
		err = controller.RelayImageHelper(c, relayMode)
	case relaymode.AudioSpeech:
		fallthrough
	case relaymode.AudioTranslation:
		fallthrough
	case relaymode.AudioTranscription:
		err = controller.RelayAudioHelper(c, relayMode)
	case relaymode.Proxy:
		err = controller.RelayProxyHelper(c, relayMode)
	case relaymode.Rerank:
		err = controller.RelayRerankHelper(c)
	case relaymode.Responses:
		err = controller.RelayResponsesHelper(c)
	case relaymode.Videos:
		err = controller.RelayVideoHelper(c)
	// A6：files / fine_tuning / assistants / threads / 删除微调模型这批端点没有
	// 各自的适配逻辑，统一走「原样透传」控制器（计费口径见该文件注释）。
	case relaymode.Files, relaymode.FineTuning, relaymode.Assistants, relaymode.Threads, relaymode.Models:
		err = controller.RelayPassthroughHelper(c, relayMode)
	default:
		err = controller.RelayTextHelper(c)
	}
	return err
}

func Relay(c *gin.Context) {
	ctx := c.Request.Context()
	relayMode := relaymode.GetByPath(c.Request.URL.Path)
	if config.DebugEnabled {
		requestBody, _ := common.GetRequestBody(c)
		logger.Debugf(ctx, "request body: %s", string(requestBody))
	}
	channelId := c.GetInt(ctxkey.ChannelId)
	userId := c.GetInt(ctxkey.Id)
	bizErr := relayHelper(c, relayMode)
	if bizErr == nil {
		// A6：透传端点必须把上游错误原样回传，因此 relayHelper 只能返回 nil；
		// 但 nil 会被当成「成功」，上游 401/5xx 就永远进不了渠道健康统计。
		// 这里根据上下文里的标记补记一次渠道失败（日志、自动禁用、指标口径
		// 与其它端点保持一致），响应本身早已原样写出，不做任何改写。
		if upstreamErr := controller.PassthroughUpstreamError(c); upstreamErr != nil {
			metrics.RecordRelay(channelId, false)
			metrics.SetRelayLastFail(channelId, upstreamErr.Error.Message)
			go processChannelRelayError(ctx, userId, channelId, c.GetString(ctxkey.ChannelName), c.GetString(ctxkey.ChannelKey), *upstreamErr)
			return
		}
		monitor.Emit(channelId, true)
		metrics.RecordRelay(channelId, true)
		return
	}
	metrics.RecordRelay(channelId, false)
	metrics.SetRelayLastFail(channelId, bizErr.Error.Message)
	lastFailedChannelId := channelId
	channelName := c.GetString(ctxkey.ChannelName)
	group := c.GetString(ctxkey.Group)
	originalModel := c.GetString(ctxkey.OriginalModel)
	go processChannelRelayError(ctx, userId, channelId, channelName, c.GetString(ctxkey.ChannelKey), *bizErr)
	requestId := c.GetString(helper.RequestIdKey)
	retryTimes := config.RetryTimes
	if !shouldRetry(c, bizErr.StatusCode) {
		logger.Errorf(ctx, "relay error happen, status code is %d, won't retry in this case", bizErr.StatusCode)
		retryTimes = 0
	}
	for i := retryTimes; i > 0; i-- {
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, originalModel, i != retryTimes)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %+v", err)
			break
		}
		logger.Infof(ctx, "using channel #%d to retry (remain times %d)", channel.Id, i)
		if channel.Id == lastFailedChannelId {
			continue
		}
		middleware.SetupContextForSelectedChannel(c, channel, originalModel)
		requestBody, err := common.GetRequestBody(c)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		bizErr = relayHelper(c, relayMode)
		if bizErr == nil {
			monitor.Emit(c.GetInt(ctxkey.ChannelId), true)
			metrics.RecordRelay(c.GetInt(ctxkey.ChannelId), true)
			return
		}
		retryChannelId := c.GetInt(ctxkey.ChannelId)
		metrics.RecordRelay(retryChannelId, false)
		metrics.SetRelayLastFail(retryChannelId, bizErr.Error.Message)
		channelId := c.GetInt(ctxkey.ChannelId)
		lastFailedChannelId = channelId
		channelName := c.GetString(ctxkey.ChannelName)
		go processChannelRelayError(ctx, userId, channelId, channelName, c.GetString(ctxkey.ChannelKey), *bizErr)
	}
	if bizErr != nil {
		if bizErr.StatusCode == http.StatusTooManyRequests {
			bizErr.Error.Message = i18n.Translate(c, "relay.upstream_overloaded")
		}

		// BUG: bizErr is in race condition
		bizErr.Error.Message = helper.MessageWithRequestId(bizErr.Error.Message, requestId)
		c.JSON(bizErr.StatusCode, gin.H{
			"error": bizErr.Error,
		})
	}
}

func shouldRetry(c *gin.Context, statusCode int) bool {
	if _, ok := c.Get(ctxkey.SpecificChannelId); ok {
		return false
	}
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	if statusCode/100 == 5 {
		return true
	}
	if statusCode == http.StatusBadRequest {
		return false
	}
	if statusCode/100 == 2 {
		return false
	}
	return true
}

func processChannelRelayError(ctx context.Context, userId int, channelId int, channelName string, channelKey string, err model.ErrorWithStatusCode) {
	// 上游报错原文经常把密钥/令牌一起回显（且会随自动禁用通知发到邮件与 IM），
	// 所以进日志与进监控前先做一次凭据脱敏。
	safeMessage := common.MaskString(err.Message)
	logger.Errorf(ctx, "relay error (channel id %d, user id: %d): %s", channelId, userId, safeMessage)
	// 多密钥渠道（LTZY-API 新增）：把出问题的这一把密钥单独冷却，同渠道其它密钥不受影响。
	// 401/403 判定为这把密钥失效，冷却久一点；429 多半只是这把被限流，冷却一分钟即可。
	if channelKey != "" {
		switch err.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			dbmodel.MarkChannelKeyFailed(channelId, channelKey, dbmodel.KeyCooldownAuthFailed)
		case http.StatusTooManyRequests:
			dbmodel.MarkChannelKeyFailed(channelId, channelKey, dbmodel.KeyCooldownRateLimited)
		}
	}
	// https://platform.openai.com/docs/guides/error-codes/api-errors
	if monitor.ShouldDisableChannel(&err.Error, err.StatusCode) {
		monitor.DisableChannel(channelId, channelName, safeMessage)
	} else {
		monitor.Emit(channelId, false)
	}
}

func RelayNotImplemented(c *gin.Context) {
	err := model.Error{
		Message: "API not implemented",
		Type:    "one_api_error",
		Param:   "",
		Code:    "api_not_implemented",
	}
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": err,
	})
}

func RelayNotFound(c *gin.Context) {
	err := model.Error{
		Message: fmt.Sprintf("Invalid URL (%s %s)", c.Request.Method, c.Request.URL.Path),
		Type:    "invalid_request_error",
		Param:   "",
		Code:    "",
	}
	c.JSON(http.StatusNotFound, gin.H{
		"error": err,
	})
}
