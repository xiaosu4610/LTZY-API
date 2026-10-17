package model

import (
	"errors"

	"gorm.io/gorm"

	"github.com/xiaosu4610/LTZY-API/common/helper"
)

// Midjourney 任务（LTZY-API 新增，A4）。
//
// 本系统不直连 Discord，只把任务转发到管理员配置的「MJ 上游代理」（如 midjourney-proxy）。
// 因此本地表要承担两件上游代理不负责的事：
//  1. 任务归属：上游只认识自己的 taskId，不认本站用户；用户查列表必须靠本表的 UserId，
//     这也是越权防护的唯一依据（禁止提供不带 UserId 的用户侧查询入口）。
//  2. 任务生命周期留痕：上游任务可能被清理/过期，本地记录仍要能回答「我提交过什么、花了多少额度」。

// Midjourney 任务状态机。
//
// 上游 midjourney-proxy 的 status 只有 NOT_START/SUBMITTED/MODAL/IN_PROGRESS/SUCCESS/FAILURE/CANCEL，
// 本站额外需要一个「待提交」态：本地记录先落库再转发上游，如果上游超时或拒收，
// 用户仍然要能看到这条失败记录（否则「提交了但什么都没发生」无法解释），而那时还没有 taskId。
//
// 取值从 1 开始：0 是 int 的零值，若 0 是合法状态，历史数据缺列时会被误读成合法状态，
// 与项目里 Redemption 状态的做法保持一致。
const (
	MidjourneyStatusPending    = 1 // 待提交：本地已落库，尚未拿到上游任务号
	MidjourneyStatusSubmitted  = 2 // 已提交：上游已受理，排队/生成中
	MidjourneyStatusInProgress = 3 // 进行中：上游已开始生成
	MidjourneyStatusSuccess    = 4 // 成功：上游返回了图片
	MidjourneyStatusFailure    = 5 // 失败：上游失败/取消，或本站提交失败
)

// MidjourneyTask MJ 任务记录。
type MidjourneyTask struct {
	Id       int    `json:"id"`
	UserId   int    `json:"user_id" gorm:"index"`
	Action   string `json:"action" gorm:"index;default:''"` // imagine/upscale/variation/...，取值由 relay/midjourney 的动作为准
	Prompt   string `json:"prompt" gorm:"type:text"`
	TaskId   string `json:"task_id" gorm:"index;default:''"` // 上游任务号；未提交成功时为空
	Status   int    `json:"status" gorm:"index;default:1"`
	Progress int    `json:"progress" gorm:"default:0"` // 0-100
	ImageUrl string `json:"image_url" gorm:"type:text"`
	// FailReason 只在失败时有值：写清是「本站未提交成功」还是「上游生成失败」，
	// 否则用户无法区分是额度/配置问题还是生成内容被拒。
	FailReason string `json:"fail_reason" gorm:"type:text"`
	SubmitTime int64  `json:"submit_time" gorm:"bigint;index;default:0"`
	FinishTime int64  `json:"finish_time" gorm:"bigint;default:0"`
	// Quota 记录本次任务实际扣除的额度。
	// 单独存一份而不是事后按倍率反算：倍率与分组配置随时可能被管理员改，
	// 反算会让历史账单随配置漂移，也无法审计「退款退了多少」。
	Quota int64 `json:"quota" gorm:"bigint;default:0"`
	// CustomId 是按钮型操作（upscale/variation/reroll 等）的上游按钮凭据，
	// 重新提交或人工排查时需要它，因此随任务一起存。
	CustomId string `json:"custom_id" gorm:"type:text"`
}

// IsMidjourneyTaskTerminal 判断是否处于终态。
//
// 终态不可再流转（见 CanMidjourneyTransition），调用方据此短路：
// 终态任务不必再打上游查询接口，避免为已结束的任务浪费上游配额。
func IsMidjourneyTaskTerminal(status int) bool {
	return status == MidjourneyStatusSuccess || status == MidjourneyStatusFailure
}

// CanMidjourneyTransition 判断状态流转是否合法。
//
// 规则（只允许前进、终态不可逆）：
//   - 同状态幂等允许：轮询同一进度是常态；
//   - 终态（成功/失败）不允许再变：上游偶发返回过期快照（例如任务重试后先回 SUBMITTED）
//     会把已成功的任务打回进行中，导致前端进度条回退、图片消失；
//   - 任何非终态都可以转失败：上游失败/取消是合法终点；
//   - 其余按状态数值「只能变大」判断，进度回退（如 进行中 → 已提交）一律拒绝。
//
// 注意这里不直接比较数值大小就完事：Failure(5) 的数值大于 Success(4)，
// 若不先排除终态，失败任务会被判定为可以「前进」到成功。
func CanMidjourneyTransition(from int, to int) bool {
	if from == to {
		return true
	}
	if IsMidjourneyTaskTerminal(from) {
		return false
	}
	if to == MidjourneyStatusFailure || to == MidjourneyStatusSuccess {
		return true
	}
	return to > from
}

// ApplyUpstreamStatus 按上游返回的状态推进本地任务（状态机唯一入口）。
//
// 返回 false 表示本次流转被拒（调用方不应落库）。字段只在有值时覆盖：
// 上游的 fetch 接口在不同状态下会省略部分字段，用空值覆盖会把已有图片/失败原因清掉。
func (task *MidjourneyTask) ApplyUpstreamStatus(status int, progress int, imageUrl string, failReason string) bool {
	if !CanMidjourneyTransition(task.Status, status) {
		return false
	}
	task.Status = status
	// 进度只增不减：上游多实例部署时可能返回旧快照，前端进度条回退比不动更糟
	if progress > task.Progress {
		task.Progress = progress
	}
	if imageUrl != "" {
		task.ImageUrl = imageUrl
	}
	if failReason != "" {
		task.FailReason = failReason
	}
	if status == MidjourneyStatusSuccess {
		task.Progress = 100
	}
	// 终态补 FinishTime，只在首次进入终态时写：重复计入会让耗时统计失真
	if IsMidjourneyTaskTerminal(status) && task.FinishTime == 0 {
		task.FinishTime = helper.GetTimestamp()
	}
	return true
}

