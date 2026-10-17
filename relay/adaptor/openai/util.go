package openai

import (
	"context"
	"fmt"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"github.com/xiaosu4610/LTZY-API/relay/model"
)

func ErrorWrapper(err error, code string, statusCode int) *model.ErrorWithStatusCode {
	// 这里是中继错误的总出口：%+v 会把 *url.Error 的结构体整个打出来（含 URL 字段，
	// 而部分渠道把 access_token/api_key 放在 query 上），返回给调用方的 Message
	// 同样是原文，普通用户就能看到上游凭据。两处都先脱敏。
	safeMessage := common.MaskString(err.Error())
	logger.Error(context.TODO(), fmt.Sprintf("[%s]%s", code, common.MaskString(fmt.Sprintf("%+v", err))))

	Error := model.Error{
		Message: safeMessage,
		Type:    "one_api_error",
		Code:    code,
	}
	return &model.ErrorWithStatusCode{
		Error:      Error,
		StatusCode: statusCode,
	}
}
