package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/model"
	billingratio "github.com/xiaosu4610/LTZY-API/relay/billing/ratio"
	"github.com/xiaosu4610/LTZY-API/relay/midjourney"
)

// MJ 控制器单测（LTZY-API 新增，A4）。
//
// 只 mock「上游 HTTP 服务」，数据库用真实 SQLite：本控制器的关键行为（扣费、退款、
// 越权过滤）几乎全部落在 SQL 上，把 model 层也 mock 掉等于什么都没验。

// initMidjourneyControllerTest 建临时库并挂好依赖的全局表。
func initMidjourneyControllerTest(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.SQLitePath = filepath.Join(t.TempDir(), "mj-controller-test.db")
	common.UsingSQLite = true
	common.RedisEnabled = false

	db, err := gorm.Open(sqlite.Open(common.SQLitePath), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败：%v", err)
	}
	model.DB = db
	model.LOG_DB = db
	if err := db.AutoMigrate(&model.MidjourneyTask{}, &model.User{}, &model.Log{}, &model.QuotaBudget{}); err != nil {
		t.Fatalf("迁移测试表失败：%v", err)
	}

	// 消费日志默认开关受配置影响，这里显式打开，保证账单断言与线上默认一致
	logConsumeEnabled := config.LogConsumeEnabled
	config.LogConsumeEnabled = true

	t.Cleanup(func() {
		config.LogConsumeEnabled = logConsumeEnabled
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

// setMidjourneyTestOptionMap 替换运行期配置表并在用例结束后还原。
func setMidjourneyTestOptionMap(t *testing.T, values map[string]string) {
	t.Helper()
	config.OptionMapRWMutex.Lock()
	previous := config.OptionMap
	config.OptionMap = values
	config.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		config.OptionMapRWMutex.Lock()
		config.OptionMap = previous
		config.OptionMapRWMutex.Unlock()
	})
}

// useRealMidjourneyClient 让用例走「按配置构造客户端」的真实路径（验证未配置上游的报错）。
func useRealMidjourneyClient(t *testing.T) {
	t.Helper()
	previous := newMidjourneyClient
	newMidjourneyClient = midjourney.NewClient
	t.Cleanup(func() { newMidjourneyClient = previous })
}

// useFakeMidjourneyUpstream 把上游指向 httptest 假服务。
func useFakeMidjourneyUpstream(t *testing.T, server *httptest.Server) {
	t.Helper()
	previous := newMidjourneyClient
	newMidjourneyClient = func() (*midjourney.Client, error) {
		return midjourney.NewClientWithBaseURL(server.URL, "sk-test"), nil
	}
	t.Cleanup(func() { newMidjourneyClient = previous })
}

// seedMidjourneyUser 建一个可用用户；Username/AccessToken/AffCode 都是唯一索引，必须各不相同。
func seedMidjourneyUser(t *testing.T, id int, quota int64) {
	t.Helper()
	user := &model.User{
		Id:          id,
		Username:    fmt.Sprintf("mj-user-%d", id),
		AccessToken: fmt.Sprintf("mj-access-token-%08d", id),
		AffCode:     fmt.Sprintf("mj-aff-%d", id),
		Group:       "default",
		Status:      model.UserStatusEnabled,
		Quota:       quota,
	}
	if err := model.DB.Create(user).Error; err != nil {
		t.Fatalf("创建测试用户失败：%v", err)
	}
}

// callMidjourneyHandler 构造带会话用户的 gin 上下文并同步调用 handler。
func callMidjourneyHandler(t *testing.T, handler gin.HandlerFunc, method string, target string, body string, userId int, params gin.Params) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	if userId > 0 {
		c.Set(ctxkey.Id, userId)
	}
	handler(c)
	return recorder
}