// Insert 落库新任务。
func (task *MidjourneyTask) Insert() error {
	if task.SubmitTime == 0 {
		task.SubmitTime = helper.GetTimestamp()
	}
	if task.Status == 0 {
		task.Status = MidjourneyStatusPending
	}
	return DB.Create(task).Error
}

// Update 保存任务的可变字段。
//
// 显式 Select 而不是 Save：Save 会连带覆盖 UserId/Action/Prompt 等不可变字段，
// 一旦上游返回的中间态对象字段不全就会把归属信息写坏。
func (task *MidjourneyTask) Update() error {
	if task.Id == 0 {
		return errors.New("任务 id 为空")
	}
	return DB.Model(task).
		Select("task_id", "status", "progress", "image_url", "fail_reason", "submit_time", "finish_time", "quota").
		Updates(task).Error
}

// MarkMidjourneyTaskFailed 把任务标记为失败并落库（供控制器在提交/轮询失败时调用）。
//
// 已是终态的任务直接拒绝：失败原因只在第一次写入时最有诊断价值，
// 后续（例如用户重试查询）再写一次会把「为什么第一次失败」覆盖掉。
func MarkMidjourneyTaskFailed(task *MidjourneyTask, reason string) error {
	if IsMidjourneyTaskTerminal(task.Status) {
		return errors.New("任务已处于终态，无法再标记为失败")
	}
	if !task.ApplyUpstreamStatus(MidjourneyStatusFailure, task.Progress, "", reason) {
		return errors.New("任务状态不允许标记为失败")
	}
	return task.Update()
}

// GetMidjourneyTaskByTaskId 按上游任务号查询。
// 上游 taskId 全局唯一，但**不建唯一索引**：midjourney-proxy 对重复的相同 prompt
// 会直接返回已存在的任务号（code 21），此时多条本地记录共享同一个上游 taskId 是正常现象。
func GetMidjourneyTaskByTaskId(taskId string) (*MidjourneyTask, error) {
	if taskId == "" {
		return nil, errors.New("task id 为空")
	}
	task := MidjourneyTask{}
	err := DB.Where("task_id = ?", taskId).Order("id desc").First(&task).Error
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// GetMidjourneyTaskById 按本地 id 查询，且强制校验归属。
//
// userId 是必填参数而非可选过滤条件：用户侧任何「按 id 取任务」的入口都必须走这里，
// 这样「忘记加 user_id 条件」这种越权漏洞在编译期就不可能写出来。
// 查不到与不属于自己都返回 ErrRecordNotFound，让上层给出同样的 404 —— 否则
// 错误信息本身就是探测他人任务是否存在的旁路。
func GetMidjourneyTaskById(id int, userId int) (*MidjourneyTask, error) {
	if id <= 0 {
		return nil, errors.New("任务 id 无效")
	}
	if userId <= 0 {
		return nil, errors.New("用户 id 无效")
	}
	task := MidjourneyTask{}
	err := DB.Where("id = ? AND user_id = ?", id, userId).First(&task).Error
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// applyMidjourneyTaskFilters 统一的过滤条件构造，避免各入口各写一份导致条件漏加。
// userId > 0 时强制按归属过滤；status > 0 时按状态过滤。
func applyMidjourneyTaskFilters(tx *gorm.DB, userId int, status int) *gorm.DB {
	if userId > 0 {
		tx = tx.Where("user_id = ?", userId)
	}
	if status > 0 {
		tx = tx.Where("status = ?", status)
	}
	return tx
}

// GetMidjourneyTasksByUserId 分页查询「某用户自己的」任务（用户侧唯一列表入口）。
//
// pageNum 从 0 开始，与项目其他分页接口（GetAllRedemptions 等）保持一致。
// total 一并返回，前端才能渲染总页数。
func GetMidjourneyTasksByUserId(userId int, status int, pageNum int, pageSize int) (tasks []*MidjourneyTask, total int64, err error) {
	if userId <= 0 {
		return nil, 0, errors.New("用户 id 无效")
	}
	if pageNum < 0 {
		pageNum = 0
	}
	if pageSize <= 0 {
		pageSize = 10
	}
	tx := applyMidjourneyTaskFilters(DB.Model(&MidjourneyTask{}), userId, status)
	if err = tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = tx.Order("id desc").Limit(pageSize).Offset(pageNum * pageSize).Find(&tasks).Error
	return tasks, total, err
}

// GetAllMidjourneyTasks 分页查询全部任务（管理面排查用）。
// userId/status 为 0 表示不过滤。
func GetAllMidjourneyTasks(userId int, status int, pageNum int, pageSize int) (tasks []*MidjourneyTask, total int64, err error) {
	if pageNum < 0 {
		pageNum = 0
	}
	if pageSize <= 0 {
		pageSize = 10
	}
	tx := applyMidjourneyTaskFilters(DB.Model(&MidjourneyTask{}), userId, status)
	if err = tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = tx.Order("id desc").Limit(pageSize).Offset(pageNum * pageSize).Find(&tasks).Error
	return tasks, total, err
}
