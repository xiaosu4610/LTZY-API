package relaymode

const (
	Unknown = iota
	ChatCompletions
	Completions
	Embeddings
	Moderations
	ImagesGenerations
	Edits
	AudioSpeech
	AudioTranscription
	AudioTranslation
	// Rerank is for semantic re-ranking of documents (Cohere/Jina/BGE style)
	Rerank
	// Responses is for OpenAI Responses API (/v1/responses)
	Responses
	// Videos is for video generation (/v1/videos)
	Videos
	// Proxy is a special relay mode for proxying requests to custom upstream
	Proxy
	// Files is for OpenAI Files API (/v1/files...) —— 列表/上传/查询/删除/下载内容。
	// 该家族不产生模型 token 用量，按透传处理（A6）。
	Files
	// FineTuning is for /v1/fine_tuning/jobs...（创建/查询/取消/事件），纯透传。
	FineTuning
	// Assistants is for /v1/assistants...（含 assistant files），纯透传。
	Assistants
	// Threads is for /v1/threads...（含 messages 与 runs）；其中创建 run 会真实产生
	// 模型用量，是这一批端点里唯一「可能计费」的形态（A6 计费口径见 relay/controller/passthrough.go）。
	Threads
	// Models is for DELETE /v1/models/{model}（删除微调模型）。
	// 注意：它**不包含** /v1/models 列表接口 —— 列表由本机管理接口提供，不经过中继。
	Models
)
