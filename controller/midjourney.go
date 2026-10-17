package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/common/helper"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"github.com/xiaosu4610/LTZY-API/model"
	billingratio "github.com/xiaosu4610/LTZY-API/relay/billing/ratio"
	"github.com/xiaosu4610/LTZY-API/relay/midjourney"
)

// Midjourney 任务接口（LTZY-API 新增，A4）。
//
// 与 /v1 中继（TokenAuth + relay 适配器）的区别：MJ 是「提交 → 轮询 → 取图」的异步任务，
// 上游是管理员配置的 MJ 代理服务，不存在渠道分发与模型映射，所以直接走 /api 下的
// 会话鉴权接口，而不是塞进 relay 适配器体系。
//
// 计费口径：按「次」预扣，额度 = 模型倍率 × 分组倍率 × midjourneyQuotaUnit；
// 采用「先扣费后提交、上游提交失败即退款」，理由是上游一旦受理就无法回滚，
// 先扣费可以保证不会出现「上游跑了图、本站没收到钱」。

// midjourneyQuotaUnit 单次 MJ 任务的额度基数，与图片/视频生成保持一致（1000）。
const midjourneyQuotaUnit = 1000

// maxMidjourneyPageSize 管理面分页上限，避免一次查询把整表拉进内存。
const maxMidjourneyPageSize = 100

// newMidjourneyClient 构造上游客户端的唯一入口。
//
// 做成包级变量而不是直接调用 midjourney.NewClient：单测要把它指向 httptest 假上游。
// 生产路径始终是「按配置构造」，测试替换不会影响线上行为。
var newMidjourneyClient = midjourney.NewClient

// SubmitMidjourneyTask 提交 MJ 任务：校验 → 落库 → 预扣额度 → 转发上游。
//
// 顺序刻意如此，每一步都有不可交换的理由：
//  1. 先确认上游已配置：未配置时若先扣费再报错，用户会看到「余额少了但没图」；
//  2. 再落本地记录：上游受理后必须有地方存任务号，上游失败时这条记录是用户唯一能追查的凭据；
//  3. 再预扣额度：额度不足直接拒绝，不必浪费一次上游调用；
//  4. 最后转发上游：失败则退款（退款原因写进本地任务，便于对账）。
func SubmitMidjourneyTask(c *gin.Context) {
	ctx := c.Request.Context()
	userId := c.GetInt(ctxkey.Id)
	if userId <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "未登录或登录状态已失效"})
		return
	}

	var req midjourney.SubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "请求体解析失败：" + err.Error()})
		return
	}
	normalized, err := req.Normalize()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	upstream, err := newMidjourneyClient()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	quota, modelRatio, groupRatio := calcMidjourneyQuota(ctx, userId, normalized.Action)
	userQuota, err := model.CacheGetUserQuota(ctx, userId)
	if err != nil {
		logger.Errorf(ctx, "读取用户额度失败：%s", err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "读取用户额度失败，请稍后重试"})
		return
	}
	if userQuota < quota {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("额度不足：本次任务需要 %d 额度，当前剩余 %d", quota, userQuota),
		})
		return
	}

	task := &model.MidjourneyTask{
		UserId:     userId,
		Action:     normalized.Action,
		Prompt:     normalized.Prompt,
		CustomId:   normalized.CustomId,
		Status:     model.MidjourneyStatusPending,
		Quota:      quota,
		SubmitTime: helper.GetTimestamp(),
	}
	if err := task.Insert(); err != nil {
		logger.Errorf(ctx, "创建 Midjourney 任务记录失败：%s", err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "创建任务记录失败，请稍后重试"})
		return
	}

	if err := consumeMidjourneyQuota(ctx, userId, quota); err != nil {
		_ = model.MarkMidjourneyTaskFailed(task, "扣费失败："+err.Error())
		logger.Errorf(ctx, "Midjourney 任务预扣额度失败：%s", err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "扣费失败，任务未提交：" + err.Error()})
		return
	}

	upstreamTaskId, err := upstream.Submit(ctx, *normalized)
	if err != nil {
		refundMidjourneyQuota(ctx, userId, quota, err.Error())
		_ = model.MarkMidjourneyTaskFailed(task, "提交上游失败："+err.Error())
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "任务提交上游失败：" + err.Error(),
			// 仍然回传本地任务号：用户可以在任务列表里看到这条失败记录和失败原因
			"data": gin.H{"task_id": task.Id},
		})
		return
	}

	task.TaskId = upstreamTaskId
	task.ApplyUpstreamStatus(model.MidjourneyStatusSubmitted, 0, "", "")
	if err := task.Update(); err != nil {
		// 上游已受理、额度已扣，只有本地状态没写成功。
		// 不能吞掉错误（否则任务号丢失、用户无从查询），也不能让用户重试（会重复扣费），
		// 因此记系统日志并在响应里如实带上任务号，让前端/用户凭任务号追查。
		logger.Errorf(ctx, "Midjourney 任务 %d 状态写库失败：%s", task.Id, err.Error())
	}
	recordMidjourneyConsumeLog(ctx, userId, quota, midjourneyBillingModelName(normalized.Action), modelRatio, groupRatio, upstreamTaskId)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"task_id":          task.Id,
			"upstream_task_id": upstreamTaskId,
			"status":           task.Status,
			"quota":            quota,
		},
	})
}