// decodeMidjourneyResponse 解析统一的 {success,message,data} 响应。
func decodeMidjourneyResponse(t *testing.T, recorder *httptest.ResponseRecorder) (bool, string, map[string]any) {
	t.Helper()
	var parsed struct {
		Success bool            `json:"success"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("响应不是合法 JSON：%s", recorder.Body.String())
	}
	data := map[string]any{}
	if len(parsed.Data) > 0 {
		_ = json.Unmarshal(parsed.Data, &data)
	}
	return parsed.Success, parsed.Message, data
}

func userQuotaOf(t *testing.T, userId int) int64 {
	t.Helper()
	quota, err := model.GetUserQuota(userId)
	if err != nil {
		t.Fatalf("读取用户额度失败：%v", err)
	}
	return quota
}

func countMidjourneyTasks(t *testing.T, userId int) int64 {
	t.Helper()
	_, total, err := model.GetMidjourneyTasksByUserId(userId, 0, 0, 10)
	if err != nil {
		t.Fatalf("统计任务数失败：%v", err)
	}
	return total
}

func countConsumeLogs(t *testing.T, userId int) int64 {
	t.Helper()
	var count int64
	if err := model.LOG_DB.Model(&model.Log{}).Where("user_id = ? AND type = ?", userId, model.LogTypeConsume).Count(&count).Error; err != nil {
		t.Fatalf("统计消费日志失败：%v", err)
	}
	return count
}

// TestSubmitMidjourneyTaskWithoutUpstream 未配置上游：明确报错，且不扣费、不落任务记录。
func TestSubmitMidjourneyTaskWithoutUpstream(t *testing.T) {
	initMidjourneyControllerTest(t)
	setMidjourneyTestOptionMap(t, map[string]string{}) // 模拟管理员尚未配置上游
	useRealMidjourneyClient(t)
	seedMidjourneyUser(t, 1, 100000)

	recorder := callMidjourneyHandler(t, SubmitMidjourneyTask, http.MethodPost, "/api/mj/submit",
		`{"action":"imagine","prompt":"a cat"}`, 1, nil)

	success, message, _ := decodeMidjourneyResponse(t, recorder)
	if success {
		t.Fatal("未配置上游时不应返回成功")
	}
	if !strings.Contains(message, midjourney.OptionUpstreamURL) {
		t.Fatalf("错误信息应指明缺失的配置项，实际：%s", message)
	}
	// 关键：配置缺失属于「系统问题」，不能让用户承担扣费或残留垃圾记录
	if quota := userQuotaOf(t, 1); quota != 100000 {
		t.Fatalf("未配置上游时不应扣费，当前额度 %d", quota)
	}
	if total := countMidjourneyTasks(t, 1); total != 0 {
		t.Fatalf("未配置上游时不应留下任务记录，实际 %d 条", total)
	}
}

// TestSubmitMidjourneyTaskSucceeds 正常提交：落库 + 按次扣费 + 记消费流水。
func TestSubmitMidjourneyTaskSucceeds(t *testing.T) {
	initMidjourneyControllerTest(t)
	setMidjourneyTestOptionMap(t, map[string]string{})
	seedMidjourneyUser(t, 1, 100000)

	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"code":1,"description":"Submit success","result":"up-1"}`))
	}))
	defer server.Close()
	useFakeMidjourneyUpstream(t, server)

	recorder := callMidjourneyHandler(t, SubmitMidjourneyTask, http.MethodPost, "/api/mj/submit",
		`{"action":"imagine","prompt":"a cat"}`, 1, nil)

	success, message, data := decodeMidjourneyResponse(t, recorder)
	if !success {
		t.Fatalf("提交应成功，实际 message=%s", message)
	}
	if gotPath != "/mj/submit/imagine" {
		t.Fatalf("上游路径错误：%s", gotPath)
	}
	if data["upstream_task_id"] != "up-1" {
		t.Fatalf("响应未回传上游任务号：%+v", data)
	}
	// 默认倍率 1.0 × 默认分组 1.0 × 单位额度 1000 × 货币换算系数
	wantQuota := expectedMidjourneyQuota()
	if quota, ok := data["quota"].(float64); !ok || int64(quota) != wantQuota {
		t.Fatalf("响应额度应为 %d，实际 %+v", wantQuota, data["quota"])
	}

	tasks, total, err := model.GetMidjourneyTasksByUserId(1, 0, 0, 10)
	if err != nil || total != 1 {
		t.Fatalf("应落库 1 条任务，实际 %d（err=%v）", total, err)
	}
	task := tasks[0]
	if task.TaskId != "up-1" || task.Status != model.MidjourneyStatusSubmitted || task.UserId != 1 {
		t.Fatalf("任务记录字段错误：%+v", task)
	}
	if task.Quota != wantQuota {
		t.Fatalf("任务应记录本次扣费额度，实际 %d", task.Quota)
	}

	if quota := userQuotaOf(t, 1); quota != 100000-wantQuota {
		t.Fatalf("应按次扣费 %d，当前额度 %d", wantQuota, quota)
	}
	if logs := countConsumeLogs(t, 1); logs != 1 {
		t.Fatalf("应记 1 条消费流水，实际 %d", logs)
	}
	var consumeLog model.Log
	if err := model.LOG_DB.Where("user_id = ? AND type = ?", 1, model.LogTypeConsume).First(&consumeLog).Error; err != nil {
		t.Fatalf("读取消费流水失败：%v", err)
	}
	if consumeLog.ModelName != "mj_imagine" || consumeLog.Quota != int(wantQuota) {
		t.Fatalf("消费流水内容错误：%+v", consumeLog)
	}
	// 预算计数（B6）也要累加，否则日/月预算熔断对 MJ 消费失效
	if _, monthly, err := model.GetBudgetUsages(model.BudgetSubjectUser, 1, time.Now()); err != nil || monthly != wantQuota {
		t.Fatalf("预算计数应为 %d，实际 %d（err=%v）", wantQuota, monthly, err)
	}
}

