package model

import "testing"

// initMidjourneyTestDB 复用 model 包的临时库，额外迁移 MJ 任务表（A4）。
func initMidjourneyTestDB(t *testing.T) {
	t.Helper()
	initTestDB(t)
	if err := DB.AutoMigrate(&MidjourneyTask{}); err != nil {
		t.Fatalf("迁移 Midjourney 任务表失败：%v", err)
	}
}

// TestMidjourneyTaskStatusFlow 覆盖「提交 → 进行中 → 成功」的正常流转与落库结果（A4）。
func TestMidjourneyTaskStatusFlow(t *testing.T) {
	initMidjourneyTestDB(t)

	task := &MidjourneyTask{UserId: 7, Action: "imagine", Prompt: "a cat", Quota: 1000}
	if err := task.Insert(); err != nil {
		t.Fatalf("插入任务失败：%v", err)
	}
	if task.Id == 0 || task.SubmitTime == 0 {
		t.Fatalf("Insert 应回填 id 与提交时间：%+v", task)
	}
	if task.Status != MidjourneyStatusPending {
		t.Fatalf("新任务应为待提交，实际 %d", task.Status)
	}

	// 待提交 → 已提交（上游返回任务号）
	task.TaskId = "upstream-1"
	if !task.ApplyUpstreamStatus(MidjourneyStatusSubmitted, 0, "", "") {
		t.Fatal("待提交 → 已提交 应被允许")
	}
	if err := task.Update(); err != nil {
		t.Fatalf("更新任务失败：%v", err)
	}

	// 已提交 → 进行中
	if !task.ApplyUpstreamStatus(MidjourneyStatusInProgress, 30, "", "") {
		t.Fatal("已提交 → 进行中 应被允许")
	}
	// 上游返回旧快照导致进度回退时，进度不应倒退
	if !task.ApplyUpstreamStatus(MidjourneyStatusInProgress, 10, "", "") {
		t.Fatal("同状态重复上报应幂等允许")
	}
	if task.Progress != 30 {
		t.Fatalf("进度只应增不减，实际 %d", task.Progress)
	}

	// 进行中 → 成功
	if !task.ApplyUpstreamStatus(MidjourneyStatusSuccess, 99, "https://example.com/a.png", "") {
		t.Fatal("进行中 → 成功 应被允许")
	}
	if task.Progress != 100 || task.ImageUrl != "https://example.com/a.png" || task.FinishTime == 0 {
		t.Fatalf("成功态字段不完整：%+v", task)
	}
	if err := task.Update(); err != nil {
		t.Fatalf("更新任务失败：%v", err)
	}

	// 终态不可逆：上游过期快照（IN_PROGRESS / FAILURE）都不得改变已成功的任务
	if task.ApplyUpstreamStatus(MidjourneyStatusInProgress, 40, "", "") {
		t.Fatal("成功是终态，不应回退到进行中")
	}
	if task.ApplyUpstreamStatus(MidjourneyStatusFailure, 0, "", "上游报错") {
		t.Fatal("成功是终态，不应被改成失败")
	}

	// 读回校验：状态机改动必须真的落库（否则重启后任务状态全丢）
	stored, err := GetMidjourneyTaskByTaskId("upstream-1")
	if err != nil {
		t.Fatalf("按上游任务号查询失败：%v", err)
	}
	if stored.Status != MidjourneyStatusSuccess || stored.Progress != 100 ||
		stored.ImageUrl != "https://example.com/a.png" || stored.FinishTime == 0 {
		t.Fatalf("落库结果与内存状态不一致：%+v", stored)
	}
	if stored.UserId != 7 || stored.Action != "imagine" || stored.Quota != 1000 {
		t.Fatalf("不可变字段被写坏：%+v", stored)
	}
}

