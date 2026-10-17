package controller

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/client"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/relay/channeltype"
	"github.com/xiaosu4610/LTZY-API/relay/relaymode"
)

// A6 透传控制器的单测。
//
// 只起一个假上游（httptest.Server），验证三件事：
//  1. 请求方法/路径/查询串/请求体/关键请求头被原样转发；
//  2. 响应状态码/响应头/响应体被原样回传（含 multipart 上传与 SSE 这类流式响应）；
//  3. 上游报错时错误体原样透传，同时把「渠道级失败」登记给 Relay 层。

// upstreamSeen 记录假上游实际收到的请求，以及控制器是否登记了渠道级失败。
type upstreamSeen struct {
	method        string
	path          string
	rawQuery      string
	contentType   string
	authorization string
	body          []byte
	// markedUpstreamError 非 nil 表示控制器把本次响应登记为上游渠道级错误
	markedUpstreamError *int
}

// newPassthroughTestRouter 搭出「假上游 + 透传控制器」的最小链路。
// 上下文里预置 TokenAuth / Distribute / ModelessChannelDispatch 本该写好的键，
// 这样测的就是控制器本身，而不是一整套中间件。
func newPassthroughTestRouter(t *testing.T, channelType int, reply http.HandlerFunc) (*gin.Engine, *upstreamSeen) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	// 中继用的是全局 HTTP 客户端，单测里没有 main 的初始化流程，这里补上
	client.Init()
	seen := &upstreamSeen{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.method = r.Method
		seen.path = r.URL.Path
		seen.rawQuery = r.URL.RawQuery
		seen.contentType = r.Header.Get("Content-Type")
		seen.authorization = r.Header.Get("Authorization")
		seen.body, _ = io.ReadAll(r.Body)
		reply(w, r)
	}))
	t.Cleanup(server.Close)

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(ctxkey.Channel, channelType)
		c.Set(ctxkey.ChannelId, 7)
		c.Set(ctxkey.ChannelName, "test-channel")
		c.Set(ctxkey.BaseURL, server.URL)
		c.Set(ctxkey.Group, "default")
		c.Set(ctxkey.Id, 1)
		c.Set(ctxkey.TokenId, 1)
		c.Set(ctxkey.TokenName, "test-token")
		// Distribute 选中渠道后会把 Authorization 换成渠道密钥
		c.Request.Header.Set("Authorization", "Bearer sk-upstream")
		c.Next()
	})
	engine.Any("/v1/*rest", func(c *gin.Context) {
		bizErr := RelayPassthroughHelper(c, relaymode.GetByPath(c.Request.URL.Path))
		if upstreamErr := PassthroughUpstreamError(c); upstreamErr != nil {
			status := upstreamErr.StatusCode
			seen.markedUpstreamError = &status
		}
		if bizErr != nil {
			c.JSON(bizErr.StatusCode, gin.H{"error": bizErr.Error})
		}
	})
	return engine, seen
}

func doPassthrough(engine *gin.Engine, method string, target string, contentType string, body []byte) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	return recorder
}

// TestPassthroughForwardsRequestVerbatim 请求体/方法/路径/查询串/请求头原样转发，
// 响应状态码/响应头/响应体原样回传。
func TestPassthroughForwardsRequestVerbatim(t *testing.T) {
	responseBody := `{"id":"msg_1","object":"thread.message"}`
	engine, seen := newPassthroughTestRouter(t, channeltype.OpenAI, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Header", "kept")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responseBody))
	})

	requestBody := []byte(`{"role":"user","content":"hello","metadata":{"k":"v"}}`)
	recorder := doPassthrough(engine, http.MethodPost, "/v1/threads/thread_1/messages?limit=20&order=desc", "application/json", requestBody)

	if recorder.Code != http.StatusOK {
		t.Fatalf("客户端应拿到 200，实际 %d，响应体 %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Body.String(); got != responseBody {
		t.Fatalf("响应体应原样回传，实际 %s", got)
	}
	if got := recorder.Header().Get("X-Upstream-Header"); got != "kept" {
		t.Fatalf("上游响应头应原样回传，实际 %q", got)
	}
	if seen.method != http.MethodPost {
		t.Fatalf("上游应收到 POST，实际 %s", seen.method)
	}
	if seen.path != "/v1/threads/thread_1/messages" {
		t.Fatalf("上游路径应原样保留，实际 %s", seen.path)
	}
	if seen.rawQuery != "limit=20&order=desc" {
		t.Fatalf("查询串应原样保留，实际 %s", seen.rawQuery)
	}
	if !bytes.Equal(seen.body, requestBody) {
		t.Fatalf("请求体应逐字节一致，实际 %s", string(seen.body))
	}
	if seen.contentType != "application/json" {
		t.Fatalf("Content-Type 应原样转发，实际 %s", seen.contentType)
	}
	if seen.authorization != "Bearer sk-upstream" {
		t.Fatalf("应使用渠道密钥请求上游，实际 %s", seen.authorization)
	}
	if seen.markedUpstreamError != nil {
		t.Fatalf("200 响应不该被登记为上游失败，实际 %d", *seen.markedUpstreamError)
	}
}