// expectedMidjourneyQuota 单次 MJ 任务的预期额度：单位额度 × 货币换算系数。
func expectedMidjourneyQuota() int64 {
	return int64(float64(midjourneyQuotaUnit) * billingratio.RatioScale())
}

// TestSubmitMidjourneyTaskRefundsOnUpstreamFailure 上游拒收：退款、任务记为失败、不记消费流水。
func TestSubmitMidjourneyTaskRefundsOnUpstreamFailure(t *testing.T) {
	initMidjourneyControllerTest(t)
	setMidjourneyTestOptionMap(t, map[string]string{})
	seedMidjourneyUser(t, 1, 100000)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":22,"description":"无可用账号","result":""}`))
	}))
	defer server.Close()
	useFakeMidjourneyUpstream(t, server)

	recorder := callMidjourneyHandler(t, SubmitMidjourneyTask, http.MethodPost, "/api/mj/submit",
		`{"action":"imagine","prompt":"a cat"}`, 1, nil)

	success, message, _ := decodeMidjourneyResponse(t, recorder)
	if success {
		t.Fatal("上游拒收时不应返回成功")
	}
	if !strings.Contains(message, "无可用账号") {
		t.Fatalf("应透出上游原因，实际：%s", message)
	}
	// 退款必须真的回到 DB：这是「已扣费但没图」的资金差异
	if quota := userQuotaOf(t, 1); quota != 100000 {
		t.Fatalf("上游失败应全额退款，当前额度 %d", quota)
	}
	tasks, total, err := model.GetMidjourneyTasksByUserId(1, 0, 0, 10)
	if err != nil || total != 1 {
		t.Fatalf("失败任务也应留痕，实际 %d 条（err=%v）", total, err)
	}
	if tasks[0].Status != model.MidjourneyStatusFailure || !strings.Contains(tasks[0].FailReason, "无可用账号") {
		t.Fatalf("失败原因未记录：%+v", tasks[0])
	}
	if logs := countConsumeLogs(t, 1); logs != 0 {
		t.Fatalf("未受理的任务不应产生消费流水，实际 %d 条", logs)
	}
}

// TestSubmitMidjourneyTaskInsufficientQuota 额度不足：不调上游、不落记录。
func TestSubmitMidjourneyTaskInsufficientQuota(t *testing.T) {
	initMidjourneyControllerTest(t)
	setMidjourneyTestOptionMap(t, map[string]string{})
	seedMidjourneyUser(t, 1, 10)

	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = w.Write([]byte(`{"code":1,"result":"up-1"}`))
	}))
	defer server.Close()
	useFakeMidjourneyUpstream(t, server)

	recorder := callMidjourneyHandler(t, SubmitMidjourneyTask, http.MethodPost, "/api/mj/submit",
		`{"action":"imagine","prompt":"a cat"}`, 1, nil)

	success, message, _ := decodeMidjourneyResponse(t, recorder)
	if success || !strings.Contains(message, "额度不足") {
		t.Fatalf("额度不足应被拒绝，实际 success=%v message=%s", success, message)
	}
	if hit {
		t.Fatal("额度不足时不应调用上游")
	}
	if total := countMidjourneyTasks(t, 1); total != 0 {
		t.Fatalf("额度不足不应留下任务记录，实际 %d 条", total)
	}
	if quota := userQuotaOf(t, 1); quota != 10 {
		t.Fatalf("额度不足时不应扣费，当前额度 %d", quota)
	}
}

