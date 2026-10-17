// Package controller is a package for handling the relay controller
package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/common/helper"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"github.com/xiaosu4610/LTZY-API/middleware"
	"github.com/xiaosu4610/LTZY-API/model"
	"github.com/xiaosu4610/LTZY-API/relay"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/openai"
	"github.com/xiaosu4610/LTZY-API/relay/apitype"
	"github.com/xiaosu4610/LTZY-API/relay/billing"
	billingratio "github.com/xiaosu4610/LTZY-API/relay/billing/ratio"
	"github.com/xiaosu4610/LTZY-API/relay/meta"
	relaymodel "github.com/xiaosu4610/LTZY-API/relay/model"
	"github.com/xiaosu4610/LTZY-API/relay/relaymode"
)

// passthroughUpstreamErrorKey 上游「渠道级」错误的上下文键。
// 只在 relay/controller 与 controller 包之间用，不放进 ctxkey（那是公共协议）。
const passthroughUpstreamErrorKey = "ltzy_passthrough_upstream_error"

// passthroughProbe 只探测对路由/日志有用的字段，请求体本身**不做任何改写**。
type passthroughProbe struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

// passthroughUsageResponse 用于从 Run 对象里提取 model 与 usage，其余字段原样回传。
type passthroughUsageResponse struct {
	Model string `json:"model"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// RelayPassthroughHelper 处理 files / fine_tuning / assistants / threads / 删除微调模型
// 这批端点：请求体原样转发，响应体（含错误）原样回传，不做任何结构转换。
//
// 为什么不给每个端点写一套适配器：
// 这些端点的请求/响应都是上游自定义对象（File / RunStep / Assistant …），网关不需要
// 理解它们的语义，只需要「转出去、带回来」。逐个适配只会产出几十份结构相同的代码，
// 而且上游一改字段就要跟着改。真正需要网关理解语义的只有计费，见下。
//
// 计费口径（A6，需与文档保持一致）：
//   - **默认不计费**：files（上传/列表/下载/删除）、assistants 与 threads 的增删改查、
//     fine_tuning 任务管理都不产生模型 token 用量，扣费等于凭空收费。
//     但请求仍受令牌 scope / 限流 / 并发 / 预算等中间件约束（它们都在同一条路由链上）。
//   - **唯一例外**：创建 run（POST /v1/threads/{id}/runs 与
//     .../runs/{runId}/submit_tool_outputs）会真实产生模型用量。但 OpenAI 的
//     create-run 是异步的，返回的 Run 通常是 status=queued、**不带 usage**，
//     usage 只在终态 Run（或流式 thread.run.completed 事件）里出现。因此这里只做
//     「响应里确实带了 usage 才结算」，并且**不做预扣**：
//   - 预扣要先预估 token，而这些请求体里没有 messages，估不出来；预估了又没有用量，
//     就是白扣用户的钱；
//   - 流式 run 的 usage 埋在 SSE 事件里，本次不解析（解析需要缓冲/改写，
//     风险大于收益），即**流式 run 不计费**；
//   - 查询类端点（GET run / GET run step）即使返回带 usage 的终态 Run 也**不结算**：
//     同一个 run 查 N 次就会重复扣 N 次，宁可漏计也不能重复扣。要完整覆盖需要
//     「run → 请求」的关联表与状态机，超出本次范围（已在交付报告中列为遗留项）。
func RelayPassthroughHelper(c *gin.Context, relayMode int) *relaymodel.ErrorWithStatusCode {
	ctx := c.Request.Context()
	m := meta.GetByContext(c)

	// 这些端点是 OpenAI 私有 REST 形态，只有走 OpenAI 协议适配器的渠道才可能被正确
	// 拼出上游地址；挑到别的协议（Anthropic/Gemini…）只会发出一个面目全非的请求。
	// 与其让调用方拿到一个莫名其妙的上游报错，不如直接说明「该渠道类型不支持」。
	// 注意：这里返回 501 与改造前「所有端点统一 501」不是一回事 —— 这是网关侧的
	// 能力判定（可自助排查，带错误码），上游真实错误一律原样透传。
	if m.APIType != apitype.OpenAI {
		return openai.ErrorWrapper(
			fmt.Errorf("当前渠道类型（%d）不支持该端点：%s", m.ChannelType, c.Request.URL.Path),
			"unsupported_channel_type_for_endpoint", http.StatusNotImplemented)
	}

	// 请求体整块读出再转发，理由有两条：
	//   - common.GetRequestBody 会把 Body 读干净（读后必须自己重建，否则上游收到空体）；
	//   - 交给 http.NewRequest 的是 bytes.Reader 才能算出 Content-Length，避免退化成
	//     chunked 上传 —— multipart 文件上传对这一点尤其敏感。
	var requestBody []byte
	if c.Request.Body != nil {
		var err error
		requestBody, err = common.GetRequestBody(c)
		if err != nil {
			return openai.ErrorWrapper(err, "read_request_body_failed", http.StatusBadRequest)
		}
	}

	// 只为日志与计费探测 model/stream：这些端点大多没有 model 字段，探测失败一律忽略。
	probe := passthroughProbe{}
	_ = json.Unmarshal(requestBody, &probe)
	m.IsStream = probe.Stream
	if probe.Model != "" {
		m.OriginModelName = probe.Model
		if mapped, ok := getMappedModelName(probe.Model, m.ModelMapping); ok {
			m.ActualModelName = mapped
		} else {
			m.ActualModelName = probe.Model
		}
	}

	adaptor := relay.GetAdaptor(m.APIType)
	if adaptor == nil {
		return openai.ErrorWrapper(fmt.Errorf("invalid api type: %d", m.APIType), "invalid_api_type", http.StatusBadRequest)
	}
	adaptor.Init(m)

	resp, err := adaptor.DoRequest(c, m, bytes.NewReader(requestBody))
	if err != nil {
		logger.Errorf(ctx, "DoRequest failed: %s", err.Error())
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}
	if resp == nil {
		return openai.ErrorWrapper(errors.New("resp is nil"), "do_request_failed", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	return passthroughResponse(c, m, relayMode, resp)
}

// passthroughResponse 把上游响应原样回传给客户端。
//
// 注意这里**不区分成功与失败**：上游的错误（状态码 + 错误体）也原样回传。
// 调用方因此能自助排查（「该端点不存在」「文件太大」「该模型不支持微调」），
// 这正把这批端点从 501 占位改成透传的核心价值。
func passthroughResponse(c *gin.Context, m *meta.Meta, relayMode int, resp *http.Response) *relaymodel.ErrorWithStatusCode {
	// SSE（流式 run）一律边收边转：既不能把整条流缓冲起来，也不解析其中的 usage
	//（见 RelayPassthroughHelper 的计费口径说明）。
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return copyPassthroughStream(c, resp)
	}
	// 只有「错误响应」与「可能带 usage 的 run 创建响应」需要读进内存；其余一律流式拷贝。
	// 典型反例是 /v1/files/{id}/content：它可能返回几百 MB 的 jsonl 文件，
	// 读进内存只为原样写回去是纯粹的浪费与风险。
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && !isBillableRunCreate(c, relayMode) {
		return copyPassthroughStream(c, resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	writePassthroughResponse(c, resp, body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		markPassthroughUpstreamError(c, resp, body)
		return nil
	}
	consumeRunUsageIfPresent(c, m, relayMode, body)
	return nil
}

// writePassthroughResponse 回传状态码、响应头与响应体。
func writePassthroughResponse(c *gin.Context, resp *http.Response, body []byte) {
	copyPassthroughHeaders(c, resp.Header)
	c.Writer.WriteHeader(resp.StatusCode)
	if len(body) == 0 {
		return
	}
	if _, err := c.Writer.Write(body); err != nil {
		logger.SysError("passthrough write to client failed: " + err.Error())
	}
}

// copyPassthroughStream 边读边写（每块 Flush），用于 SSE 与大文件下载。
//
// 这里刻意不把读写错误往上抛：响应头已经发出去了，再返回一个错误只会让上层
// 往响应体里追加一段错误 JSON，把已经原样回传的内容破坏掉；而连接中断/客户端取消
// 也不是上游故障，不该记进渠道健康统计。所以只留日志、直接结束。
func copyPassthroughStream(c *gin.Context, resp *http.Response) *relaymodel.ErrorWithStatusCode {
	copyPassthroughHeaders(c, resp.Header)
	c.Writer.WriteHeader(resp.StatusCode)
	flusher, _ := c.Writer.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := c.Writer.Write(buf[:n]); writeErr != nil {
				logger.SysError("passthrough write to client failed: " + writeErr.Error())
				return nil
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				logger.SysError("passthrough read from upstream failed: " + err.Error())
			}
			return nil
		}
	}
}

// copyPassthroughHeaders 复制上游响应头。
//
// Content-Length / Transfer-Encoding 必须丢弃：前者可能与我们实际写出的字节数不一致
// （错误体读过、SSE 无长度），后者由 net/http 根据我们自己是否设置长度来决定分块方式。
func copyPassthroughHeaders(c *gin.Context, header http.Header) {
	for key, values := range header {
		if strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Transfer-Encoding") {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}
}

// isChannelFailureStatus 判断上游状态码是否属于「渠道级故障」，也就是值得记进渠道
// 健康统计、并可能触发自动禁用的那类。
//
// 上游 4xx 的大部分（400 参数错、404 端点或模型不存在、409 冲突、422 校验失败）是
// 调用方或上游能力边界的问题，不是渠道坏了；把它们算进去，会让「用户反复调用一个
// 上游本来就不支持的端点」把好渠道自动禁用。401/403/429 与 5xx 才是渠道自身的问题。
func isChannelFailureStatus(statusCode int) bool {
	return statusCode == http.StatusUnauthorized ||
		statusCode == http.StatusForbidden ||
		statusCode == http.StatusTooManyRequests ||
		statusCode >= 500
}

// markPassthroughUpstreamError 记录上游错误，供 controller.Relay 在「响应已经原样
// 回传」之后补做渠道健康统计与错误日志。
//
// 为什么要绕这一下：透传端点必须对 relayHelper 返回 nil（返回错误的话，Relay 会
// 把我们刚刚原样写出的上游错误体覆盖成网关自己的错误信封）。但返回 nil 会被 Relay
// 当成成功，上游 401/5xx 就永远进不了渠道健康统计。用上下文标记把这两件事拆开。
func markPassthroughUpstreamError(c *gin.Context, resp *http.Response, body []byte) {
	if !isChannelFailureStatus(resp.StatusCode) {
		// 非渠道级错误：响应已原样回传，只留一条便于排查的系统日志，不打扰渠道健康统计
		logger.SysLogf("passthrough endpoint %s returned upstream status %d: %s",
			c.Request.URL.Path, resp.StatusCode, extractUpstreamErrorMessage(body))
		return
	}
	c.Set(passthroughUpstreamErrorKey, &relaymodel.ErrorWithStatusCode{
		StatusCode: resp.StatusCode,
		Error: relaymodel.Error{
			Message: extractUpstreamErrorMessage(body),
			Type:    "upstream_error",
			Code:    "bad_response_status_code",
			Param:   strconv.Itoa(resp.StatusCode),
		},
	})
}

// PassthroughUpstreamError 返回透传端点记录的上游「渠道级」错误（没有则 nil）。
// 只给 controller.Relay 用：它需要知道「响应已经原样回传，但这次其实是上游失败」。
func PassthroughUpstreamError(c *gin.Context) *relaymodel.ErrorWithStatusCode {
	value, ok := c.Get(passthroughUpstreamErrorKey)
	if !ok {
		return nil
	}
	errWithStatus, _ := value.(*relaymodel.ErrorWithStatusCode)
	return errWithStatus
}

// extractUpstreamErrorMessage 从上游错误体里取出人可读的消息；取不到就退回截断后的原文。
// 上游原文经常把密钥一起回显，所以出口统一脱敏。
func extractUpstreamErrorMessage(body []byte) string {
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil {
		if parsed.Error.Message != "" {
			return common.MaskString(parsed.Error.Message)
		}
		if parsed.Message != "" {
			return common.MaskString(parsed.Message)
		}
		if parsed.Detail != "" {
			return common.MaskString(parsed.Detail)
		}
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "upstream returned an empty error body"
	}
	// 按 rune 截断，避免把多字节字符切成半个
	runes := []rune(trimmed)
	if len(runes) > 512 {
		trimmed = string(runes[:512]) + "…"
	}
	return common.MaskString(trimmed)
}

// isBillableRunCreate 判断本次请求是否为「会真实产生模型用量」的 run 创建调用。
//
// 只认两个 POST 路径：
//   - /v1/threads/{id}/runs
//   - /v1/threads/{id}/runs/{runId}/submit_tool_outputs
//
// 刻意不认 POST /v1/threads/{id}/runs/{runId}（修改 run）：它返回的可能是一个已经
// 跑到终态、带 usage 的 Run，在它上面结算会和创建时重复计费。
// 同理，GET 类查询端点一律不结算 —— 同一个 run 查几次就扣几次，宁可漏计。
func isBillableRunCreate(c *gin.Context, relayMode int) bool {
	if relayMode != relaymode.Threads || c.Request.Method != http.MethodPost {
		return false
	}
	segments := strings.Split(strings.Trim(c.Request.URL.Path, "/"), "/")
	if len(segments) < 4 || segments[0] != "v1" || segments[1] != "threads" || segments[3] != "runs" {
		return false
	}
	if len(segments) == 4 {
		return true
	}
	return len(segments) == 6 && segments[5] == "submit_tool_outputs"
}

// consumeRunUsageIfPresent 在响应体里找 Run 的 usage，找到才结算。
// 找不到（绝大多数情况：create-run 返回 status=queued）就不扣，也不估算 ——
// 估算出来的 token 数等于凭空收费。
func consumeRunUsageIfPresent(c *gin.Context, m *meta.Meta, relayMode int, body []byte) {
	if !isBillableRunCreate(c, relayMode) {
		return
	}
	parsed := passthroughUsageResponse{}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Usage == nil {
		return
	}
	promptTokens := parsed.Usage.PromptTokens
	completionTokens := parsed.Usage.CompletionTokens
	if promptTokens+completionTokens <= 0 {
		// usage 全 0（被取消、立即失败的 run）：没有用量就没有消费
		return
	}
	modelName := parsed.Model
	if modelName == "" {
		modelName = m.ActualModelName
	}
	if modelName == "" {
		modelName = m.OriginModelName
	}
	if modelName == "" {
		// 拿不到模型就查不到倍率，宁可不扣也不能按错的倍率扣
		logger.SysError("run usage present but model name is unknown, skip billing to avoid wrong charge")
		return
	}
	consumeRunQuota(c.Request.Context(), c, m, modelName, promptTokens, completionTokens)
}

// consumeRunQuota 按 usage 结算一次 run 消费。
// 因为不做预扣，这里只可能是「加扣」；倍率口径与文本中继（postConsumeQuota）保持一致，
// 复用统一的定价模型，避免出现第二套计费公式。
func consumeRunQuota(ctx context.Context, c *gin.Context, m *meta.Meta, modelName string, promptTokens int, completionTokens int) {
	modelRatio := billingratio.GetModelRatio(modelName, m.ChannelType)
	groupRatio := billingratio.GetGroupRatioForModel(m.Group, modelName)
	completionRatio := billingratio.GetCompletionRatio(modelName, m.ChannelType)
	pricingModel := billing.NewTextPricingModel(modelName, modelRatio, completionRatio, tieredRatio(modelName, m.UserId))
	quota := pricingModel.CalcQuota(billing.UsageInput{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
	}, groupRatio)
	if quota <= 0 {
		// 倍率为 0（免费模型）或计算结果异常：不写 0 额度日志，也不动用户余额
		return
	}
	if err := model.PostConsumeTokenQuota(m.TokenId, quota); err != nil {
		logger.Error(ctx, "error consuming token remain quota: "+err.Error())
	}
	if err := model.CacheUpdateUserQuota(ctx, m.UserId); err != nil {
		logger.Error(ctx, "error update user quota cache: "+err.Error())
	}
	model.RecordConsumeLog(ctx, &model.Log{
		UserId:           m.UserId,
		ChannelId:        m.ChannelId,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		ModelName:        modelName,
		TokenName:        m.TokenName,
		Quota:            int(quota),
		Content:          fmt.Sprintf("倍率：%.2f × %.2f × %.2f (assistants run)", modelRatio, groupRatio, completionRatio),
		IsStream:         m.IsStream,
		ElapsedTime:      helper.CalcElapsedTime(m.StartTime),
	})
	model.UpdateUserUsedQuotaAndRequestCount(m.UserId, quota)
	model.UpdateChannelUsedQuota(m.ChannelId, quota)
	// 预算计数（LTZY-API 新增，B6）
	model.RecordBudgetUsage(m.UserId, m.TokenId, quota)
	// 令牌本分钟 token 消耗（用于 TPM 统计/告警）
	middleware.RecordTokenTPM(m.TokenId, c.GetInt(ctxkey.TokenTpm), promptTokens+completionTokens)
}
