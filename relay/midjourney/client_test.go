package midjourney

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/model"
)

// setOptionMap 替换运行期配置表并在用例结束后还原。
// 上游地址/密钥都从 config.OptionMap 读取，因此单测必须能精确控制它。
func setOptionMap(t *testing.T, values map[string]string) {
	t.Helper()
	config.OptionMapRWMutex.Lock()
	previous := config.OptionMap
	config.OptionMap = values
	config.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		config.OptionMapRWMutex.Lock()
		config.OptionMap = previous
		config.OptionMapRWMutex.Unlock()
	})
}

// TestNewClientWithoutUpstream 未配置上游时必须返回明确错误，而不是 panic 或打到空地址。
func TestNewClientWithoutUpstream(t *testing.T) {
	setOptionMap(t, map[string]string{})

	client, err := NewClient()
	if !errors.Is(err, ErrUpstreamNotConfigured) {
		t.Fatalf("应为 ErrUpstreamNotConfigured，实际 %v", err)
	}
	if client != nil {
		t.Fatal("未配置上游时不应返回可用客户端")
	}
	// 错误信息要能让管理员知道该配哪个键
	if !strings.Contains(err.Error(), OptionUpstreamURL) {
		t.Fatalf("错误信息应指明缺失的配置项，实际：%s", err.Error())
	}
}

// TestNewClientTrimsConfig 配置里的首尾空白与结尾斜杠必须被规整，否则拼出的 URL 会非法。
func TestNewClientTrimsConfig(t *testing.T) {
	setOptionMap(t, map[string]string{
		OptionUpstreamURL: " https://mj.example.com/ ",
		OptionApiKey:      " sk-mj ",
	})

	client, err := NewClient()
	if err != nil {
		t.Fatalf("配置存在时不应报错：%v", err)
	}
	if client.BaseURL != "https://mj.example.com" {
		t.Fatalf("上游地址未规整：%q", client.BaseURL)
	}
	if client.ApiKey != "sk-mj" {
		t.Fatalf("密钥未规整：%q", client.ApiKey)
	}
	if client.HTTPClient == nil {
		t.Fatal("必须复用项目 HTTP 客户端，不应为 nil")
	}
}

// TestSubmitContentAction 覆盖内容型动作（imagine）的请求构造与响应解析。
func TestSubmitContentAction(t *testing.T) {
	var gotMethod, gotPath, gotSecret, gotAuth, gotContentType string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotSecret = r.Header.Get("mj-api-secret")
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"description":"Submit success","result":"1700000000000001"}`))
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL, "sk-mj")
	taskId, err := client.Submit(context.Background(), SubmitRequest{
		// 故意带空白与大写：Normalize 应把它们规整掉
		Action: " Imagine ",
		Prompt: "  a cat --ar 16:9  ",
	})
	if err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	if taskId != "1700000000000001" {
		t.Fatalf("任务号解析错误：%q", taskId)
	}
	if gotMethod != http.MethodPost || gotPath != "/mj/submit/imagine" {
		t.Fatalf("请求方法与路径错误：%s %s", gotMethod, gotPath)
	}
	if gotBody["prompt"] != "a cat --ar 16:9" {
		t.Fatalf("prompt 未按预期传递：%+v", gotBody)
	}
	// 内容型动作不应带按钮字段，否则上游会按按钮操作解析
	if _, ok := gotBody["customId"]; ok {
		t.Fatalf("imagine 不应携带 customId：%+v", gotBody)
	}
	if gotSecret != "sk-mj" {
		t.Fatalf("mj-api-secret 请求头错误：%q", gotSecret)
	}
	if gotAuth != "Bearer sk-mj" {
		t.Fatalf("Authorization 请求头错误：%q", gotAuth)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Fatalf("Content-Type 错误：%q", gotContentType)
	}
}

// TestSubmitCustomIdAction 按钮型动作（upscale）必须走 /mj/submit/action 并带上 customId。
func TestSubmitCustomIdAction(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"code":1,"description":"Submit success","result":"1700000000000002"}`))
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL, "")
	taskId, err := client.Submit(context.Background(), SubmitRequest{
		Action:   ActionUpscale,
		TaskId:   "1700000000000001",
		CustomId: "MJ::JOB::upsample::1::abc",
	})
	if err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	if taskId != "1700000000000002" {
		t.Fatalf("任务号解析错误：%q", taskId)
	}
	if gotPath != "/mj/submit/action" {
		t.Fatalf("按钮型动作应走 /mj/submit/action，实际 %s", gotPath)
	}
	if gotBody["customId"] != "MJ::JOB::upsample::1::abc" || gotBody["taskId"] != "1700000000000001" {
		t.Fatalf("请求体错误：%+v", gotBody)
	}
}