// TestSubmitMidjourneyTaskRejectsBadAction 非法动作在本地就被拒绝。
func TestSubmitMidjourneyTaskRejectsBadAction(t *testing.T) {
	initMidjourneyControllerTest(t)
	setMidjourneyTestOptionMap(t, map[string]string{})
	seedMidjourneyUser(t, 1, 100000)

	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = w.Write([]byte(`{"code":1,"result":"up-1"}`))
	}))
	defer server.Close()
	useFakeMidjourneyUpstream(t, server)

	recorder := callMidjourneyHandler(t, SubmitMidjourneyTask, http.MethodPost, "/api/mj/submit",
		`{"action":"video","prompt":"a cat"}`, 1, nil)

	success, message, _ := decodeMidjourneyResponse(t, recorder)
	if success || !strings.Contains(message, "不支持的 action") {
		t.Fatalf("非法动作应被拒绝，实际 success=%v message=%s", success, message)
	}
	if hit || countMidjourneyTasks(t, 1) != 0 {
		t.Fatal("非法动作不应调上游或落库")
	}
}

// TestFetchMidjourneyTaskUpdatesLocalRecord 查询上游成功后要更新本地记录。
func TestFetchMidjourneyTaskUpdatesLocalRecord(t *testing.T) {
	initMidjourneyControllerTest(t)
	setMidjourneyTestOptionMap(t, map[string]string{})
	seedMidjourneyUser(t, 1, 100000)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mj/task/up-1/fetch" {
			t.Errorf("上游查询路径错误：%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"up-1","status":"SUCCESS","progress":"100%","imageUrl":"https://cdn.example.com/a.png"}`))
	}))
	defer server.Close()
	useFakeMidjourneyUpstream(t, server)

	task := &model.MidjourneyTask{
		UserId: 1, Action: "imagine", Prompt: "a cat", TaskId: "up-1",
		Status: model.MidjourneyStatusSubmitted,
	}
	if err := task.Insert(); err != nil {
		t.Fatalf("插入任务失败：%v", err)
	}

	recorder := callMidjourneyHandler(t, FetchMidjourneyTask, http.MethodGet,
		fmt.Sprintf("/api/mj/task/%d/fetch", task.Id), "", 1, gin.Params{{Key: "id", Value: fmt.Sprint(task.Id)}})

	success, message, data := decodeMidjourneyResponse(t, recorder)
	if !success {
		t.Fatalf("查询应成功，实际 message=%s", message)
	}
	if data["image_url"] != "https://cdn.example.com/a.png" {
		t.Fatalf("响应未回传图片地址：%+v", data)
	}

	// 落库校验：状态与图片必须持久化，否则用户刷新页面又回到「进行中」
	stored, err := model.GetMidjourneyTaskById(task.Id, 1)
	if err != nil {
		t.Fatalf("重新读取任务失败：%v", err)
	}
	if stored.Status != model.MidjourneyStatusSuccess || stored.Progress != 100 ||
		stored.ImageUrl != "https://cdn.example.com/a.png" || stored.FinishTime == 0 {
		t.Fatalf("本地记录未按上游结果更新：%+v", stored)
	}

	// 终态后再查不应再打上游（fake 上游会记录调用次数）
	calls := 0
	counting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"id":"up-1","status":"IN_PROGRESS","progress":"10%"}`))
	}))
	defer counting.Close()
	useFakeMidjourneyUpstream(t, counting)
	recorder = callMidjourneyHandler(t, FetchMidjourneyTask, http.MethodGet,
		fmt.Sprintf("/api/mj/task/%d/fetch", task.Id), "", 1, gin.Params{{Key: "id", Value: fmt.Sprint(task.Id)}})
	if success, _, _ := decodeMidjourneyResponse(t, recorder); !success {
		t.Fatal("终态任务查询应直接返回本地记录")
	}
	if calls != 0 {
		t.Fatalf("终态任务不应再打上游，实际调用 %d 次", calls)
	}
}

// TestFetchMidjourneyTaskKeepsStatusOnUpstreamError 上游查询失败不得把任务判成失败。
func TestFetchMidjourneyTaskKeepsStatusOnUpstreamError(t *testing.T) {
	initMidjourneyControllerTest(t)
	setMidjourneyTestOptionMap(t, map[string]string{})
	seedMidjourneyUser(t, 1, 100000)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"description":"上游重启中"}`))
	}))
	defer server.Close()
	useFakeMidjourneyUpstream(t, server)

	task := &model.MidjourneyTask{
		UserId: 1, Action: "imagine", TaskId: "up-1", Status: model.MidjourneyStatusInProgress, Progress: 40,
	}
	if err := task.Insert(); err != nil {
		t.Fatalf("插入任务失败：%v", err)
	}

	recorder := callMidjourneyHandler(t, FetchMidjourneyTask, http.MethodGet,
		fmt.Sprintf("/api/mj/task/%d/fetch", task.Id), "", 1, gin.Params{{Key: "id", Value: fmt.Sprint(task.Id)}})

	success, message, _ := decodeMidjourneyResponse(t, recorder)
	if success || !strings.Contains(message, "查询上游任务失败") {
		t.Fatalf("上游故障应返回失败提示，实际 success=%v message=%s", success, message)
	}
	stored, err := model.GetMidjourneyTaskById(task.Id, 1)
	if err != nil {
		t.Fatalf("重新读取任务失败：%v", err)
	}
	// 网络抖动不是生成失败：状态必须原样保留，否则用户会以为图没了
	if stored.Status != model.MidjourneyStatusInProgress || stored.Progress != 40 {
		t.Fatalf("上游查询失败不应改动本地状态：%+v", stored)
	}
}

