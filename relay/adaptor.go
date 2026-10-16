package relay

import (
	"github.com/xiaosu4610/LTZY-API/relay/adaptor"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/aiproxy"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/ali"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/anthropic"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/aws"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/baidu"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/cloudflare"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/cohere"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/coze"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/deepl"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/gemini"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/ollama"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/openai"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/palm"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/proxy"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/replicate"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/tencent"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/vertexai"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/xunfei"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/zhipu"
	"github.com/xiaosu4610/LTZY-API/relay/apitype"
)

func GetAdaptor(apiType int) adaptor.Adaptor {
	switch apiType {
	case apitype.AIProxyLibrary:
		return &aiproxy.Adaptor{}
	case apitype.Ali:
		return &ali.Adaptor{}
	case apitype.Anthropic:
		return &anthropic.Adaptor{}
	case apitype.AwsClaude:
		return &aws.Adaptor{}
	case apitype.Baidu:
		return &baidu.Adaptor{}
	case apitype.Gemini:
		return &gemini.Adaptor{}
	case apitype.OpenAI:
		return &openai.Adaptor{}
	case apitype.PaLM:
		return &palm.Adaptor{}
	case apitype.Tencent:
		return &tencent.Adaptor{}
	case apitype.Xunfei:
		return &xunfei.Adaptor{}
	case apitype.Zhipu:
		return &zhipu.Adaptor{}
	case apitype.Ollama:
		return &ollama.Adaptor{}
	case apitype.Coze:
		return &coze.Adaptor{}
	case apitype.Cohere:
		return &cohere.Adaptor{}
	case apitype.Cloudflare:
		return &cloudflare.Adaptor{}
	case apitype.DeepL:
		return &deepl.Adaptor{}
	case apitype.VertexAI:
		return &vertexai.Adaptor{}
	case apitype.Proxy:
		return &proxy.Adaptor{}
	case apitype.Replicate:
		return &replicate.Adaptor{}
	}
	return nil
}