// FetchMidjourneyTask 查询任务：命中本地记录后按需回源上游并更新本地状态。
//
// :id 既接受本地 id 也接受上游 taskId，两种都强制按当前用户过滤（见 resolveUserMidjourneyTask）。
func FetchMidjourneyTask(c *gin.Context) {
	ctx := c.Request.Context()
	userId := c.GetInt(ctxkey.Id)
	if userId <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "未登录或登录状态已失效"})
		return
	}

	task, err := resolveUserMidjourneyTask(c.Param("id"), userId)
	if err != nil {
		// 「不存在」与「不属于自己」返回同一提示：错误信息本身不能成为
		// 探测他人任务是否存在的旁路（越权防护的一部分）。
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "任务不存在"})
		return
	}

	// 终态不再回源：结果已定，继续查询只消耗上游配额；
	// 没有上游任务号（提交阶段就失败）也无从查询。两者都直接返回本地记录。
	if model.IsMidjourneyTaskTerminal(task.Status) || task.TaskId == "" {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": task})
		return
	}

	upstream, err := newMidjourneyClient()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error(), "data": task})
		return
	}
	info, err := upstream.Fetch(ctx, task.TaskId)
	if err != nil {
		// 查询失败不改本地状态：网络抖动或上游重启不该把正在生成的任务判成失败，
		// 用户重新查询即可（前端轮询场景下这是常态）。
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "查询上游任务失败：" + err.Error(), "data": task})
		return
	}

	if task.ApplyUpstreamStatus(info.StatusCode, info.Progress, info.ImageUrl, info.FailReason) {
		if err := task.Update(); err != nil {
			logger.Errorf(ctx, "Midjourney 任务 %d 状态更新失败：%s", task.Id, err.Error())
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": task})
}

// GetUserMidjourneyTasks 分页查询当前用户自己的任务。
//
// userId 只取自会话（ctxkey.Id），不接受任何请求参数覆盖 —— 否则构造一个 user_id 参数
// 就能看别人的任务，这是本接口唯一的越权风险点。
func GetUserMidjourneyTasks(c *gin.Context) {
	userId := c.GetInt(ctxkey.Id)
	if userId <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "未登录或登录状态已失效"})
		return
	}
	page := parseMidjourneyPage(c.Query("p"))
	status := parseMidjourneyStatus(c.Query("status"))

	tasks, total, err := model.GetMidjourneyTasksByUserId(userId, status, page, config.ItemsPerPage)
	if err != nil {
		logger.SysError("查询 Midjourney 任务失败：" + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "查询任务失败，请稍后重试"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items":     tasks,
			"total":     total,
			"page":      page,
			"page_size": config.ItemsPerPage,
		},
	})
}

