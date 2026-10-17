package message

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/logger"
)

// 通知渠道使用的 Option 键名。集中定义是为了让「配置项注册」与「读取处」不易拼写漂移。
const (
	optionWebhookURL         = "WebhookURL"
	optionLarkWebhookURL     = "LarkWebhookURL"
	optionDingTalkWebhookURL = "DingTalkWebhookURL"
	optionDingTalkSecret     = "DingTalkSecret"
	optionSlackWebhookURL    = "SlackWebhookURL"
)

// webhookHTTPClient 设置超时：这些第三方通知服务不可用时不能把调用方
// （渠道自动禁用、启用等主流程）一直挂住，因此不能像 MessagePusher 那样用默认客户端。
var webhookHTTPClient = &http.Client{Timeout: 10 * time.Second}

// readOption 在读写锁保护下读取 OptionMap。
// OptionMap 会被后台任务热更新（model.loadOptionsFromDatabase），无锁访问会产生数据竞争。
func readOption(key string) string {
	config.OptionMapRWMutex.RLock()
	defer config.OptionMapRWMutex.RUnlock()
	return config.OptionMap[key]
}

// webhookRequest 通用 Webhook 的请求体。字段名沿用 MessagePusher 的约定，
// 使接收方可复用同一套解析逻辑。
type webhookRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

// larkRequest 飞书自定义机器人消息体（最简文本消息）
type larkRequest struct {
	MsgType string      `json:"msg_type"`
	Content larkContent `json:"content"`
}

type larkContent struct {
	Text string `json:"text"`
}

// dingTalkRequest 钉钉自定义机器人消息体（最简文本消息）
type dingTalkRequest struct {
	MsgType string       `json:"msgtype"`
	Text    dingTalkText `json:"text"`
}

type dingTalkText struct {
	Content string `json:"content"`
}

// slackRequest Slack Incoming Webhook 消息体
type slackRequest struct {
	Text string `json:"text"`
}

// botResponse 同时覆盖飞书与钉钉的业务返回结构：
// 飞书用 code/msg（旧版为 StatusCode/StatusMessage），钉钉用 errcode/errmsg。
// 这两类机器人在加签错误、被限流等情况下仍返回 HTTP 200，只看状态码会把失败当成功。
type botResponse struct {
	Code          int    `json:"code"`
	Msg           string `json:"msg"`
	StatusCode    int    `json:"StatusCode"`
	StatusMessage string `json:"StatusMessage"`
	ErrCode       int    `json:"errcode"`
	ErrMsg        string `json:"errmsg"`
}

// buildText 把标题与正文拼成纯文本渠道可读的消息体。
// 飞书/钉钉/Slack 的文本消息没有独立标题字段，若丢掉标题，
// 运维收到告警时无法一眼区分是哪种事件。
func buildText(title string, content string) string {
	if title == "" {
		return content
	}
	return title + "\n" + content
}

