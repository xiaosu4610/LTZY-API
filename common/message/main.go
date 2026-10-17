package message

import (
	"fmt"
	"github.com/xiaosu4610/LTZY-API/common/config"
)

const (
	ByAll           = "all"
	ByEmail         = "email"
	ByMessagePusher = "message_pusher"
)

func Notify(by string, title string, description string, content string) error {
	if by == ByEmail {
		return SendEmail(title, config.RootUserEmail, content)
	}
	if by == ByMessagePusher {
		return SendMessage(title, description, content)
	}
	if by == ByAll {
		// 全部渠道（LTZY-API 新增，D5）：邮件 + MessagePusher + 新增的
		// Webhook/飞书/钉钉/Slack。此前 ByAll 直接落到 unknown，
		// 导致渠道测试通知（controller/channel-test.go）实际上什么都没发。
		// NotifyExtraChannels 内部已逐个渠道兜底记日志、不返回错误，
		// 因此这里只需收集邮件/MessagePusher 的错误即可。
		NotifyExtraChannels(title, description, content)
		var firstErr error
		if config.MessagePusherAddress != "" {
			if err := SendMessage(title, description, content); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if config.RootUserEmail != "" {
			if err := SendEmail(title, config.RootUserEmail, content); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
	return fmt.Errorf("unknown notify method: %s", by)
}