// GetAllMidjourneyTasks 分页查询全部任务（管理面，路由上挂 AdminAuth）。
//
// 存在的意义是排障：用户报「任务卡住了」时，管理员需要看到全站任务而不只是自己的。
func GetAllMidjourneyTasks(c *gin.Context) {
	page := parseMidjourneyPage(c.Query("p"))
	status := parseMidjourneyStatus(c.Query("status"))
	queryUserId, _ := strconv.Atoi(c.Query("user_id"))
	if queryUserId < 0 {
		queryUserId = 0
	}
	pageSize := parseMidjourneyPageSize(c.Query("page_size"))

	tasks, total, err := model.GetAllMidjourneyTasks(queryUserId, status, page, pageSize)
	if err != nil {
		logger.SysError("查询 Midjourney 任务失败：" + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "查询任务失败，请稍后重试"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items":     tasks,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// resolveUserMidjourneyTask 把路径参数解析为「当前用户的」任务。
//
// 先按本地 id 查、再按上游 taskId 查：用户界面上拿到的是本地 id，
// 而工单里用户常常直接贴上游任务号，两种都能查才不至于让客服无能为力。
// 两条路径都必须带 userId 条件，越权防护不能只做在其中一条上。
func resolveUserMidjourneyTask(idOrTaskId string, userId int) (*model.MidjourneyTask, error) {
	value := strings.TrimSpace(idOrTaskId)
	if value == "" {
		return nil, errors.New("任务 id 为空")
	}
	if localId, err := strconv.Atoi(value); err == nil {
		task, err := model.GetMidjourneyTaskById(localId, userId)
		if err == nil {
			return task, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		// 本地 id 不存在时继续按上游任务号查找（例如本地记录 id 与上游号恰好都是数字）
	}
	task, err := model.GetMidjourneyTaskByTaskId(value)
	if err != nil {
		return nil, err
	}
	// 二次校验归属：上游任务号可能被多个本地用户引用（同一上游任务被不同用户分别 upsample），
	// 因此按 taskId 查到的记录必须再核对一次 UserId。
	if task.UserId != userId {
		return nil, errors.New("任务不存在")
	}
	return task, nil
}

// midjourneyBillingModelName 计费与日志中使用的模型名。
//
// 把动作编进模型名（mj_imagine / mj_upscale）而不是编造一个统一假模型：
// 消费日志按 ModelName 聚合，不同动作的成本差异正是账单要回答的问题。
func midjourneyBillingModelName(action string) string {
	return "mj_" + action
}

// calcMidjourneyQuota 计算本次任务额度：模型倍率 × 分组倍率 × 单位额度。
func calcMidjourneyQuota(ctx context.Context, userId int, action string) (quota int64, modelRatio float64, groupRatio float64) {
	modelName := midjourneyBillingModelName(action)
	modelRatio = getMidjourneyModelRatio(action)

	group := "default"
	userGroup, err := model.CacheGetUserGroup(userId)
	if err != nil {
		// 分组只影响折扣，读不到就按默认分组计价：不能因为计费辅助信息缺失而拒绝用户提交
		logger.Errorf(ctx, "读取用户分组失败，按默认分组计价：%s", err.Error())
	} else if strings.TrimSpace(userGroup) != "" {
		group = strings.TrimSpace(userGroup)
	}
	groupRatio = billingratio.GetGroupRatioForModel(group, modelName)

	// RatioScale：倍率表以美元为基准，本系统按人民币计价，扣费前统一换算
	quota = int64(modelRatio * groupRatio * midjourneyQuotaUnit * billingratio.RatioScale())
	if quota < 0 {
		quota = 0
	}
	return quota, modelRatio, groupRatio
}

// getMidjourneyModelRatio 取 MJ 任务倍率。
//
// 三级回退：MJ 专属配置 → 通用 ModelRatio 里的 mj_<action> → 1.0。
// 这里刻意不用 billingratio.GetModelRatio：它对未知模型兜底 30，
// 未配置 MJ 倍率的部署会被多扣 30 倍，属于静默的定价事故。
func getMidjourneyModelRatio(action string) float64 {
	config.OptionMapRWMutex.RLock()
	dedicated := config.OptionMap["MidjourneyModelRatio"]
	general := config.OptionMap["ModelRatio"]
	config.OptionMapRWMutex.RUnlock()

	// MJ 专属配置既支持按动作区分（{"imagine":1.5,"upscale":0.5}），也支持一个数管全部动作（1.5）
	if value := strings.TrimSpace(dedicated); value != "" {
		if ratios := parseMidjourneyRatioMap(value); ratios != nil {
			if ratio, ok := ratios[action]; ok && ratio >= 0 {
				return ratio
			}
		} else if ratio, err := strconv.ParseFloat(value, 64); err == nil && ratio >= 0 {
			return ratio
		} else {
			logger.SysError("MidjourneyModelRatio 配置无法解析（应为数字或 {\"imagine\":1.5} 形式的 JSON）：" + value)
		}
	}
	if ratios := parseMidjourneyRatioMap(general); ratios != nil {
		if ratio, ok := ratios[midjourneyBillingModelName(action)]; ok && ratio >= 0 {
			return ratio
		}
	}
	return 1.0
}

// parseMidjourneyRatioMap 解析倍率 JSON；不是对象时返回 nil（调用方再尝试按单个数字解析）。
func parseMidjourneyRatioMap(value string) map[string]float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	ratios := map[string]float64{}
	if err := json.Unmarshal([]byte(value), &ratios); err != nil {
		return nil
	}
	return ratios
}

// consumeMidjourneyQuota 预扣用户额度。
//
// 只扣「用户」维度：本接口走会话鉴权，没有令牌维度，
// 硬套 PreConsumeTokenQuota 需要 tokenId，会把不属于该令牌的消费记到别的令牌上。
func consumeMidjourneyQuota(ctx context.Context, userId int, quota int64) error {
	if quota <= 0 {
		return nil
	}
	if err := model.DecreaseUserQuota(userId, quota); err != nil {
		return err
	}
	if common.RedisEnabled {
		if err := model.CacheDecreaseUserQuota(userId, quota); err != nil {
			// 缓存只是额度判断的加速层，DB 才是账本；同步失败只记日志，不回滚已完成的扣费
			logger.Errorf(ctx, "同步用户额度缓存失败：%s", err.Error())
		}
	}
	return nil
}

// recordMidjourneyConsumeLog 记消费流水与用量统计。
//
// 只在上游确认受理后调用：消费日志是账单凭据，
// 出现「有流水没任务」的记录会让对账无从下手（退款时会另记一条充值类型流水）。
func recordMidjourneyConsumeLog(ctx context.Context, userId int, quota int64, modelName string, modelRatio float64, groupRatio float64, upstreamTaskId string) {
	if quota <= 0 {
		return
	}
	model.RecordConsumeLog(ctx, &model.Log{
		UserId:    userId,
		ModelName: modelName,
		Quota:     int(quota),
		Content:   fmt.Sprintf("倍率：%.2f × %.2f (midjourney, 任务 %s)", modelRatio, groupRatio, upstreamTaskId),
	})
	model.UpdateUserUsedQuotaAndRequestCount(userId, quota)
	// 预算计数：tokenId 传 0，RecordBudgetUsage 会自动跳过令牌维度
	model.RecordBudgetUsage(userId, 0, quota)
}

// refundMidjourneyQuota 退还预扣额度（上游提交失败时调用）。
//
// 退款失败必须记日志并带上用户与额度：这是「用户被扣了钱但没拿到任务」的资金差异，
// 需要人工介入，不能悄悄放过。
func refundMidjourneyQuota(ctx context.Context, userId int, quota int64, reason string) {
	if quota <= 0 {
		return
	}
	if err := model.IncreaseUserQuota(userId, quota); err != nil {
		logger.Errorf(ctx, "Midjourney 退款失败（已扣费但上游未受理），用户 %d 额度 %d：%s", userId, quota, err.Error())
		return
	}
	if common.RedisEnabled {
		// 直接失效缓存键而不是对称地 DecrBy：
		// 退款可能发生在缓存键已过期之后，那时 DecrBy(负数) 会凭空造出一个很小余额的键，
		// 让用户被误判为额度不足；删键则强制下次回源查库拿到真实余额。
		key := fmt.Sprintf("user_quota:%d", userId) // 与 model.CacheGetUserQuota 使用同一键格式
		if err := common.RedisDel(key); err != nil {
			logger.Errorf(ctx, "清除用户额度缓存失败：%s", err.Error())
		}
	}
	model.RecordTopupLog(ctx, userId, "Midjourney 任务提交失败退款："+reason, int(quota))
}

// parseMidjourneyPage 解析页码（从 0 开始，与项目其他分页接口一致）。
func parseMidjourneyPage(value string) int {
	page, err := strconv.Atoi(value)
	if err != nil || page < 0 {
		return 0
	}
	return page
}

// parseMidjourneyStatus 解析状态过滤；非数字或 0 表示不过滤。
func parseMidjourneyStatus(value string) int {
	status, err := strconv.Atoi(value)
	if err != nil || status < 0 || status > model.MidjourneyStatusFailure {
		return 0
	}
	return status
}

// parseMidjourneyPageSize 解析管理面每页条数，越界时回退到默认值并封顶。
func parseMidjourneyPageSize(value string) int {
	pageSize, err := strconv.Atoi(value)
	if err != nil || pageSize <= 0 {
		return config.ItemsPerPage
	}
	if pageSize > maxMidjourneyPageSize {
		return maxMidjourneyPageSize
	}
	return pageSize
}
