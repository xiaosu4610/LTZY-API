// Package midjourney 封装对「MJ 上游代理」的 HTTP 调用（LTZY-API 新增，A4）。
//
// 本系统不直连 Discord，只把任务转发给管理员配置的代理服务（如 midjourney-proxy）。
// 因此这里刻意只做三件事：读配置、拼请求、解析响应；
// 任务归属、状态机、计费全部留在 model / controller 层，
// 换一个上游代理实现时只需要改这个包，不会牵动计费与权限逻辑。
package midjourney

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/xiaosu4610/LTZY-API/common/client"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/model"
)

// 配置项键名。注意这些键没有注册进 model/option.go 的默认列表（该文件由他人并行维护），
// 未注册的键读出来是空字符串而不是报错，所以这里读不到就按「未配置」处理。
const (
	OptionUpstreamURL = "MidjourneyUpstreamURL"
	OptionApiKey      = "MidjourneyApiKey"
)

// 动作常量。取值即上游 /mj/submit/<action> 的路径片段，避免两处各写一份字符串。
const (
	ActionImagine   = "imagine"
	ActionBlend     = "blend"
	ActionDescribe  = "describe"
	ActionShorten   = "shorten"
	ActionUpscale   = "upscale"
	ActionVariation = "variation"
	ActionReroll    = "reroll"
	ActionPan       = "pan"
	ActionZoom      = "zoom"
)

// promptActions 内容型动作：直接提交提示词，走 /mj/submit/<action>。
var promptActions = map[string]bool{
	ActionImagine:  true,
	ActionBlend:    true,
	ActionDescribe: true,
	ActionShorten:  true,
}

// customIdActions 按钮型动作：必须带上游按钮凭据 customId（有时还要 taskId），
// 统一走 /mj/submit/action —— midjourney-proxy 只对「内容型」动作暴露了同名路径。
var customIdActions = map[string]bool{
	ActionUpscale:   true,
	ActionVariation: true,
	ActionReroll:    true,
	ActionPan:       true,
	ActionZoom:      true,
}

// maxUpstreamBodyBytes 上游响应体读取上限。
// 上游异常时可能返回巨大的 HTML 错误页，无上限读取会把内存吃满。
const maxUpstreamBodyBytes = 1 << 20

// ErrUpstreamNotConfigured 上游未配置。
//
// 单独导出这个错误而不是返回普通 error：控制器要靠它区分「配置缺失（应引导管理员）」
// 与「上游临时故障（应提示稍后重试）」，两者的用户提示语义完全不同。
var ErrUpstreamNotConfigured = errors.New("未配置 Midjourney 上游代理地址（系统设置项 MidjourneyUpstreamURL），请联系管理员")

// GetUpstreamConfig 读取上游地址与密钥。
//
// 加读锁的原因与 relay/billing/pricing.go 一致：OptionMap 会被管理面在运行时整体替换，
// 直接读 map 与写操作并发会触发 Go 的并发 map 读写崩溃。
func GetUpstreamConfig() (baseURL string, apiKey string) {
	config.OptionMapRWMutex.RLock()
	defer config.OptionMapRWMutex.RUnlock()
	baseURL = strings.TrimRight(strings.TrimSpace(config.OptionMap[OptionUpstreamURL]), "/")
	apiKey = strings.TrimSpace(config.OptionMap[OptionApiKey])
	return baseURL, apiKey
}

// SubmitRequest 一次任务提交请求（本站字段名，与上游 JSON 字段名解耦）。
type SubmitRequest struct {
	Action   string `json:"action"`
	Prompt   string `json:"prompt"`
	TaskId   string `json:"task_id"`   // 按钮型操作指向的既有上游任务号
	CustomId string `json:"custom_id"` // 上游按钮凭据
}

// Normalize 校验并规范化请求。
//
// 返回副本而不是原地修改：调用方（控制器）随后还要用用户的原始输入记日志/建记录，
// 规范化顺带 Trim 会让「用户到底提交了什么」失真。
func (r SubmitRequest) Normalize() (*SubmitRequest, error) {
	normalized := r
	normalized.Action = strings.ToLower(strings.TrimSpace(r.Action))
	normalized.Prompt = strings.TrimSpace(r.Prompt)
	normalized.TaskId = strings.TrimSpace(r.TaskId)
	normalized.CustomId = strings.TrimSpace(r.CustomId)

	if normalized.Action == "" {
		return nil, errors.New("action 不能为空")
	}
	switch {
	case promptActions[normalized.Action]:
		if normalized.Prompt == "" {
			return nil, fmt.Errorf("动作 %s 需要提供 prompt", normalized.Action)
		}
	case customIdActions[normalized.Action]:
		if normalized.CustomId == "" {
			return nil, fmt.Errorf("动作 %s 需要提供 custom_id（上游按钮凭据）", normalized.Action)
		}
	default:
		return nil, fmt.Errorf("不支持的 action：%s（可用：%s）", normalized.Action, strings.Join(SupportedActions(), "/"))
	}
	return &normalized, nil
}

