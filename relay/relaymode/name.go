package relaymode

import "strings"

// 中继模式的稳定字符串名（LTZY-API 新增，C3）。
//
// 用途：令牌 scope 配置、后台展示与 API 返回。
// ⚠️ 这些字符串会写进数据库并被管理员配置引用，**一旦发布不要更名**
// （更名会导致已有令牌的 scope 配置失效）。
var modeNames = map[int]string{
	ChatCompletions:    "chat",
	Completions:        "completions",
	Embeddings:         "embeddings",
	Moderations:        "moderations",
	ImagesGenerations:  "images",
	Edits:              "edits",
	AudioSpeech:        "audio_speech",
	AudioTranscription: "audio_transcription",
	AudioTranslation:   "audio_translation",
	Rerank:             "rerank",
	Responses:          "responses",
	Videos:             "videos",
	Proxy:              "proxy",
	// A6 透传端点家族（LTZY-API 新增）。令牌 scope 配置里用这些名字放行/拦截。
	Files:      "files",
	FineTuning: "fine_tuning",
	Assistants: "assistants",
	Threads:    "threads",
	Models:     "models",
	// A5 声音复刻 / 音色管理
	AudioVoice: "audio_voice",
}

// ModeName2Mode 反查表，用于校验管理员填写的中继模式名是否合法。
var modeName2Mode = func() map[string]int {
	out := make(map[string]int, len(modeNames))
	for mode, name := range modeNames {
		out[name] = mode
	}
	return out
}()

// Name 返回中继模式的稳定字符串名；未知模式返回空串。
func Name(mode int) string {
	return modeNames[mode]
}

// Names 返回全部合法的中继模式名（供前端下拉与配置校验使用）。
func Names() []string {
	out := make([]string, 0, len(modeNames))
	for _, name := range modeNames {
		out = append(out, name)
	}
	return out
}

// NamesHelp 返回逗号分隔的合法取值，用于错误提示与后台展示。
func NamesHelp() string {
	return strings.Join(Names(), ",")
}

// IsValidName 校验是否为合法的中继模式名。
func IsValidName(name string) bool {
	_, ok := modeName2Mode[name]
	return ok
}

// ParseAllowedModes 解析令牌 scope 配置（逗号分隔的模式名）。
// 返回 nil 表示「不限制任何模式」（即全部放行）——
// 这与"配置了但解析为空"等价，都是不限制，避免误配把令牌锁死。
func ParseAllowedModes(raw string) map[int]bool {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	allowed := map[int]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if mode, ok := modeName2Mode[part]; ok {
			allowed[mode] = true
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	return allowed
}
