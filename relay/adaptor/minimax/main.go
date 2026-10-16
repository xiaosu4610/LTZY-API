package minimax

import (
	"fmt"
	"github.com/xiaosu4610/LTZY-API/relay/meta"
	"github.com/xiaosu4610/LTZY-API/relay/relaymode"
)

func GetRequestURL(meta *meta.Meta) (string, error) {
	if meta.Mode == relaymode.ChatCompletions {
		return fmt.Sprintf("%s/v1/text/chatcompletion_v2", meta.BaseURL), nil
	}
	return "", fmt.Errorf("unsupported relay mode %d for minimax", meta.Mode)
}