// SupportedActions 返回全部支持的动作（排序后返回，保证错误提示与单测断言稳定）。
func SupportedActions() []string {
	actions := make([]string, 0, len(promptActions)+len(customIdActions))
	for action := range promptActions {
		actions = append(actions, action)
	}
	for action := range customIdActions {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	return actions
}

// IsPromptAction 判断动作是否需要直接提交提示词（供控制器决定是否要求 prompt 非空）。
func IsPromptAction(action string) bool {
	return promptActions[strings.ToLower(strings.TrimSpace(action))]
}

// Client 上游客户端。
type Client struct {
	BaseURL    string
	ApiKey     string
	HTTPClient *http.Client
}

// NewClient 按当前配置构造客户端；未配置上游地址时返回 ErrUpstreamNotConfigured。
//
// 这里显式返回错误而不是让请求打到空地址：静默失败会让用户看到的错误
// 变成「request failed」之类的底层信息，根本猜不到是没配上游。
func NewClient() (*Client, error) {
	baseURL, apiKey := GetUpstreamConfig()
	if baseURL == "" {
		return nil, ErrUpstreamNotConfigured
	}
	return NewClientWithBaseURL(baseURL, apiKey), nil
}

// NewClientWithBaseURL 用指定地址构造客户端（单测注入假上游时使用）。
func NewClientWithBaseURL(baseURL string, apiKey string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		ApiKey:     apiKey,
		HTTPClient: sharedHTTPClient(),
	}
}

// sharedHTTPClient 复用项目统一初始化的 HTTP 客户端。
//
// 自己 new 一个 http.Client 会让 RELAY_PROXY/RELAY_TIMEOUT 等全局中继配置对它失效，
// 也会绕开连接池复用；HTTPClient 为 nil（单测或尚未 Init）时回退 DefaultClient 以免空指针。
func sharedHTTPClient() *http.Client {
	if client.HTTPClient != nil {
		return client.HTTPClient
	}
	return http.DefaultClient
}

// submitResponse 上游提交接口的响应信封。
// midjourney-proxy 约定：code=1 表示受理成功，result 为任务号；
// 但 code=21（任务已存在）同样会返回可用的 result，因此判定以 result 为准。
type submitResponse struct {
	Code        int    `json:"code"`
	Description string `json:"description"`
	Result      string `json:"result"`
}

// Submit 提交任务，返回上游任务号。
func (c *Client) Submit(ctx context.Context, req SubmitRequest) (string, error) {
	normalized, err := req.Normalize()
	if err != nil {
		return "", err
	}

	path := "/mj/submit/action"
	payload := map[string]any{}
	if IsPromptAction(normalized.Action) {
		path = "/mj/submit/" + normalized.Action
		payload["prompt"] = normalized.Prompt
	} else {
		payload["customId"] = normalized.CustomId
		if normalized.TaskId != "" {
			payload["taskId"] = normalized.TaskId
		}
	}

	var resp submitResponse
	if err := c.doJSON(ctx, http.MethodPost, path, payload, &resp); err != nil {
		return "", err
	}
	// result 为空才算失败，并且要把上游的 description 透出来（通常就是「无可用账号」这类中文原因）
	if strings.TrimSpace(resp.Result) == "" {
		description := strings.TrimSpace(resp.Description)
		if description == "" {
			description = "上游未返回任务号"
		}
		return "", fmt.Errorf("上游受理失败（code=%d）：%s", resp.Code, description)
	}
	return strings.TrimSpace(resp.Result), nil
}

// TaskInfo 上游任务信息（本站关心的字段）。
type TaskInfo struct {
	Id         string
	Action     string
	Status     string // 上游原始状态，保留原样便于排障
	StatusCode int    // 映射后的本站状态（model.MidjourneyStatus*）
	Progress   int
	ImageUrl   string
	FailReason string
}

// upstreamProgress 兼容 progress 的两种上游形态："50%"（midjourney-proxy）与 50（部分网关）。
type upstreamProgress struct{ value int }

func (p *upstreamProgress) UnmarshalJSON(data []byte) error {
	raw := strings.Trim(strings.TrimSpace(string(data)), `"`)
	raw = strings.TrimSuffix(strings.TrimSpace(raw), "%")
	if raw == "" || raw == "null" {
		return nil
	}
	// 解析不出来按 0 处理：进度字段畸形不应该让整个任务查询失败
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	p.value = value
	return nil
}

