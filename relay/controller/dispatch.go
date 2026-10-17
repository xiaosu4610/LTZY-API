// Package controller is a package for handling the relay controller
package controller

import (
	"context"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"github.com/xiaosu4610/LTZY-API/model"
	"github.com/xiaosu4610/LTZY-API/relay/apitype"
	"github.com/xiaosu4610/LTZY-API/relay/channeltype"
	"github.com/xiaosu4610/LTZY-API/relay/relaymode"
)

const (
	// openAIProtocolScoreMax 最高优先档（原生 OpenAI / OpenAI 兼容 / 自定义渠道）。
	openAIProtocolScoreMax = 3
	// maxChannelCandidates 候选渠道行数上限。这里只是为了从分组里挑「一个」渠道，
	// 没必要把整张 abilities 表拉出来；按优先级降序取前 200 行已经覆盖任何合理配置。
	maxChannelCandidates = 200
)

// 这里放的是「渠道选择」相关的中继钩子。它本质是中间件，但按本任务的范围约束
// （middleware/** 由其他开发者并行修改，不允许改动），只能落在 relay/controller 中，
// 由 router/relay.go 显式挂在 Distribute 之前。

// ModelessChannelDispatch 为 A6 的透传端点补一次渠道选择，必须挂在 Distribute 之前。
//
// 为什么非要有这一步：middleware.Distribute 的路由入口是「(分组, 模型) → 渠道」，
// 而这批端点里大多数**根本不带 model**（GET /v1/files、DELETE /v1/files/{id}、
// POST /v1/threads …），fine_tuning 带的又是上游基础模型名（如
// gpt-4o-mini-2024-07-18，它并不作为可用模型登记在网关里）。这两类请求走到
// Distribute 只会拿到「当前分组下对于模型 无可用渠道」503 —— 端点看着通了，
// 实际永远不可用。所以在 Distribute 之前先用一次查表把渠道定下来
// （写 ctxkey.SpecificChannelId，Distribute 会识别并装载上下文）。
//
// 取舍（都是有意的）：
//   - 只对 relaymode.IsPassthrough 的端点、且管理员没有显式指定渠道时介入，
//     其它端点的行为一字不变；
//   - 只挑「讲 OpenAI 协议」的渠道：files/fine_tuning/assistants/threads 是 OpenAI
//     私有 REST 形态，挑到 Anthropic/Gemini 之类只会得到一个看不懂的上游报错；
//   - 定下渠道后 Relay 层不会跨渠道重试（shouldRetry 见到 SpecificChannelId 即返回
//     false）。这批端点大量是「创建」类操作（上传文件、建 thread、建 run），
//     跨渠道重试有重复创建的风险，不重试更安全。管理员要固定/更换渠道，
//     仍可用 sk-xxx-<渠道id> 的老办法显式指定，那条路径优先于这里的猜测；
//   - 令牌配了模型白名单时只在白名单内的模型上挑渠道，避免这些端点成为
//     「绕过白名单拿到别的渠道」的口子。
func ModelessChannelDispatch() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !relaymode.IsPassthrough(relaymode.GetByPath(c.Request.URL.Path)) {
			c.Next()
			return
		}
		// 管理员/上层已经指定了具体渠道：尊重它，交给 Distribute 校验并装载
		if _, ok := c.Get(ctxkey.SpecificChannelId); ok {
			c.Next()
			return
		}
		// 没有数据库连接（精简部署、部分单测）时不做任何猜测
		if model.DB == nil {
			c.Next()
			return
		}
		// 分组平时由 Distribute 写入，而它排在本钩子之后，所以这里自己取一次
		// （与 Distribute 用的是同一个带缓存的入口）。
		group, err := model.CacheGetUserGroup(c.GetInt(ctxkey.Id))
		if err != nil || group == "" {
			// 取不到分组就交回 Distribute，让它给出与其它端点一致的错误
			c.Next()
			return
		}

		route := selectOpenAIProtocolChannel(c.Request.Context(), group, allowedModelsOfToken(c))
		if route == nil {
			// 分组里没有 OpenAI 协议渠道：不硬凑，交回 Distribute 报「无可用渠道」
			c.Next()
			return
		}
		// 无模型请求补一个真实模型名，让上下文/日志/监控不至于出现「空模型」归属。
		// 请求自带 model（fine_tuning）时保留原值：它才是调用方视角的模型。
		if c.GetString(ctxkey.RequestModel) == "" {
			c.Set(ctxkey.RequestModel, route.model)
		}
		c.Set(ctxkey.SpecificChannelId, strconv.Itoa(route.channelId))
		c.Next()
	}
}

