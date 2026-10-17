package message

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xiaosu4610/LTZY-API/common/config"
)

// recordedRequest 假服务收到的一次请求，用于断言请求方法、路径、查询参数与请求体
type recordedRequest struct {
	method      string
	path        string
	contentType string
	rawQuery    string
	query       url.Values
	body        []byte
}

type requestRecorder struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (recorder *requestRecorder) add(req recordedRequest) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.requests = append(recorder.requests, req)
}

func (recorder *requestRecorder) all() []recordedRequest {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]recordedRequest(nil), recorder.requests...)
}

// newFakeServer 启动本地假服务：既能断言渠道发出的请求，也能用 status 模拟下游异常
func newFakeServer(t *testing.T, status int, responseBody string) (*httptest.Server, *requestRecorder) {
	t.Helper()
	recorder := &requestRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read request body: %v", err)
		}
		recorder.add(recordedRequest{
			method:      r.Method,
			path:        r.URL.Path,
			contentType: r.Header.Get("Content-Type"),
			rawQuery:    r.URL.RawQuery,
			query:       r.URL.Query(),
			body:        body,
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(responseBody))
	}))
	t.Cleanup(server.Close)
	return server, recorder
}

// setOptions 重置 OptionMap，避免用例之间互相污染。
// 该 map 由多个 goroutine 共享，写入必须持锁。
func setOptions(t *testing.T, options map[string]string) {
	t.Helper()
	config.OptionMapRWMutex.Lock()
	config.OptionMap = make(map[string]string, len(options))
	for key, value := range options {
		config.OptionMap[key] = value
	}
	config.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		config.OptionMapRWMutex.Lock()
		config.OptionMap = make(map[string]string)
		config.OptionMapRWMutex.Unlock()
	})
}

func TestSendSkippedWhenURLNotConfigured(t *testing.T) {
	setOptions(t, nil)
	senders := map[string]func(string, string, string) error{
		"webhook":  SendWebhook,
		"lark":     SendLark,
		"dingtalk": SendDingTalk,
		"slack":    SendSlack,
	}
	for name, send := range senders {
		if err := send("标题", "描述", "正文"); err != nil {
			t.Errorf("%s: URL 未配置时应直接跳过且不报错，实际返回 %v", name, err)
		}
	}
}

func TestNotifyExtraChannelsSkipsUnconfiguredChannels(t *testing.T) {
	server, recorder := newFakeServer(t, http.StatusOK, `{"success":true}`)
	setOptions(t, map[string]string{optionWebhookURL: server.URL})

	NotifyExtraChannels("标题", "描述", "正文")

	if got := len(recorder.all()); got != 1 {
		t.Fatalf("只配置了通用 Webhook，应只发出 1 次请求，实际 %d 次", got)
	}
}

func TestSendWebhookRequestBody(t *testing.T) {
	server, recorder := newFakeServer(t, http.StatusOK, `{"success":true}`)
	setOptions(t, map[string]string{optionWebhookURL: server.URL + "/notify"})

	if err := SendWebhook("渠道状态变更提醒", "渠道 #1 已被禁用", "<p>正文</p>"); err != nil {
		t.Fatalf("SendWebhook 返回错误: %v", err)
	}

	requests := recorder.all()
	if len(requests) != 1 {
		t.Fatalf("期望 1 次请求，实际 %d 次", len(requests))
	}
	req := requests[0]
	if req.method != http.MethodPost {
		t.Errorf("期望 POST，实际 %s", req.method)
	}
	if req.path != "/notify" {
		t.Errorf("不应改动配置的 URL 路径，期望 /notify，实际 %s", req.path)
	}
	if !strings.HasPrefix(req.contentType, "application/json") {
		t.Errorf("期望 application/json，实际 %s", req.contentType)
	}
	var payload webhookRequest
	if err := json.Unmarshal(req.body, &payload); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v, body=%s", err, req.body)
	}
	if payload.Title != "渠道状态变更提醒" || payload.Description != "渠道 #1 已被禁用" || payload.Content != "<p>正文</p>" {
		t.Errorf("请求体字段不符合预期: %+v", payload)
	}
}

func TestSendLarkRequestBody(t *testing.T) {
	server, recorder := newFakeServer(t, http.StatusOK, `{"code":0,"msg":"success"}`)
	setOptions(t, map[string]string{optionLarkWebhookURL: server.URL})

	if err := SendLark("渠道状态变更提醒", "描述", "正文"); err != nil {
		t.Fatalf("SendLark 返回错误: %v", err)
	}

	requests := recorder.all()
	if len(requests) != 1 {
		t.Fatalf("期望 1 次请求，实际 %d 次", len(requests))
	}
	var payload larkRequest
	if err := json.Unmarshal(requests[0].body, &payload); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v, body=%s", err, requests[0].body)
	}
	if payload.MsgType != "text" {
		t.Errorf("msg_type 期望 text，实际 %s", payload.MsgType)
	}
	if payload.Content.Text != "渠道状态变更提醒\n正文" {
		t.Errorf("文本内容不符合预期: %q", payload.Content.Text)
	}
}

func TestSendSlackRequestBody(t *testing.T) {
	server, recorder := newFakeServer(t, http.StatusOK, "ok")
	setOptions(t, map[string]string{optionSlackWebhookURL: server.URL})

	if err := SendSlack("渠道状态变更提醒", "描述", "正文"); err != nil {
		t.Fatalf("SendSlack 返回错误: %v", err)
	}

	requests := recorder.all()
	if len(requests) != 1 {
		t.Fatalf("期望 1 次请求，实际 %d 次", len(requests))
	}
	var payload slackRequest
	if err := json.Unmarshal(requests[0].body, &payload); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v, body=%s", err, requests[0].body)
	}
	if payload.Text != "渠道状态变更提醒\n正文" {
		t.Errorf("文本内容不符合预期: %q", payload.Text)
	}
}

