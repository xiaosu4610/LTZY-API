package channeltype

var ChannelBaseURLs = []string{
	"",                              // 0
	"https://api.openai.com",        // 1
	"https://oa.api2d.net",          // 2
	"",                              // 3
	"https://api.closeai-proxy.xyz", // 4
	"https://api.openai-sb.com",     // 5
	"https://api.openaimax.com",     // 6
	"https://api.ohmygpt.com",       // 7
	"",                              // 8
	"https://api.caipacity.com",     // 9
	"https://api.aiproxy.io",        // 10
	"https://generativelanguage.googleapis.com", // 11
	"https://api.api2gpt.com",                   // 12
	"https://api.aigc2d.com",                    // 13
	"https://api.anthropic.com",                 // 14
	"https://aip.baidubce.com",                  // 15
	"https://open.bigmodel.cn",                  // 16
	"https://dashscope.aliyuncs.com",            // 17
	"",                                          // 18
	"https://ai.360.cn",                         // 19
	"https://openrouter.ai/api",                 // 20
	"https://api.aiproxy.io",                    // 21
	"https://fastgpt.run/api/openapi",           // 22
	"https://hunyuan.tencentcloudapi.com",       // 23
	"https://generativelanguage.googleapis.com", // 24
	"https://api.moonshot.cn",                   // 25
	"https://api.baichuan-ai.com",               // 26
	"https://api.minimax.chat",                  // 27
	"https://api.mistral.ai",                    // 28
	"https://api.groq.com/openai",               // 29
	"http://localhost:11434",                    // 30
	"https://api.lingyiwanwu.com",               // 31
	"https://api.stepfun.com",                   // 32
	"",                                          // 33
	"https://api.coze.com",                      // 34
	"https://api.cohere.ai",                     // 35
	"https://api.deepseek.com",                  // 36
	"https://api.cloudflare.com",                // 37
	"https://api-free.deepl.com",                // 38
	"https://api.together.xyz",                  // 39
	"https://ark.cn-beijing.volces.com",         // 40
	"https://api.novita.ai/v3/openai",           // 41
	"",                                          // 42
	"",                                          // 43
	"https://api.siliconflow.cn",                // 44
	"https://api.x.ai",                          // 45
	"https://api.replicate.com/v1/models/",      // 46
	"https://qianfan.baidubce.com",              // 47
	"https://spark-api-open.xf-yun.com",         // 48
	"https://dashscope.aliyuncs.com",            // 49
	"",                                          // 50

	"https://generativelanguage.googleapis.com/v1beta/openai/", // 51 GeminiOpenAICompatible
	"", // 52 Dummy（测试用，无上游地址）

	// ── LTZY-API 二开新增（A7）：索引必须与 define.go 的常量顺序严格一致，
	// 否则 init() 会 panic（长度校验），这是有意的防误配保护。
	"https://api.perplexity.ai",           // 53 Perplexity
	"https://api.jina.ai",                 // 54 Jina
	"https://api.voyageai.com",            // 55 VoyageAI
	"https://api.fireworks.ai/inference",  // 56 Fireworks
	"https://api.deepinfra.com/v1/openai", // 57 DeepInfra
	"https://api.hyperbolic.xyz",          // 58 Hyperbolic
	"https://api.cerebras.ai",             // 59 Cerebras
	"https://api.sambanova.ai",            // 60 SambaNova
}

// lastChannelType 必须始终指向 define.go 中**最后一个**渠道类型常量。
//
// 原实现校验的是 Dummy（当时它恰好是最后一个），但 A7 新增渠道后
// Dummy 不再是末尾，这条校验就失效了 —— 现在改为显式维护"最后一个"，
// 并保留同等强度的保护：新增渠道类型却忘了加 BaseURL 会立刻 panic，
// 而不是在运行期取到空 URL 再变成"莫名其妙的连接失败"。
const lastChannelType = SambaNova

func init() {
	if len(ChannelBaseURLs) != lastChannelType+1 {
		panic("channel base urls length not match")
	}
}