// postJSON 统一发送 JSON 请求，返回（截断后的）响应体供调用方解析业务码。
// 响应体做长度限制既是避免异常的超大响应体占用内存，也足够用于错误诊断。
func postJSON(target string, body []byte, channelName string) ([]byte, error) {
	resp, err := webhookHTTPClient.Post(target, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s notification failed with status %d: %s", channelName, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// checkBotResponse 解析飞书/钉钉的业务码。响应不是 JSON 时不做判断，交给 HTTP 状态码兜底。
func checkBotResponse(channelName string, data []byte) error {
	var res botResponse
	if err := json.Unmarshal(data, &res); err != nil {
		return nil
	}
	if res.Code != 0 {
		return fmt.Errorf("%s notification rejected: code=%d msg=%s", channelName, res.Code, res.Msg)
	}
	if res.StatusCode != 0 {
		return fmt.Errorf("%s notification rejected: code=%d msg=%s", channelName, res.StatusCode, res.StatusMessage)
	}
	if res.ErrCode != 0 {
		return fmt.Errorf("%s notification rejected: errcode=%d errmsg=%s", channelName, res.ErrCode, res.ErrMsg)
	}
	return nil
}

// SendWebhook 通用 Webhook 通知；未配置 URL 视为「未启用」，直接跳过。
func SendWebhook(title string, description string, content string) error {
	webhookURL := readOption(optionWebhookURL)
	if webhookURL == "" {
		return nil
	}
	data, err := json.Marshal(webhookRequest{
		Title:       title,
		Description: description,
		Content:     content,
	})
	if err != nil {
		return err
	}
	_, err = postJSON(webhookURL, data, "webhook")
	return err
}

// SendLark 飞书(Lark)自定义机器人通知；未配置 URL 视为「未启用」，直接跳过。
func SendLark(title string, description string, content string) error {
	larkURL := readOption(optionLarkWebhookURL)
	if larkURL == "" {
		return nil
	}
	data, err := json.Marshal(larkRequest{
		MsgType: "text",
		Content: larkContent{Text: buildText(title, content)},
	})
	if err != nil {
		return err
	}
	respBody, err := postJSON(larkURL, data, "lark")
	if err != nil {
		return err
	}
	return checkBotResponse("lark", respBody)
}

// SendDingTalk 钉钉(DingTalk)自定义机器人通知；未配置 URL 视为「未启用」，直接跳过。
// 配置了 DingTalkSecret 时按钉钉要求加签。
func SendDingTalk(title string, description string, content string) error {
	dingTalkURL := readOption(optionDingTalkWebhookURL)
	if dingTalkURL == "" {
		return nil
	}
	target := dingTalkURL
	if secret := readOption(optionDingTalkSecret); secret != "" {
		// 时间戳必须与签名同一时刻生成：钉钉会校验 timestamp 与服务器时间的偏差。
		signed, err := signDingTalkURL(dingTalkURL, secret, time.Now().UnixMilli())
		if err != nil {
			return err
		}
		target = signed
	}
	data, err := json.Marshal(dingTalkRequest{
		MsgType: "text",
		Text:    dingTalkText{Content: buildText(title, content)},
	})
	if err != nil {
		return err
	}
	respBody, err := postJSON(target, data, "dingtalk")
	if err != nil {
		return err
	}
	return checkBotResponse("dingtalk", respBody)
}

// SendSlack Slack Incoming Webhook 通知；未配置 URL 视为「未启用」，直接跳过。
func SendSlack(title string, description string, content string) error {
	slackURL := readOption(optionSlackWebhookURL)
	if slackURL == "" {
		return nil
	}
	data, err := json.Marshal(slackRequest{Text: buildText(title, content)})
	if err != nil {
		return err
	}
	_, err = postJSON(slackURL, data, "slack")
	return err
}

// signDingTalkURL 为钉钉机器人 URL 追加 timestamp 与 sign 参数。
// 基础 URL 可能已经带了 access_token，因此用 url.Parse 而不是字符串拼接，避免覆盖或拼错查询串。
func signDingTalkURL(rawURL string, secret string, timestamp int64) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	timestampText := strconv.FormatInt(timestamp, 10)
	query := parsed.Query()
	query.Set("timestamp", timestampText)
	// 钉钉要求 sign 做 URL encode，这里交给 query.Encode() 统一完成百分号转义，
	// 因此 dingTalkSign 返回的是未编码的 Base64 原文（二次编码会导致验签失败）。
	query.Set("sign", dingTalkSign(secret, timestampText))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// dingTalkSign 计算钉钉加签值：以 secret 为密钥，对 "timestamp\nsecret" 做 HMAC-SHA256 后 Base64。
func dingTalkSign(secret string, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// notifyChannel 用统一签名描述一个渠道，便于遍历发送并让日志定位到具体渠道。
type notifyChannel struct {
	name string
	send func(title string, description string, content string) error
}

var extraNotifyChannels = []notifyChannel{
	{name: "webhook", send: SendWebhook},
	{name: "lark", send: SendLark},
	{name: "dingtalk", send: SendDingTalk},
	{name: "slack", send: SendSlack},
}

// NotifyExtraChannels 向所有已配置 URL 的新增渠道推送同一条通知。
// 这里刻意不返回错误、也不使用 panic：通知只是渠道禁用/启用等主流程的旁路，
// 单个渠道的配置错误或网络故障既不能让主流程失败，也不能阻断后面的渠道。
func NotifyExtraChannels(title string, description string, content string) {
	for _, channel := range extraNotifyChannels {
		if err := channel.send(title, description, content); err != nil {
			logger.SysError(fmt.Sprintf("failed to send %s notification: %s", channel.name, err.Error()))
		}
	}
}
