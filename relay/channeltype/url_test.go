package channeltype

import (
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/xiaosu4610/LTZY-API/relay/apitype"
)

func TestChannelBaseURLs(t *testing.T) {
	Convey("channel base urls", t, func() {
		// 原断言是 len == Dummy，但 A7 新增渠道后 Dummy 不再是最后一个常量。
		// 改为与 init() 使用同一个基准（lastChannelType），两处口径永远一致。
		So(len(ChannelBaseURLs), ShouldEqual, lastChannelType+1)
	})
}

// TestNewChannelsHaveBaseURL A7 新增渠道必须配好默认 BaseURL 且为 OpenAI 兼容。
//
// 为什么值得单独测：
//   - BaseURL 为空会让管理员建渠道后"看起来配好了但连不上"，而报错要到中继时
//     才出现，排查成本高；
//   - 这些厂商都是靠「OpenAI 兼容 + 默认 URL」零适配接入的，
//     若哪天 ToAPIType 把它们映射成别的 apiType，这个测试会立刻失败。
func TestNewChannelsHaveBaseURL(t *testing.T) {
	newChannels := map[string]int{
		"Perplexity": Perplexity,
		"Jina":       Jina,
		"VoyageAI":   VoyageAI,
		"Fireworks":  Fireworks,
		"DeepInfra":  DeepInfra,
		"Hyperbolic": Hyperbolic,
		"Cerebras":   Cerebras,
		"SambaNova":  SambaNova,
	}
	for name, channelType := range newChannels {
		baseURL := ChannelBaseURLs[channelType]
		if strings.TrimSpace(baseURL) == "" {
			t.Fatalf("%s 必须配置默认 BaseURL", name)
		}
		if !strings.HasPrefix(baseURL, "https://") {
			t.Fatalf("%s 的 BaseURL 必须是 https（避免明文传输密钥）：%s", name, baseURL)
		}
		if apiType := ToAPIType(channelType); apiType != apitype.OpenAI {
			t.Fatalf("%s 应为 OpenAI 兼容渠道（复用 OpenAI 适配器），实际 apiType=%d", name, apiType)
		}
	}
}