// TestFetchMidjourneyTaskRejectsOtherUser 越权：用户 A 查不到用户 B 的任务。
func TestFetchMidjourneyTaskRejectsOtherUser(t *testing.T) {
	initMidjourneyControllerTest(t)
	setMidjourneyTestOptionMap(t, map[string]string{})
	seedMidjourneyUser(t, 1, 100000)
	seedMidjourneyUser(t, 2, 100000)

	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = w.Write([]byte(`{"id":"up-b","status":"SUCCESS","progress":"100%","imageUrl":"https://cdn.example.com/b.png"}`))
	}))
	defer server.Close()
	useFakeMidjourneyUpstream(t, server)

	taskB := &model.MidjourneyTask{
		UserId: 2, Action: "imagine", Prompt: "belongs to 2", TaskId: "up-b",
		Status: model.MidjourneyStatusSubmitted,
	}
	if err := taskB.Insert(); err != nil {
		t.Fatalf("插入任务失败：%v", err)
	}

	// 用户 1 用本地 id 查用户 2 的任务
	recorder := callMidjourneyHandler(t, FetchMidjourneyTask, http.MethodGet,
		fmt.Sprintf("/api/mj/task/%d/fetch", taskB.Id), "", 1, gin.Params{{Key: "id", Value: fmt.Sprint(taskB.Id)}})
	success, message, _ := decodeMidjourneyResponse(t, recorder)
	if success {
		t.Fatal("越权查询应被拒绝")
	}
	if message != "任务不存在" {
		// 统一提示，避免通过错误信息探测他人任务是否存在
		t.Fatalf("越权应返回统一的「任务不存在」，实际：%s", message)
	}

	// 用户 1 用上游任务号查用户 2 的任务（第二条解析路径也必须拦住）
	recorder = callMidjourneyHandler(t, FetchMidjourneyTask, http.MethodGet,
		"/api/mj/task/up-b/fetch", "", 1, gin.Params{{Key: "id", Value: "up-b"}})
	if success, _, _ := decodeMidjourneyResponse(t, recorder); success {
		t.Fatal("按上游任务号越权查询应被拒绝")
	}

	// 越权请求不能触达上游，也不能改动他人记录
	if hit {
		t.Fatal("越权查询不应调用上游")
	}
	stored, err := model.GetMidjourneyTaskById(taskB.Id, 2)
	if err != nil {
		t.Fatalf("重新读取任务失败：%v", err)
	}
	if stored.Status != model.MidjourneyStatusSubmitted || stored.ImageUrl != "" {
		t.Fatalf("他人任务被越权修改：%+v", stored)
	}
}