// TestMidjourneyTaskFailureFlow 覆盖失败分支与终态保护（A4）。
func TestMidjourneyTaskFailureFlow(t *testing.T) {
	initMidjourneyTestDB(t)

	task := &MidjourneyTask{UserId: 8, Action: "upscale", CustomId: "MJ::JOB::upsample::1::abc"}
	if err := task.Insert(); err != nil {
		t.Fatalf("插入任务失败：%v", err)
	}

	// 待提交 → 失败：上游拒收（例如无可用账号）时本地要留下可追查的失败记录
	if err := MarkMidjourneyTaskFailed(task, "提交上游失败：无可用账号"); err != nil {
		t.Fatalf("标记失败失败：%v", err)
	}
	if task.Status != MidjourneyStatusFailure || task.FailReason == "" || task.FinishTime == 0 {
		t.Fatalf("失败态字段不完整：%+v", task)
	}

	// 失败是终态：不允许再流转到成功（否则失败的图会「凭空出现」）
	if task.ApplyUpstreamStatus(MidjourneyStatusSuccess, 100, "https://example.com/b.png", "") {
		t.Fatal("失败是终态，不应再流转")
	}
	// 终态任务重复标记失败应被拒绝，避免覆盖首次的失败原因
	if err := MarkMidjourneyTaskFailed(task, "第二次失败"); err == nil {
		t.Fatal("终态任务不应被重复标记失败")
	}

	// 进行中 → 失败
	another := &MidjourneyTask{UserId: 8, Action: "imagine", TaskId: "upstream-2", Status: MidjourneyStatusInProgress}
	if err := another.Insert(); err != nil {
		t.Fatalf("插入任务失败：%v", err)
	}
	if !another.ApplyUpstreamStatus(MidjourneyStatusFailure, 60, "", "任务被取消") {
		t.Fatal("进行中 → 失败 应被允许")
	}
	if another.FailReason != "任务被取消" || another.FinishTime == 0 {
		t.Fatalf("失败原因/完成时间未写入：%+v", another)
	}
	// 失败时已有进度保留（前端展示「失败于 60%」）
	if another.Progress != 60 {
		t.Fatalf("失败时不应清零进度，实际 %d", another.Progress)
	}
}

// TestCanMidjourneyTransition 状态机规则表（A4）。
func TestCanMidjourneyTransition(t *testing.T) {
	cases := []struct {
		from  int
		to    int
		allow bool
		why   string
	}{
		{MidjourneyStatusPending, MidjourneyStatusSubmitted, true, "拿到上游任务号"},
		{MidjourneyStatusPending, MidjourneyStatusFailure, true, "上游拒收"},
		{MidjourneyStatusSubmitted, MidjourneyStatusInProgress, true, "开始生成"},
		{MidjourneyStatusSubmitted, MidjourneyStatusSuccess, true, "小任务直接完成"},
		{MidjourneyStatusInProgress, MidjourneyStatusSuccess, true, "生成完成"},
		{MidjourneyStatusInProgress, MidjourneyStatusFailure, true, "生成失败"},
		{MidjourneyStatusSubmitted, MidjourneyStatusSubmitted, true, "轮询幂等"},
		{MidjourneyStatusInProgress, MidjourneyStatusSubmitted, false, "状态不允许倒退"},
		{MidjourneyStatusSuccess, MidjourneyStatusInProgress, false, "终态不可逆"},
		{MidjourneyStatusSuccess, MidjourneyStatusFailure, false, "终态不可逆"},
		{MidjourneyStatusFailure, MidjourneyStatusSuccess, false, "终态不可逆"},
	}
	for _, c := range cases {
		if got := CanMidjourneyTransition(c.from, c.to); got != c.allow {
			t.Errorf("%d → %d 期望 %v（%s），实际 %v", c.from, c.to, c.allow, c.why, got)
		}
	}
	if !IsMidjourneyTaskTerminal(MidjourneyStatusSuccess) || !IsMidjourneyTaskTerminal(MidjourneyStatusFailure) {
		t.Fatal("成功与失败都应为终态")
	}
	if IsMidjourneyTaskTerminal(MidjourneyStatusInProgress) {
		t.Fatal("进行中不是终态")
	}
}

