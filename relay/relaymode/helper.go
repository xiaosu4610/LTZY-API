package relaymode

import "strings"

func GetByPath(path string) int {
	relayMode := Unknown
	if strings.HasPrefix(path, "/v1/chat/completions") {
		relayMode = ChatCompletions
	} else if strings.HasPrefix(path, "/v1/completions") {
		relayMode = Completions
	} else if strings.HasPrefix(path, "/v1/embeddings") {
		relayMode = Embeddings
	} else if strings.HasSuffix(path, "embeddings") {
		relayMode = Embeddings
	} else if strings.HasPrefix(path, "/v1/moderations") {
		relayMode = Moderations
	} else if strings.HasPrefix(path, "/v1/images/generations") {
		relayMode = ImagesGenerations
	} else if strings.HasPrefix(path, "/v1/edits") {
		relayMode = Edits
	} else if strings.HasPrefix(path, "/v1/audio/speech") {
		relayMode = AudioSpeech
	} else if strings.HasPrefix(path, "/v1/audio/transcriptions") {
		relayMode = AudioTranscription
	} else if strings.HasPrefix(path, "/v1/audio/translations") {
		relayMode = AudioTranslation
	} else if strings.HasPrefix(path, "/v1/rerank") {
		relayMode = Rerank
	} else if strings.HasPrefix(path, "/v1/responses") {
		relayMode = Responses
	} else if strings.HasPrefix(path, "/v1/videos") {
		relayMode = Videos
	} else if strings.HasPrefix(path, "/v1/files") {
		relayMode = Files
	} else if strings.HasPrefix(path, "/v1/fine_tuning") {
		relayMode = FineTuning
	} else if strings.HasPrefix(path, "/v1/assistants") {
		relayMode = Assistants
	} else if strings.HasPrefix(path, "/v1/threads") {
		relayMode = Threads
	} else if strings.HasPrefix(path, "/v1/models/") {
		// 只认带子路径的形态（DELETE /v1/models/{model}，删除微调模型）；
		// /v1/models 本身是本地模型列表接口，不进中继，保持 Unknown。
		relayMode = Models
	} else if strings.HasPrefix(path, "/v1/oneapi/proxy") {
		relayMode = Proxy
	}
	return relayMode
}

// IsPassthrough 判断某个中继模式是否属于 A6 的「原样透传」家族
// （files / fine_tuning / assistants / threads / 删除微调模型）。
//
// 这些端点的共同点：请求体与响应体都是上游自定义对象，网关不需要理解其语义，
// 只需要把请求转出去、把响应（含错误）原样带回来。它们的差异只是在
// 渠道选择与计费口径上，所以用一个判定函数集中表达，避免各处散落 switch。
func IsPassthrough(mode int) bool {
	switch mode {
	case Files, FineTuning, Assistants, Threads, Models:
		return true
	}
	return false
}