// TestGetUserMidjourneyTasksOnlyOwnTasks 列表只返回自己的任务，且忽略伪造的 user_id 参数。
func TestGetUserMidjourneyTasksOnlyOwnTasks(t *testing.T) {
	initMidjourneyControllerTest(t)
	setMidjourneyTestOptionMap(t, map[string]string{})
	seedMidjourneyUser(t, 1, 0)
	seedMidjourneyUser(t, 2, 0)

	for _, task := range []*model.MidjourneyTask{
		{UserId: 1, Action: "imagine", Prompt: "u1-a"},
		{UserId: 1, Action: "upscale", Prompt: "u1-b"},
		{UserId: 2, Action: "imagine", Prompt: "u2-a"},
	} {
		if err := task.Insert(); err != nil {
			t.Fatalf("插入任务失败：%v", err)
		}
	}

	// 故意伪造 user_id=2：列表必须只认会话里的用户
	recorder := callMidjourneyHandler(t, GetUserMidjourneyTasks, http.MethodGet, "/api/mj/tasks?p=0&user_id=2", "", 1, nil)
	success, message, data := decodeMidjourneyResponse(t, recorder)
	if !success {
		t.Fatalf("查询应成功，实际 message=%s", message)
	}
	if total, _ := data["total"].(float64); total != 2 {
		t.Fatalf("用户 1 应只有 2 条任务，实际 %+v", data["total"])
	}
	items, ok := data["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("列表条数错误：%+v", data["items"])
	}
	for _, item := range items {
		row, _ := item.(map[string]any)
		if row["user_id"].(float64) != 1 {
			t.Fatalf("越权：列表里出现了他人任务 %+v", row)
		}
	}

	// 未登录（无会话用户）必须被拒绝
	recorder = callMidjourneyHandler(t, GetUserMidjourneyTasks, http.MethodGet, "/api/mj/tasks", "", 0, nil)
	if success, _, _ := decodeMidjourneyResponse(t, recorder); success {
		t.Fatal("未登录不应返回数据")
	}
}

// TestGetAllMidjourneyTasksAdminFilters 管理面查询支持用户/状态过滤。
func TestGetAllMidjourneyTasksAdminFilters(t *testing.T) {
	initMidjourneyControllerTest(t)
	setMidjourneyTestOptionMap(t, map[string]string{})
	seedMidjourneyUser(t, 2, 0)

	for _, task := range []*model.MidjourneyTask{
		{UserId: 1, Action: "imagine"},
		{UserId: 2, Action: "imagine", Status: model.MidjourneyStatusFailure},
	} {
		if err := task.Insert(); err != nil {
			t.Fatalf("插入任务失败：%v", err)
		}
	}

	recorder := callMidjourneyHandler(t, GetAllMidjourneyTasks, http.MethodGet, "/api/mj/admin/tasks?user_id=2", "", 0, nil)
	success, message, data := decodeMidjourneyResponse(t, recorder)
	if !success {
		t.Fatalf("管理面查询应成功，实际 message=%s", message)
	}
	if total, _ := data["total"].(float64); total != 1 {
		t.Fatalf("按用户过滤后应只有 1 条，实际 %+v", data["total"])
	}

	recorder = callMidjourneyHandler(t, GetAllMidjourneyTasks, http.MethodGet,
		fmt.Sprintf("/api/mj/admin/tasks?status=%d&page_size=999", model.MidjourneyStatusFailure), "", 0, nil)
	_, _, data = decodeMidjourneyResponse(t, recorder)
	if total, _ := data["total"].(float64); total != 1 {
		t.Fatalf("按状态过滤后应只有 1 条，实际 %+v", data["total"])
	}
	// page_size 必须被封顶，避免一次拉全表
	if pageSize, _ := data["page_size"].(float64); int(pageSize) != maxMidjourneyPageSize {
		t.Fatalf("page_size 应封顶到 %d，实际 %+v", maxMidjourneyPageSize, data["page_size"])
	}
}
