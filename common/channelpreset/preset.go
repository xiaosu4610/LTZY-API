package channelpreset

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/xiaosu4610/LTZY-API/common/config"
)

// 渠道预设（LTZY-API 新增）。
//
// 目的：新建渠道时最费时的是「上游地址 + 常用模型名」这两项，而且容易抄错。
// 预设把主流供应商的 OpenAI 兼容地址与常见模型列出来，一键带出。
//
// 两个刻意的设计取舍：
//  1. 预设**不含渠道类型**（Type）。同一家的渠道在本项目里可能对应不同适配器，
//     强行预设 Type 反而容易建出一条不能用的渠道。前端只带出 base_url 与模型，
//     类型仍由管理员选择。
//  2. 预设可以整体被 Option `ChannelPresets` 覆盖（JSON 数组），
//     这样模型下线/新增时管理员改配置即可，不必等发版。
//
// 注意：上游模型 ID 变动很频繁，这里的模型列表只是「常见起点」，
// 管理员应结合上游 /v1/models 的实际返回调整。
type Preset struct {
	Id       string   `json:"id"`
	Name     string   `json:"name"`
	BaseURL  string   `json:"base_url"`
	AuthType string   `json:"auth_type"`
	Models   []string `json:"models"`
	DocURL   string   `json:"doc_url"`
	Note     string   `json:"note"`
}

