package model

// VideoRequest 视频生成请求（LTZY-API 新增）。
//
// 兼容主流视频生成 API 的通用字段：大多数平台（如可灵、海螺、豆包、Sora 兼容层）
// 都接受 prompt + model 两个核心参数，其余为可选扩展参数。
// 未识别的字段通过 Extra 透传，保证不同上游的私有参数不被丢弃。
type VideoRequest struct {
	Model          string  `json:"model"`
	Prompt         string  `json:"prompt"`
	NegativePrompt string  `json:"negative_prompt,omitempty"`
	Image          string  `json:"image,omitempty"`   // 图生视频：首帧图片 URL 或 base64
	Duration       int     `json:"duration,omitempty"` // 视频时长（秒）
	Size           string  `json:"size,omitempty"`     // 分辨率，如 1280x720
	Fps            int     `json:"fps,omitempty"`
	Seed           int64   `json:"seed,omitempty"`
	N              int     `json:"n,omitempty"`             // 生成数量，默认 1
	ResponseFormat string  `json:"response_format,omitempty"` // url | b64_json
	User           string  `json:"user,omitempty"`
	// Extra 保存未识别的扩展字段，原样透传给上游
	Extra map[string]any `json:"-"`
}

// VideoTask 上游返回的异步任务信息（通用形态）
type VideoTask struct {
	Id     string `json:"id"`
	Status string `json:"status"`
	Url    string `json:"url,omitempty"`
}

// BuildForwardBody 构造转发给上游的请求体：核心字段 + 扩展字段。
func (r *VideoRequest) BuildForwardBody() map[string]any {
	body := map[string]any{
		"model":  r.Model,
		"prompt": r.Prompt,
	}
	if r.NegativePrompt != "" {
		body["negative_prompt"] = r.NegativePrompt
	}
	if r.Image != "" {
		body["image"] = r.Image
	}
	if r.Duration > 0 {
		body["duration"] = r.Duration
	}
	if r.Size != "" {
		body["size"] = r.Size
	}
	if r.Fps > 0 {
		body["fps"] = r.Fps
	}
	if r.Seed != 0 {
		body["seed"] = r.Seed
	}
	if r.N > 0 {
		body["n"] = r.N
	}
	if r.ResponseFormat != "" {
		body["response_format"] = r.ResponseFormat
	}
	for key, value := range r.Extra {
		body[key] = value
	}
	return body
}