// TestSubmitAcceptsExistingTaskCode 覆盖 code=21（任务已存在）仍返回可用任务号的情况。
// 若按 code 判定成功，用户会看到「提交失败」，而任务其实已在上游运行并已被扣费。
func TestSubmitAcceptsExistingTaskCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":21,"description":"任务已存在","result":"1700000000000003"}`))
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL, "")
	taskId, err := client.Submit(context.Background(), SubmitRequest{Action: ActionImagine, Prompt: "cat"})
	if err != nil {
		t.Fatalf("result 非空时应视为受理成功：%v", err)
	}
	if taskId != "1700000000000003" {
		t.Fatalf("任务号解析错误：%q", taskId)
	}
}

// TestSubmitUpstreamDescriptionAsError 上游拒收时要把中文原因透出来。
func TestSubmitUpstreamDescriptionAsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":22,"description":"无可用账号，请稍后重试","result":""}`))
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL, "")
	_, err := client.Submit(context.Background(), SubmitRequest{Action: ActionImagine, Prompt: "cat"})
	if err == nil {
		t.Fatal("result 为空时应报错")
	}
	if !strings.Contains(err.Error(), "无可用账号") {
		t.Fatalf("错误信息应包含上游原因，实际：%s", err.Error())
	}
}

// TestSubmitRejectsInvalidRequest 参数校验必须在本地完成，不能把垃圾请求打给上游。
func TestSubmitRejectsInvalidRequest(t *testing.T) {
	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = w.Write([]byte(`{"code":1,"result":"x"}`))
	}))
	defer server.Close()
	client := NewClientWithBaseURL(server.URL, "")

	cases := []struct {
		name string
		req  SubmitRequest
		want string
	}{
		{"缺少 action", SubmitRequest{Prompt: "cat"}, "action 不能为空"},
		{"不支持的动作", SubmitRequest{Action: "video", Prompt: "cat"}, "不支持的 action"},
		{"imagine 缺少 prompt", SubmitRequest{Action: ActionImagine}, "需要提供 prompt"},
		{"upscale 缺少 customId", SubmitRequest{Action: ActionUpscale, TaskId: "t1"}, "需要提供 custom_id"},
	}
	for _, c := range cases {
		_, err := client.Submit(context.Background(), c.req)
		if err == nil {
			t.Fatalf("%s：应返回错误", c.name)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s：错误信息应包含 %q，实际 %q", c.name, c.want, err.Error())
		}
	}
	if hit {
		t.Fatal("参数非法的请求不应打到上游")
	}
}

// TestFetchParsesInProgressTask 覆盖进行中任务的响应解析（含 "50%" 形态的进度）。
func TestFetchParsesInProgressTask(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"id":"1700000000000001","action":"IMAGINE","status":"IN_PROGRESS","progress":"50%"}`))
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL, "")
	info, err := client.Fetch(context.Background(), "1700000000000001")
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if gotPath != "/mj/task/1700000000000001/fetch" {
		t.Fatalf("查询路径错误：%s", gotPath)
	}
	if info.StatusCode != model.MidjourneyStatusInProgress {
		t.Fatalf("状态映射错误：%d（原始 %q）", info.StatusCode, info.Status)
	}
	if info.Progress != 50 {
		t.Fatalf("进度解析错误：%d", info.Progress)
	}
	if info.Id != "1700000000000001" {
		t.Fatalf("任务号解析错误：%q", info.Id)
	}
}

// TestFetchParsesSuccessAndFallbackFields 成功态：进度 100、图片取 imageUrl、缺省时回退 uri。
func TestFetchParsesSuccessAndFallbackFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"t1","status":"SUCCESS","progress":100,"uri":"https://cdn.example.com/a.png"}`))
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL, "")
	info, err := client.Fetch(context.Background(), "t1")
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if info.StatusCode != model.MidjourneyStatusSuccess {
		t.Fatalf("状态映射错误：%d", info.StatusCode)
	}
	if info.Progress != 100 {
		t.Fatalf("数字形态进度解析错误：%d", info.Progress)
	}
	// 部分实现沿用 Discord 原生字段名 uri
	if info.ImageUrl != "https://cdn.example.com/a.png" {
		t.Fatalf("图片地址回退失败：%q", info.ImageUrl)
	}
}

