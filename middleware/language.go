package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common/i18n"
)

// Language 解析请求语言并写入 context（LTZY-API 改造，E6）。
//
// 原实现只认「zh* → zh-CN，其余一律 en」，于是 i18n 里新增的
// fr / ru / es / ar 词条在生产请求下**永远选不中**——文件加了等于没加。
// 现改为交给 i18n.Negotiate 做完整的 Accept-Language 协商
// （支持 q 值排序、zh-TW/zh-Hans 等变体、通配与非法串容错）。
//
// 兼容性：对 zh / en 的行为与原来等价；空请求头仍回退 en。
func Language() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(i18n.ContextKey, i18n.Negotiate(c.GetHeader("Accept-Language")))
		c.Next()
	}
}