// upstreamTask 上游任务对象。
//
// /mj/task/{id}/fetch 正常时直接返回任务对象本身，但部分网关会把错误包成
// {code, description} 并用 HTTP 200 返回，所以同一个结构体兼容两种形态。
type upstreamTask struct {
	Id          string           `json:"id"`
	Action      string           `json:"action"`
	Status      string           `json:"status"`
	Progress    upstreamProgress `json:"progress"`
	ImageUrl    string           `json:"imageUrl"`
	Uri         string           `json:"uri"` // 部分实现沿用 Discord 原生字段名
	FailReason  string           `json:"failReason"`
	Description string           `json:"description"`
	Code        int              `json:"code"`
}

// Fetch 查询上游任务。
func (c *Client) Fetch(ctx context.Context, taskId string) (*TaskInfo, error) {
	taskId = strings.TrimSpace(taskId)
	if taskId == "" {
		return nil, errors.New("taskId 为空")
	}

	var task upstreamTask
	// 路径由 midjourney-proxy 的接口约定决定（/mj/task/{id}/fetch）
	if err := c.doJSON(ctx, http.MethodGet, "/mj/task/"+url.PathEscape(taskId)+"/fetch", nil, &task); err != nil {
		return nil, err
	}
	if strings.TrimSpace(task.Id) == "" && strings.TrimSpace(task.Status) == "" {
		description := strings.TrimSpace(task.Description)
		if description == "" {
			description = "上游返回的任务数据为空"
		}
		return nil, errors.New(description)
	}

	info := &TaskInfo{
		Id:         strings.TrimSpace(task.Id),
		Action:     strings.TrimSpace(task.Action),
		Status:     strings.ToUpper(strings.TrimSpace(task.Status)),
		Progress:   task.Progress.value,
		ImageUrl:   strings.TrimSpace(task.ImageUrl),
		FailReason: strings.TrimSpace(task.FailReason),
	}
	if info.Id == "" {
		info.Id = taskId
	}
	if info.ImageUrl == "" {
		info.ImageUrl = strings.TrimSpace(task.Uri)
	}
	info.StatusCode = MapUpstreamStatus(info.Status)
	return info, nil
}

// MapUpstreamStatus 把上游状态映射为本站状态。
//
// 未知状态一律按「已提交」而不是「失败」：上游升级新增状态时，
// 把正在生成的任务误判为失败会直接引发投诉与退款纠纷，而保守停在「已提交」
// 最多只是下次轮询再判一次。
func MapUpstreamStatus(status string) int {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SUCCESS":
		return model.MidjourneyStatusSuccess
	case "FAILURE", "CANCEL", "CANCELED", "CANCELLED", "FAILED":
		return model.MidjourneyStatusFailure
	case "IN_PROGRESS", "PROGRESS", "RUNNING":
		return model.MidjourneyStatusInProgress
	default:
		// NOT_START / SUBMITTED / MODAL / "" 等
		return model.MidjourneyStatusSubmitted
	}
}

// doJSON 执行一次 JSON 请求；out 为 nil 时只校验状态码。
func (c *Client) doJSON(ctx context.Context, method string, path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("序列化上游请求失败：%w", err)
		}
		body = bytes.NewReader(encoded)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return fmt.Errorf("构造上游请求失败：%w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if c.ApiKey != "" {
		// mj-api-secret 是 midjourney-proxy 约定的鉴权头
		httpReq.Header.Set("mj-api-secret", c.ApiKey)
		// 同时带标准 Bearer：有些部署在外层网关做了统一鉴权，只认 Authorization
		httpReq.Header.Set("Authorization", "Bearer "+c.ApiKey)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("请求上游失败：%w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamBodyBytes))
	if err != nil {
		return fmt.Errorf("读取上游响应失败：%w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("上游返回 HTTP %d：%s", resp.StatusCode, errorDetail(raw))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("解析上游响应失败：%w", err)
	}
	return nil
}

// errorDetail 从错误响应体里尽量提取可读原因。
// 上游正常时也可能返回 HTML 错误页，因此截断长度，避免把整页 HTML 塞进用户提示。
func errorDetail(raw []byte) string {
	var envelope struct {
		Description string `json:"description"`
		Message     string `json:"message"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil {
		for _, candidate := range []string{envelope.Description, envelope.Message, envelope.Error} {
			if detail := strings.TrimSpace(candidate); detail != "" {
				return truncate(detail, 200)
			}
		}
	}
	detail := strings.TrimSpace(string(raw))
	if detail == "" {
		detail = "上游未返回错误说明"
	}
	return truncate(detail, 200)
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	// 按字节截断可能切坏 UTF-8，这里按 rune 截断保证中文不乱码
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "..."
}