func TestSendDingTalkWithoutSecret(t *testing.T) {
	server, recorder := newFakeServer(t, http.StatusOK, `{"errcode":0,"errmsg":"ok"}`)
	setOptions(t, map[string]string{optionDingTalkWebhookURL: server.URL + "/robot/send?access_token=test-token"})

	if err := SendDingTalk("渠道状态变更提醒", "描述", "正文"); err != nil {
		t.Fatalf("SendDingTalk 返回错误: %v", err)
	}

	requests := recorder.all()
	if len(requests) != 1 {
		t.Fatalf("期望 1 次请求，实际 %d 次", len(requests))
	}
	req := requests[0]
	if req.query.Get("access_token") != "test-token" {
		t.Errorf("已有的 access_token 查询参数被破坏: %s", req.rawQuery)
	}
	if req.query.Get("timestamp") != "" || req.query.Get("sign") != "" {
		t.Errorf("未配置密钥时不应加签: %s", req.rawQuery)
	}
	var payload dingTalkRequest
	if err := json.Unmarshal(req.body, &payload); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v, body=%s", err, req.body)
	}
	if payload.MsgType != "text" || payload.Text.Content != "渠道状态变更提醒\n正文" {
		t.Errorf("请求体不符合预期: %+v", payload)
	}
}

func TestSendDingTalkSign(t *testing.T) {
	const secret = "SEC0000testsecret"
	server, recorder := newFakeServer(t, http.StatusOK, `{"errcode":0,"errmsg":"ok"}`)
	setOptions(t, map[string]string{
		optionDingTalkWebhookURL: server.URL + "/robot/send?access_token=test-token",
		optionDingTalkSecret:     secret,
	})

	if err := SendDingTalk("标题", "描述", "正文"); err != nil {
		t.Fatalf("SendDingTalk 返回错误: %v", err)
	}

	requests := recorder.all()
	if len(requests) != 1 {
		t.Fatalf("期望 1 次请求，实际 %d 次", len(requests))
	}
	req := requests[0]
	timestamp := req.query.Get("timestamp")
	if timestamp == "" {
		t.Fatalf("配置密钥后必须带 timestamp 参数: %s", req.rawQuery)
	}
	// 独立于被测代码重新计算签名，验证算法本身（secret 为密钥，原文为 "timestamp\nsecret"）
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "\n" + secret))
	wantSign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if got := req.query.Get("sign"); got != wantSign {
		t.Errorf("签名不匹配: got %q want %q", got, wantSign)
	}
	// 钉钉要求 sign 做 URL encode：raw query 里应是百分号转义后的 Base64
	// （漏编码或重复编码都会让接收端解出的值对不上）
	if !strings.Contains(req.rawQuery, "sign="+url.QueryEscape(wantSign)) {
		t.Errorf("sign 未按钉钉要求做 URL 编码: %s", req.rawQuery)
	}
	if req.query.Get("access_token") != "test-token" {
		t.Errorf("已有的 access_token 查询参数被破坏: %s", req.rawQuery)
	}
	// 时间戳必须与签名用同一个值，且不能是写死的旧时间（钉钉校验 1 小时偏差）
	millis, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		t.Fatalf("timestamp 不是毫秒时间戳: %q", timestamp)
	}
	if delta := time.Now().UnixMilli() - millis; delta < 0 || delta > 5000 {
		t.Errorf("timestamp 与当前时间偏差过大: %d ms", delta)
	}
}

func TestNotifyExtraChannelsIsolatesFailures(t *testing.T) {
	failing, failingRecorder := newFakeServer(t, http.StatusInternalServerError, "boom")
	working, workingRecorder := newFakeServer(t, http.StatusOK, `{"code":0,"msg":"success"}`)
	setOptions(t, map[string]string{
		optionWebhookURL:     failing.URL,
		optionLarkWebhookURL: working.URL,
	})

	// 前面的渠道失败不能 panic，也不能阻断后面的渠道
	NotifyExtraChannels("标题", "描述", "正文")

	if got := len(failingRecorder.all()); got != 1 {
		t.Errorf("失败的渠道也应被调用 1 次，实际 %d 次", got)
	}
	if got := len(workingRecorder.all()); got != 1 {
		t.Errorf("前一个渠道失败后，后面的渠道仍应被调用，实际 %d 次", got)
	}
}

func TestBotResponseBusinessError(t *testing.T) {
	cases := []struct {
		name     string
		response string
		wantErr  bool
	}{
		{"飞书成功", `{"code":0,"msg":"success"}`, false},
		{"飞书业务错误", `{"code":19001,"msg":"param invalid"}`, true},
		{"飞书旧版成功", `{"StatusCode":0,"StatusMessage":"success"}`, false},
		{"飞书旧版错误", `{"StatusCode":9499,"StatusMessage":"Bad Request"}`, true},
		{"钉钉成功", `{"errcode":0,"errmsg":"ok"}`, false},
		{"钉钉加签失败", `{"errcode":310000,"errmsg":"sign not match"}`, true},
		{"非 JSON 响应", `ok`, false},
	}
	for _, c := range cases {
		err := checkBotResponse("test", []byte(c.response))
		if (err != nil) != c.wantErr {
			t.Errorf("%s: wantErr=%v, 实际 err=%v", c.name, c.wantErr, err)
		}
	}
}