// builtinPresets 内置预设。base_url 一栏以「OpenAI 兼容端点」为准，
// 并保留上游官方的路径特征（例如智谱是 /api/paas/v4、Perplexity 不带 /v1）。
func builtinPresets() []Preset {
	return []Preset{
		{
			Id: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1",
			AuthType: "bearer", DocURL: "https://platform.openai.com/docs",
			Models: []string{"gpt-4o", "gpt-4o-mini", "gpt-4-turbo", "gpt-3.5-turbo", "o1", "o1-mini", "o3-mini", "text-embedding-3-small", "dall-e-3", "whisper-1"},
		},
		{
			Id: "anthropic", Name: "Anthropic (Claude)", BaseURL: "https://api.anthropic.com/v1",
			AuthType: "x-api-key", DocURL: "https://docs.anthropic.com",
			Models: []string{"claude-3-5-sonnet-20241022", "claude-3-5-haiku-20241022", "claude-3-opus-20240229", "claude-3-haiku-20240307"},
			Note: "原生 Messages 协议用 x-api-key；若本网关按 OpenAI 协议转发，请确认所选渠道类型支持。",
		},
		{
			Id: "gemini", Name: "Google Gemini", BaseURL: "https://generativelanguage.googleapis.com",
			AuthType: "bearer", DocURL: "https://ai.google.dev/gemini-api/docs/openai",
			Models: []string{"gemini-1.5-pro", "gemini-1.5-flash", "gemini-1.5-flash-8b", "gemini-2.0-flash"},
			Note: "OpenAI 兼容端点为 /v1beta/openai/；原生端点用 x-goog-api-key。",
		},
		{
			Id: "xai", Name: "xAI (Grok)", BaseURL: "https://api.x.ai/v1",
			AuthType: "bearer", DocURL: "https://docs.x.ai/docs/api-reference",
			Models: []string{"grok-beta", "grok-2-1212", "grok-2-vision-1212"},
		},
		{
			Id: "deepseek", Name: "深度求索 DeepSeek", BaseURL: "https://api.deepseek.com",
			AuthType: "bearer", DocURL: "https://api-docs.deepseek.com",
			Models: []string{"deepseek-chat", "deepseek-reasoner"},
			Note: "官方同时支持 https://api.deepseek.com 与 https://api.deepseek.com/v1 两种写法。",
		},
		{
			Id: "moonshot", Name: "月之暗面 Kimi", BaseURL: "https://api.moonshot.cn/v1",
			AuthType: "bearer", DocURL: "https://platform.moonshot.cn/docs",
			Models: []string{"moonshot-v1-8k", "moonshot-v1-32k", "moonshot-v1-128k"},
			Note: "国内 api.moonshot.cn 与国际 api.moonshot.ai 的 Key 不通用。",
		},
		{
			Id: "zhipu", Name: "智谱 AI (GLM)", BaseURL: "https://open.bigmodel.cn/api/paas/v4",
			AuthType: "bearer", DocURL: "https://docs.bigmodel.cn",
			Models: []string{"glm-4-plus", "glm-4-air", "glm-4-airx", "glm-4-flash", "glm-4v-plus", "glm-4-long"},
			Note: "路径是 /api/paas/v4，不要写成 /v1。",
		},
		{
			Id: "dashscope", Name: "阿里云百炼（通义千问）", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
			AuthType: "bearer", DocURL: "https://help.aliyun.com/zh/model-studio/",
			Models: []string{"qwen-max", "qwen-plus", "qwen-turbo", "qwen-long", "qwen-vl-max", "qwen-vl-plus", "qwen2.5-72b-instruct", "text-embedding-v3"},
			Note: "必须带 /compatible-mode 前缀；新加坡节点是 dashscope-intl.aliyuncs.com。",
		},
		{
			Id: "volcengine", Name: "火山方舟（豆包）", BaseURL: "https://ark.cn-beijing.volces.com/api/v3",
			AuthType: "bearer", DocURL: "https://www.volcengine.com/docs/82379",
			Models: []string{"doubao-pro-32k", "doubao-pro-128k", "doubao-lite-32k", "doubao-1-5-pro-32k-250115"},
			Note: "路径是 /api/v3；部分模型需先在方舟控制台创建「接入点」并填接入点 ID。",
		},
		{
			Id: "qianfan", Name: "百度千帆（文心）", BaseURL: "https://qianfan.baidubce.com/v2",
			AuthType: "bearer", DocURL: "https://cloud.baidu.com/doc/qianfan",
			Models: []string{"ernie-4.0-8k", "ernie-4.0-turbo-8k", "ernie-3.5-8k", "ernie-speed-8k", "ernie-lite-8k"},
			Note: "路径是 /v2。",
		},
		{
			Id: "hunyuan", Name: "腾讯混元", BaseURL: "https://api.hunyuan.cloud.tencent.com/v1",
			AuthType: "bearer", DocURL: "https://cloud.tencent.com/document/product/1729",
			Models: []string{"hunyuan-turbo", "hunyuan-pro", "hunyuan-standard", "hunyuan-lite", "hunyuan-code", "hunyuan-vision"},
		},
		{
			Id: "spark", Name: "讯飞星火", BaseURL: "https://spark-api-open.xf-yun.com/v1",
			AuthType: "bearer", DocURL: "https://www.xfyun.cn/doc/spark/",
			Models: []string{"generalv3.5", "4.0Ultra", "generalv3", "pro-128k", "lite"},
		},
		{
			Id: "stepfun", Name: "阶跃星辰 StepFun", BaseURL: "https://api.stepfun.com/v1",
			AuthType: "bearer", DocURL: "https://platform.stepfun.com/docs",
			Models: []string{"step-2-16k", "step-1-8k", "step-1-32k", "step-1-128k", "step-1-flash", "step-1v-8k"},
		},
		{
			Id: "minimax", Name: "MiniMax", BaseURL: "https://api.minimax.chat/v1",
			AuthType: "bearer", DocURL: "https://platform.minimaxi.com/document",
			Models: []string{"abab6.5s-chat", "abab6.5-chat", "abab6-chat"},
		},
		{
			Id: "yi", Name: "零一万物 01.AI", BaseURL: "https://api.lingyiwanwu.com/v1",
			AuthType: "bearer", DocURL: "https://platform.lingyiwanwu.com/docs",
			Models: []string{"yi-lightning", "yi-large", "yi-large-turbo", "yi-medium", "yi-vision"},
		},
		{
			Id: "baichuan", Name: "百川智能", BaseURL: "https://api.baichuan-ai.com/v1",
			AuthType: "bearer", DocURL: "https://platform.baichuan-ai.com/docs",
			Models: []string{"Baichuan4", "Baichuan4-Turbo", "Baichuan4-Air", "Baichuan3-Turbo", "Baichuan3-Turbo-128k"},
		},
		{
			Id: "siliconflow", Name: "硅基流动 SiliconFlow", BaseURL: "https://api.siliconflow.cn/v1",
			AuthType: "bearer", DocURL: "https://docs.siliconflow.cn",
			Models: []string{"deepseek-ai/DeepSeek-V3", "deepseek-ai/DeepSeek-R1", "Qwen/Qwen2.5-72B-Instruct", "meta-llama/Llama-3.3-70B-Instruct", "BAAI/bge-m3"},
		},
		{
			Id: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1",
			AuthType: "bearer", DocURL: "https://openrouter.ai/docs",
			Models: []string{"openai/gpt-4o", "openai/gpt-4o-mini", "anthropic/claude-3.5-sonnet", "google/gemini-pro-1.5", "deepseek/deepseek-chat", "meta-llama/llama-3.3-70b-instruct"},
			Note: "建议同时设置 HTTP-Referer 与 X-Title 头以便统计。",
		},
		{
			Id: "together", Name: "Together AI", BaseURL: "https://api.together.xyz/v1",
			AuthType: "bearer", DocURL: "https://docs.together.ai",
			Models: []string{"meta-llama/Llama-3.3-70B-Instruct-Turbo", "meta-llama/Meta-Llama-3.1-8B-Instruct-Turbo", "deepseek-ai/DeepSeek-V3", "Qwen/Qwen2.5-72B-Instruct-Turbo"},
		},
		{
			Id: "groq", Name: "Groq", BaseURL: "https://api.groq.com/openai/v1",
			AuthType: "bearer", DocURL: "https://console.groq.com/docs",
			Models: []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant", "llama3-70b-8192", "llama3-8b-8192", "mixtral-8x7b-32768", "gemma2-9b-it"},
			Note: "路径是 /openai/v1。",
		},
		{
			Id: "mistral", Name: "Mistral AI", BaseURL: "https://api.mistral.ai/v1",
			AuthType: "bearer", DocURL: "https://docs.mistral.ai",
			Models: []string{"mistral-large-latest", "mistral-medium-latest", "mistral-small-latest", "open-mistral-7b", "open-mixtral-8x7b", "codestral-latest", "mistral-embed"},
		},
		{
			Id: "cohere", Name: "Cohere", BaseURL: "https://api.cohere.ai",
			AuthType: "bearer", DocURL: "https://docs.cohere.com",
			Models: []string{"command-r-plus", "command-r", "command-r7b-12-2024", "command-light", "embed-english-v3.0"},
			Note: "OpenAI 兼容端点是 /compatibility/v1，原生端点是 /v2。",
		},
		{
			Id: "perplexity", Name: "Perplexity", BaseURL: "https://api.perplexity.ai",
			AuthType: "bearer", DocURL: "https://docs.perplexity.ai",
			Models: []string{"sonar", "sonar-pro", "sonar-reasoning"},
			Note: "⚠️ 地址后面直接拼 /chat/completions，**没有 /v1**。",
		},
		{
			Id: "nvidia", Name: "NVIDIA NIM", BaseURL: "https://integrate.api.nvidia.com/v1",
			AuthType: "bearer", DocURL: "https://build.nvidia.com",
			Models: []string{"meta/llama-3.3-70b-instruct", "meta/llama-3.1-8b-instruct", "deepseek-ai/deepseek-r1", "nvidia/llama-3.1-nemotron-70b-instruct", "mistralai/mistral-7b-instruct-v0.3"},
			Note: "免费额度渠道对 429 很敏感：本项目已把 429 视为「限流」而非「密钥失效」，不会据此禁用渠道。",
		},
		{
			Id: "cerebras", Name: "Cerebras", BaseURL: "https://api.cerebras.ai/v1",
			AuthType: "bearer", DocURL: "https://inference-docs.cerebras.ai",
			Models: []string{"llama-3.3-70b", "llama3.1-8b", "qwen-3-32b"},
		},
		{
			Id: "github-models", Name: "GitHub Models", BaseURL: "https://models.inference.ai.azure.com",
			AuthType: "bearer", DocURL: "https://docs.github.com/en/github-models",
			Models: []string{"gpt-4o", "gpt-4o-mini", "o1-mini", "DeepSeek-R1", "Llama-3.3-70B-Instruct"},
			Note: "用 GitHub PAT 作为令牌，需 models:read 权限。",
		},
		{
			Id: "302ai", Name: "302.AI", BaseURL: "https://api.302.ai/v1",
			AuthType: "bearer", DocURL: "https://302ai.apifox.cn",
			Models: []string{"gpt-4o", "gpt-4o-mini", "claude-3-5-sonnet-20241022", "deepseek-chat"},
		},
		{
			Id: "aihubmix", Name: "AiHubMix", BaseURL: "https://aihubmix.com/v1",
			AuthType: "bearer", DocURL: "https://docs.aihubmix.com",
			Models: []string{"gpt-4o", "gpt-4o-mini", "claude-3-5-sonnet-20241022", "gemini-1.5-pro", "deepseek-chat"},
		},
		{
			Id: "ollama", Name: "Ollama（本地）", BaseURL: "http://localhost:11434/v1",
			AuthType: "bearer", DocURL: "https://docs.ollama.com/api/openai-compatibility",
			Models: []string{"llama3.2", "qwen2.5", "deepseek-r1", "gemma2", "mistral"},
			Note: "本地部署，Key 可随意填一个非空值。",
		},
		{
			Id: "vllm", Name: "vLLM（自建）", BaseURL: "http://localhost:8000/v1",
			AuthType: "bearer", DocURL: "https://docs.vllm.ai",
			Models: []string{},
			Note: "模型名取决于你实际部署的权重。",
		},
		{
			Id: "lmstudio", Name: "LM Studio（本地）", BaseURL: "http://localhost:1234/v1",
			AuthType: "bearer", DocURL: "https://lmstudio.ai/docs",
			Models: []string{},
			Note: "模型名取决于本地已加载的模型。",
		},
		{
			Id: "azure-openai", Name: "Azure OpenAI", BaseURL: "https://{resource}.openai.azure.com/openai",
			AuthType: "api-key", DocURL: "https://learn.microsoft.com/azure/ai-services/openai/",
			Models: []string{},
			Note: "⚠️ 需把 {resource} 换成你的资源名，模型名要填**部署名**而非模型 ID，并需要 api-version 参数。",
		},
		{
			Id: "cloudflare", Name: "Cloudflare Workers AI", BaseURL: "https://api.cloudflare.com/client/v4/accounts/{account_id}/ai/v1",
			AuthType: "bearer", DocURL: "https://developers.cloudflare.com/workers-ai/",
			Models: []string{"@cf/meta/llama-3.1-8b-instruct", "@cf/meta/llama-3.3-70b-instruct-fp8-fast", "@cf/qwen/qwen1.5-14b-chat-awq"},
			Note: "⚠️ 需把 {account_id} 换成你的账号 ID。",
		},
		{
			Id: "openai-compatible", Name: "通用 OpenAI 兼容", BaseURL: "",
			AuthType: "bearer", DocURL: "",
			Models: []string{},
			Note: "自建/中转网关通用占位，请自行填写地址与模型。",
		},
	}
}

// All 返回生效的预设列表：优先使用 Option `ChannelPresets` 覆盖值，为空时用内置列表。
func All() []Preset {
	config.OptionMapRWMutex.RLock()
	raw := config.OptionMap["ChannelPresets"]
	config.OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) != "" {
		parsed := []Preset{}
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil && len(parsed) > 0 {
			return parsed
		}
	}
	presets := builtinPresets()
	sort.SliceStable(presets, func(i, j int) bool { return presets[i].Name < presets[j].Name })
	return presets
}
