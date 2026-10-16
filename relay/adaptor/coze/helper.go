package coze

import "github.com/xiaosu4610/LTZY-API/relay/adaptor/coze/constant/event"

func event2StopReason(e *string) string {
	if e == nil || *e == event.Message {
		return ""
	}
	return "stop"
}