// openAIProtocolRoute 选中的上游渠道；只带路由必需的信息，避免把可变的
// model.Channel 传出去被下游误改。
type openAIProtocolRoute struct {
	channelId int
	model     string
}

// selectOpenAIProtocolChannel 在分组内挑一个讲 OpenAI 协议的渠道。
//
// 为什么用一次 join 查询，而不是「遍历分组里的模型、逐个探测是否有渠道」：
// CacheGetRandomSatisfiedChannel 必须先有模型名，而 MEMORY_CACHE_ENABLED 默认关闭，
// 逐个探测会退化成「每个模型一次数据库查询」（分组常有几十上百个模型），
// 每个透传请求都做一遍是不可接受的放大。
func selectOpenAIProtocolChannel(ctx context.Context, group string, allowedModels map[string]bool) *openAIProtocolRoute {
	if group == "" || model.DB == nil {
		return nil
	}
	// group 是 SQL 保留字，按方言加引号（与 model 包内既有写法保持一致）
	groupCol := "abilities.`group`"
	if common.UsingPostgreSQL {
		groupCol = `abilities."group"`
	}
	var rows []struct {
		Model     string
		ChannelId int
		Type      int
	}
	err := model.DB.Table("abilities").
		Select("abilities.model as model, abilities.channel_id as channel_id, channels.type as type").
		Joins("join channels on channels.id = abilities.channel_id").
		Where(groupCol+" = ?", group).
		Where("abilities.enabled = ?", true).
		Where("channels.status = ?", model.ChannelStatusEnabled).
		Where("channels.type in ?", openAIProtocolTypes).
		Order("coalesce(abilities.priority, 0) desc, channels.id asc").
		Limit(maxChannelCandidates).
		Scan(&rows).Error
	if err != nil {
		logger.SysError("failed to select channel for passthrough endpoint: " + err.Error())
		return nil
	}
	bestScore := 0
	var picked *openAIProtocolRoute
	for _, row := range rows {
		if len(allowedModels) > 0 && !allowedModels[row.Model] {
			continue
		}
		score := openAIProtocolScore(row.Type)
		if score > bestScore {
			bestScore = score
			picked = &openAIProtocolRoute{channelId: row.ChannelId, model: row.Model}
			if bestScore == openAIProtocolScoreMax {
				// 已是最高优先档，再往下找只会更差；按优先级+渠道 id 排序，
				// 因此结果对同一分组是稳定的（可预期 > 随机）。
				break
			}
		}
	}
	return picked
}

// allowedModelsOfToken 读取令牌的模型白名单（空表示不限制）。
// TokenAuth 写入的是逗号分隔的字符串，与它内部 isModelInList 的约定一致。
func allowedModelsOfToken(c *gin.Context) map[string]bool {
	raw := strings.TrimSpace(c.GetString(ctxkey.AvailableModels))
	if raw == "" {
		return nil
	}
	allowed := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			allowed[part] = true
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	return allowed
}

// openAIProtocolTypes 预先算出「可能跑得动这些端点」的渠道类型，供 SQL 过滤使用。
// 用循环生成而不是手写常量列表：channeltype 的取值是 iota 序列，
// 手写列表迟早会和 ToAPIType 的映射脱节。
var openAIProtocolTypes = func() []int {
	out := make([]int, 0, 8)
	for channelType := 1; channelType < channeltype.Dummy; channelType++ {
		if openAIProtocolScore(channelType) > 0 {
			out = append(out, channelType)
		}
	}
	return out
}()

// openAIProtocolScore 评估某渠道类型「跑得动 OpenAI 私有 REST 端点」的可能性，
// 分数越高越优先，0 表示不适用。
//   - 3：原生 OpenAI / OpenAI 兼容网关 / 自定义渠道 —— 基本就是同一套 REST 形态；
//   - 2：其它走 OpenAI 协议适配器的第三方渠道（可能是代理，也可能上游不支持这些端点；
//     不支持时上游会返回真实错误，我们原样回传给调用方）；
//   - 0：非 OpenAI 协议（Anthropic/Gemini…），或形态特殊 —— Azure 的 files 路径带
//     deployment 前缀、不适用通用拼法，Dummy 不是真实上游，Unknown 不该被选中。
func openAIProtocolScore(channelType int) int {
	switch channelType {
	case channeltype.OpenAI, channeltype.OpenAICompatible, channeltype.Custom:
		return openAIProtocolScoreMax
	case channeltype.Azure, channeltype.Dummy, channeltype.Unknown:
		return 0
	}
	if channeltype.ToAPIType(channelType) == apitype.OpenAI {
		return 2
	}
	return 0
}