// TestPassthroughForwardsMultipartUpload 文件上传（multipart）必须逐字节转发。
// 这是最容易出问题的一类：body 被读过一次就容易变成空体，Content-Type 里的
// boundary 一旦丢失，上游就无法解析。
func TestPassthroughForwardsMultipartUpload(t *testing.T) {
	engine, seen := newPassthroughTestRouter(t, channeltype.OpenAI, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"file-1","object":"file","purpose":"fine-tune"}`))
	})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "train.jsonl")
	if err != nil {
		t.Fatalf("构造 multipart 失败：%v", err)
	}
	if _, err := part.Write([]byte("{\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}\n")); err != nil {
		t.Fatalf("写入文件内容失败：%v", err)
	}
	if err := writer.WriteField("purpose", "fine-tune"); err != nil {
		t.Fatalf("写入表单字段失败：%v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 multipart writer 失败：%v", err)
	}
	contentType := writer.FormDataContentType()

	recorder := doPassthrough(engine, http.MethodPost, "/v1/files", contentType, body.Bytes())
	if recorder.Code != http.StatusOK {
		t.Fatalf("上传应返回 200，实际 %d，响应体 %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Equal(seen.body, body.Bytes()) {
		t.Fatalf("multipart 请求体应逐字节一致：期望 %d 字节，实际 %d 字节", body.Len(), len(seen.body))
	}
	if seen.contentType != contentType {
		t.Fatalf("multipart Content-Type（含 boundary）应原样转发：期望 %s，实际 %s", contentType, seen.contentType)
	}
}

// TestPassthroughRelaysUpstreamErrorVerbatim 上游 4xx 错误必须原样回传
// （状态码 + 错误体都不许被网关改写），且不该被算成渠道故障。
func TestPassthroughRelaysUpstreamErrorVerbatim(t *testing.T) {
	upstreamBody := `{"error":{"message":"Invalid URL (POST /v1/assistants)","type":"invalid_request_error","param":null,"code":"unknown_url"}}`
	engine, seen := newPassthroughTestRouter(t, channeltype.OpenAI, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(upstreamBody))
	})

	recorder := doPassthrough(engine, http.MethodPost, "/v1/assistants", "application/json", []byte(`{"model":"gpt-4o-mini"}`))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("上游 404 应原样回传，实际 %d", recorder.Code)
	}
	if got := recorder.Body.String(); got != upstreamBody {
		t.Fatalf("上游错误体应逐字节一致：\n期望 %s\n实际 %s", upstreamBody, got)
	}
	if seen.markedUpstreamError != nil {
		t.Fatalf("上游 404（端点不支持/参数错）不是渠道级故障，不该登记为失败，实际 %d", *seen.markedUpstreamError)
	}
}

// TestPassthroughMarksChannelLevelFailure 上游 5xx / 401 属于渠道级故障：
// 响应原样回传的同时必须登记失败，供 controller.Relay 做渠道健康统计。
func TestPassthroughMarksChannelLevelFailure(t *testing.T) {
	upstreamBody := `{"error":{"message":"upstream exploded","type":"server_error"}}`
	engine, seen := newPassthroughTestRouter(t, channeltype.OpenAI, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(upstreamBody))
	})

	recorder := doPassthrough(engine, http.MethodGet, "/v1/files", "", nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("上游 500 应原样回传，实际 %d", recorder.Code)
	}
	if got := recorder.Body.String(); got != upstreamBody {
		t.Fatalf("上游错误体应原样回传，实际 %s", got)
	}
	if seen.markedUpstreamError == nil || *seen.markedUpstreamError != http.StatusInternalServerError {
		t.Fatalf("上游 500 应登记渠道级失败，实际 %v", seen.markedUpstreamError)
	}
}

// TestPassthroughStreamsEventStream SSE 响应必须边收边转（原样），不能被缓冲改写。
func TestPassthroughStreamsEventStream(t *testing.T) {
	events := "event: thread.run.completed\n" +
		"data: {\"id\":\"run_1\",\"status\":\"completed\",\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5,\"total_tokens\":8}}\n\n" +
		"data: [DONE]\n\n"
	engine, seen := newPassthroughTestRouter(t, channeltype.OpenAI, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(events))
	})

	recorder := doPassthrough(engine, http.MethodPost, "/v1/threads/thread_1/runs", "application/json", []byte(`{"assistant_id":"asst_1","stream":true}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("流式 run 应返回 200，实际 %d", recorder.Code)
	}
	if got := recorder.Body.String(); got != events {
		t.Fatalf("SSE 事件应逐字节原样回传：\n期望 %q\n实际 %q", events, got)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("SSE 的 Content-Type 应保留，实际 %q", got)
	}
	// 流式 run 的 usage 埋在 SSE 里，本次明确不计费（见控制器注释），
	// 因此这里既不该有失败标记，也不该有消费日志产生。
	if seen.markedUpstreamError != nil {
		t.Fatalf("流式成功响应不该被登记为失败，实际 %d", *seen.markedUpstreamError)
	}
}

// TestPassthroughRejectsNonOpenAIProtocolChannel 非 OpenAI 协议渠道应快速失败，
// 而不是把一个改得面目全非的请求发给上游。
func TestPassthroughRejectsNonOpenAIProtocolChannel(t *testing.T) {
	engine, seen := newPassthroughTestRouter(t, channeltype.Anthropic, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("非 OpenAI 协议渠道不应该请求到上游")
	})

	recorder := doPassthrough(engine, http.MethodGet, "/v1/files", "", nil)
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("非 OpenAI 协议渠道应返回 501 并说明原因，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "unsupported_channel_type_for_endpoint") {
		t.Fatalf("错误里应带上可自助排查的错误码，实际 %s", recorder.Body.String())
	}
	if seen.path != "" {
		t.Fatalf("不该真的请求上游，实际请求了 %s", seen.path)
	}
}
