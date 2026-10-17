package channeltype

const (
	Unknown = iota
	OpenAI
	API2D
	Azure
	CloseAI
	OpenAISB
	OpenAIMax
	OhMyGPT
	Custom
	Ails
	AIProxy
	PaLM
	API2GPT
	AIGC2D
	Anthropic
	Baidu
	Zhipu
	Ali
	Xunfei
	AI360
	OpenRouter
	AIProxyLibrary
	FastGPT
	Tencent
	Gemini
	Moonshot
	Baichuan
	Minimax
	Mistral
	Groq
	Ollama
	LingYiWanWu
	StepFun
	AwsClaude
	Coze
	Cohere
	DeepSeek
	Cloudflare
	DeepL
	TogetherAI
	Doubao
	Novita
	VertextAI
	Proxy
	SiliconFlow
	XAI
	Replicate
	BaiduV2
	XunfeiV2
	AliBailian
	OpenAICompatible
	GeminiOpenAICompatible
	Dummy
	// ── 以下为 LTZY-API 二开新增（A7 新渠道）──────────────────────────────
	// 说明：这些厂商都提供 **OpenAI 兼容** 接口，因此不需要写适配器——
	// channeltype.ToAPIType 默认返回 apitype.OpenAI，会自动复用 OpenAI 适配器。
	// 单独列出渠道类型的价值：① 每个渠道有独立默认 BaseURL，管理员不用手填；
	// ② 前端下拉能显示厂商名，便于运维识别。这是「零适配成本、可维护性收益」。

	// Perplexity：带联网检索的问答模型（sonar 系列），OpenAI 兼容。
	Perplexity
	// Jina：向量嵌入与重排（jina-embeddings / jina-reranker），OpenAI 兼容。
	Jina
	// VoyageAI：高质量嵌入模型（voyage-3 等），Anthropic 官方推荐的嵌入方案。
	VoyageAI
	// Fireworks：托管大量开源模型的推理平台（Llama/Qwen/DeepSeek 等），OpenAI 兼容。
	Fireworks
	// DeepInfra：低成本开源模型推理平台，OpenAI 兼容。
	DeepInfra
	// Hyperbolic：开源模型推理与 GPU 市场，OpenAI 兼容。
	Hyperbolic
	// Cerebras：以极低延迟著称的推理平台（Llama 等），OpenAI 兼容。
	Cerebras
	// SambaNova：高速推理平台，OpenAI 兼容。
	SambaNova
)