// TestFetchParsesFailureReason 失败态要带回失败原因。
func TestFetchParsesFailureReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"t1","status":"FAILURE","progress":"80%","failReason":"内容违反社区准则"}`))
	}))
	defer server.Close()

	info, err := NewClientWithBaseURL(server.URL, "").Fetch(context.Background(), "t1")
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if info.StatusCode != model.MidjourneyStatusFailure || info.FailReason != "内容违反社区准则" {
		t.Fatalf("失败态解析错误：%+v", info)
	}
}

// TestFetchErrorEnvelopeWithHTTP200 有些网关把错误包成 {code,description} 并用 200 返回。
func TestFetchErrorEnvelopeWithHTTP200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":4,"description":"任务不存在"}`))
	}))
	defer server.Close()

	_, err := NewClientWithBaseURL(server.URL, "").Fetch(context.Background(), "missing")
	if err == nil {
		t.Fatal("错误信封应被识别为错误")
	}
	if !strings.Contains(err.Error(), "任务不存在") {
		t.Fatalf("错误信息应带上上游说明，实际：%s", err.Error())
	}
}

// TestFetchHTTPErrorStatus 非 2xx 响应要把状态码与上游说明一起带出。
func TestFetchHTTPErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"description":"上游内部错误"}`))
	}))
	defer server.Close()

	_, err := NewClientWithBaseURL(server.URL, "").Fetch(context.Background(), "t1")
	if err == nil {
		t.Fatal("HTTP 500 应报错")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "上游内部错误") {
		t.Fatalf("错误信息不完整：%s", err.Error())
	}
	if _, err := NewClientWithBaseURL(server.URL, "").Fetch(context.Background(), "  "); err == nil {
		t.Fatal("空 taskId 应报错而不是打上游")
	}
}

// TestMapUpstreamStatus 状态映射表，含未知状态的保守处理。
func TestMapUpstreamStatus(t *testing.T) {
	cases := map[string]int{
		"":            model.MidjourneyStatusSubmitted,
		"NOT_START":   model.MidjourneyStatusSubmitted,
		"SUBMITTED":   model.MidjourneyStatusSubmitted,
		"MODAL":       model.MidjourneyStatusSubmitted,
		"in_progress": model.MidjourneyStatusInProgress,
		"SUCCESS":     model.MidjourneyStatusSuccess,
		"FAILURE":     model.MidjourneyStatusFailure,
		"CANCEL":      model.MidjourneyStatusFailure,
		"WHAT_IS_NEW": model.MidjourneyStatusSubmitted, // 未知状态按已提交，绝不误判为失败
	}
	for upstream, want := range cases {
		if got := MapUpstreamStatus(upstream); got != want {
			t.Errorf("%q 应映射为 %d，实际 %d", upstream, want, got)
		}
	}
}

// TestSupportedActionsStable 动作白名单去重且稳定（错误提示与前端下拉都依赖它）。
func TestSupportedActionsStable(t *testing.T) {
	actions := SupportedActions()
	if len(actions) != len(promptActions)+len(customIdActions) {
		t.Fatalf("动作数量错误：%v", actions)
	}
	for i := 1; i < len(actions); i++ {
		if actions[i-1] >= actions[i] {
			t.Fatalf("动作列表应有序且无重复：%v", actions)
		}
	}
	if !IsPromptAction(ActionImagine) || IsPromptAction(ActionUpscale) {
		t.Fatal("IsPromptAction 判定错误")
	}
}