// TestGetMidjourneyTasksByUserIdIsolation 覆盖分页与「只能查到自己任务」的隔离（A4）。
func TestGetMidjourneyTasksByUserIdIsolation(t *testing.T) {
	initMidjourneyTestDB(t)

	seed := []*MidjourneyTask{
		{UserId: 1, Action: "imagine", Prompt: "u1-a"},
		{UserId: 1, Action: "imagine", Prompt: "u1-b", Status: MidjourneyStatusSuccess},
		{UserId: 1, Action: "upscale", Prompt: "u1-c"},
		{UserId: 2, Action: "imagine", Prompt: "u2-a"},
		{UserId: 2, Action: "imagine", Prompt: "u2-b"},
	}
	for _, task := range seed {
		if err := task.Insert(); err != nil {
			t.Fatalf("插入任务失败：%v", err)
		}
	}

	// 用户 1：总数为 3，第一页只返回 2 条，且全部属于自己
	page0, total, err := GetMidjourneyTasksByUserId(1, 0, 0, 2)
	if err != nil {
		t.Fatalf("查询用户任务失败：%v", err)
	}
	if total != 3 {
		t.Fatalf("用户 1 任务总数应为 3，实际 %d", total)
	}
	if len(page0) != 2 {
		t.Fatalf("第一页应返回 2 条，实际 %d", len(page0))
	}
	for _, task := range page0 {
		if task.UserId != 1 {
			t.Fatalf("越权：查到了用户 %d 的任务 %d", task.UserId, task.Id)
		}
	}
	// 按 id 倒序：第二页应只剩最早的那条
	page1, _, err := GetMidjourneyTasksByUserId(1, 0, 1, 2)
	if err != nil {
		t.Fatalf("查询用户任务失败：%v", err)
	}
	if len(page1) != 1 || page1[0].Id != seed[0].Id {
		t.Fatalf("第二页应只剩最早的任务，实际 %+v", page1)
	}

	// 状态过滤：用户 1 只有 1 条成功任务
	successOnly, successTotal, err := GetMidjourneyTasksByUserId(1, MidjourneyStatusSuccess, 0, 10)
	if err != nil {
		t.Fatalf("按状态查询失败：%v", err)
	}
	if successTotal != 1 || len(successOnly) != 1 || successOnly[0].Prompt != "u1-b" {
		t.Fatalf("状态过滤结果错误：total=%d items=%+v", successTotal, successOnly)
	}

	// 用户 2 看不到用户 1 的任务
	_, total2, err := GetMidjourneyTasksByUserId(2, 0, 0, 10)
	if err != nil {
		t.Fatalf("查询用户任务失败：%v", err)
	}
	if total2 != 2 {
		t.Fatalf("用户 2 任务总数应为 2，实际 %d", total2)
	}

	// 非法 userId 必须显式报错，而不是「查不到但成功」——
	// 后者会让上层误以为该用户没有任务，掩盖调用方的参数错误。
	if _, _, err := GetMidjourneyTasksByUserId(0, 0, 0, 10); err == nil {
		t.Fatal("userId 为 0 时应报错，避免退化成全表查询")
	}
}

// TestGetMidjourneyTaskByIdRejectsOtherUser 按 id 取任务必须带归属校验（A4 越权防护）。
func TestGetMidjourneyTaskByIdRejectsOtherUser(t *testing.T) {
	initMidjourneyTestDB(t)

	task := &MidjourneyTask{UserId: 11, Action: "imagine", Prompt: "belongs to 11"}
	if err := task.Insert(); err != nil {
		t.Fatalf("插入任务失败：%v", err)
	}

	// 本人可查
	if _, err := GetMidjourneyTaskById(task.Id, 11); err != nil {
		t.Fatalf("本人应能查到自己的任务：%v", err)
	}
	// 他人查不到（返回 ErrRecordNotFound，由上层统一转成 404）
	if _, err := GetMidjourneyTaskById(task.Id, 12); err == nil {
		t.Fatal("越权：用户 12 查到了用户 11 的任务")
	}
	if _, err := GetMidjourneyTaskById(task.Id, 0); err == nil {
		t.Fatal("userId 为 0 时应报错")
	}
	if _, err := GetMidjourneyTaskById(0, 11); err == nil {
		t.Fatal("id 为 0 时应报错")
	}
}

// TestGetAllMidjourneyTasksFilters 管理面查询支持按用户与状态过滤（A4）。
func TestGetAllMidjourneyTasksFilters(t *testing.T) {
	initMidjourneyTestDB(t)

	for _, task := range []*MidjourneyTask{
		{UserId: 1, Action: "imagine"},
		{UserId: 2, Action: "imagine", Status: MidjourneyStatusFailure},
		{UserId: 2, Action: "imagine", Status: MidjourneyStatusFailure},
	} {
		if err := task.Insert(); err != nil {
			t.Fatalf("插入任务失败：%v", err)
		}
	}

	_, total, err := GetAllMidjourneyTasks(0, 0, 0, 10)
	if err != nil || total != 3 {
		t.Fatalf("全量查询应为 3 条，实际 %d（err=%v）", total, err)
	}
	_, userTotal, err := GetAllMidjourneyTasks(2, 0, 0, 10)
	if err != nil || userTotal != 2 {
		t.Fatalf("按用户过滤应为 2 条，实际 %d（err=%v）", userTotal, err)
	}
	_, failTotal, err := GetAllMidjourneyTasks(0, MidjourneyStatusFailure, 0, 10)
	if err != nil || failTotal != 2 {
		t.Fatalf("按状态过滤应为 2 条，实际 %d（err=%v）", failTotal, err)
	}
}
