package controller

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/helper"
	"github.com/xiaosu4610/LTZY-API/common/i18n"
	"github.com/xiaosu4610/LTZY-API/model"

	"github.com/gin-gonic/gin"
)

func GetOptions(c *gin.Context) {
	var options []*model.Option
	config.OptionMapRWMutex.Lock()
	for k, v := range config.OptionMap {
		// 密钥类配置不回显（E9：判定口径统一收到 model.IsSensitiveOptionKey，
		// 避免"设置列表里隐藏、变更历史里却明文"这类规则漂移）。
		// 前端对这些字段本来就是「留空 = 不修改」的用法，不返回不会丢配置。
		// 除 Token/Secret 结尾外，还覆盖 TurnstileSecretKey、MidjourneyApiKey 两个漏网之鱼。
		if model.IsSensitiveOptionKey(k) {
			continue
		}
		options = append(options, &model.Option{
			Key:   k,
			Value: helper.Interface2String(v),
		})
	}
	config.OptionMapRWMutex.Unlock()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    options,
	})
	return
}

func UpdateOption(c *gin.Context) {
	var option model.Option
	err := json.NewDecoder(c.Request.Body).Decode(&option)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": i18n.Translate(c, "invalid_parameter"),
		})
		return
	}
	switch option.Key {
	case "Theme":
		if !config.ValidThemes[option.Value] {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": i18n.Translate(c, "option.invalid_theme"),
			})
			return
		}
	case "GitHubOAuthEnabled":
		if option.Value == "true" && config.GitHubClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": i18n.Translate(c, "option.github_oauth_not_configured"),
			})
			return
		}
	case "EmailDomainRestrictionEnabled":
		if option.Value == "true" && len(config.EmailDomainWhitelist) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": i18n.Translate(c, "option.email_domain_not_configured"),
			})
			return
		}
	case "WeChatAuthEnabled":
		if option.Value == "true" && config.WeChatServerAddress == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": i18n.Translate(c, "option.wechat_not_configured"),
			})
			return
		}
	case "TurnstileCheckEnabled":
		if option.Value == "true" && config.TurnstileSiteKey == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": i18n.Translate(c, "option.turnstile_not_configured"),
			})
			return
		}
	}
	// 带上操作者信息：配置变更历史（E9）要能回答"谁改的"，回滚链路同样依赖它。
	// 取值来自 RootAuth 写入的 context（middleware.authHelper），与 D4 审计同源。
	_, err = model.UpdateOptionWithOperator(option.Key, option.Value, c.GetInt("id"), c.GetString("username"), "")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

// GetOptionHistory 配置变更历史分页查询（E9）。
// 参数：p（页码，默认 1）、page_size（默认 10，上限 100）、key（可选，只看某个配置键）。
//
// 返回的 old_value/new_value 对密钥类配置是脱敏值（IsSensitive=true 提示前端）；
// 原文仅存于历史表、只在回滚时使用，任何接口都不会回显。
func GetOptionHistory(c *gin.Context) {
	pageNum, _ := strconv.Atoi(c.Query("p"))
	if pageNum <= 0 {
		pageNum = 1
	}
	pageSize, _ := strconv.Atoi(c.Query("page_size"))
	if pageSize <= 0 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100 // 与审计日志接口一致：防止一次把历史表拖垮
	}
	key := c.Query("key")

	histories, total, err := model.GetOptionHistories(pageNum, pageSize, key)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items":     histories,
			"total":     total,
			"p":         pageNum,
			"page_size": pageSize,
		},
	})
}

// RollbackOption 按历史记录 id 回滚一次配置变更（E9）。
// 请求体：{"id": 12}；语义是"撤销这条记录对应的那次变更"，即把配置恢复成该记录的旧值。
//
// 回滚会再写一条历史（is_rollback=true），因此"谁改回去的、改成了什么"同样可追溯。
func RollbackOption(c *gin.Context) {
	var payload struct {
		Id int `json:"id"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil || payload.Id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": i18n.Translate(c, "invalid_parameter"),
		})
		return
	}
	// 操作者取自 RootAuth 写入的 context（middleware.authHelper），与 D4 审计同一来源
	history, err := model.RollbackOptionById(payload.Id, c.GetInt("id"), c.GetString("username"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"history": history,
			"key":     history.Key,
		},
	})
}

// GetRestartRequiredOptions 返回"需重启生效"的配置清单（E9）。
//
// 存在的意义：这些键写进 options 表后运行时不会有任何变化，属于静默失效；
// 显式暴露清单后，前端可以在设置页提示管理员"改完必须改环境变量并重启"。
func GetRestartRequiredOptions(c *gin.Context) {
	items := model.RestartRequiredOptions()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items": items,
			"count": len(items),
		},
	})
}
