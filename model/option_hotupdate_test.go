package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/config"
	billingratio "github.com/xiaosu4610/LTZY-API/relay/billing/ratio"
)

// 配置热更新与版本回滚单测（LTZY-API 新增，E9）。
//
// 覆盖面：
//  1. applyOptionToRuntime 把键同步到 config 全局变量（日志开关/主题/SMTP 等）；
//  2. 变更历史写入（操作者、旧值、新值）与分页查询；
//  3. 回滚：值恢复原状，并追加一条标记为回滚的历史；
//  4. 敏感键脱敏：历史接口不回显明文，但回滚能真正恢复原值；
//  5. "需重启生效"清单的内容与自一致性。

// initTestOptionMap 把 OptionMap 换成干净的 map，并在用例结束后还原。
// 为什么必须换：OptionMap 是包级全局，交叉污染会让"旧值"断言随执行顺序漂移。
func initTestOptionMap(t *testing.T) {
	t.Helper()
	config.OptionMapRWMutex.Lock()
	previous := config.OptionMap
	config.OptionMap = make(map[string]string)
	config.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		config.OptionMapRWMutex.Lock()
		config.OptionMap = previous
		config.OptionMapRWMutex.Unlock()
	})
}

// initOptionTestDB 准备配置相关表（options 与 option_histories）。
func initOptionTestDB(t *testing.T) {
	t.Helper()
	initTestDB(t)
	if err := DB.AutoMigrate(&Option{}, &OptionHistory{}); err != nil {
		t.Fatalf("迁移配置相关表失败：%v", err)
	}
}

func optionMapValue(t *testing.T, key string) string {
	t.Helper()
	config.OptionMapRWMutex.RLock()
	defer config.OptionMapRWMutex.RUnlock()
	return config.OptionMap[key]
}

// TestApplyOptionToRuntimeSyncsGlobals 覆盖核心目标：改键 → 全局变量真的变了。
func TestApplyOptionToRuntimeSyncsGlobals(t *testing.T) {
	initTestOptionMap(t)

	// 快照并还原被本用例改动的全局变量，避免影响同包其它用例
	prevLogConsume := config.LogConsumeEnabled
	prevTheme := config.Theme
	prevSMTPServer := config.SMTPServer
	prevSMTPPort := config.SMTPPort
	prevSMTPToken := config.SMTPToken
	prevMemoryCache := config.MemoryCacheEnabled
	prevQuotaPerUnit := config.QuotaPerUnit
	t.Cleanup(func() {
		config.LogConsumeEnabled = prevLogConsume
		config.Theme = prevTheme
		config.SMTPServer = prevSMTPServer
		config.SMTPPort = prevSMTPPort
		config.SMTPToken = prevSMTPToken
		config.MemoryCacheEnabled = prevMemoryCache
		config.QuotaPerUnit = prevQuotaPerUnit
	})

	// 1) 直接调用 applyOptionToRuntime：这是所有热更新的唯一出口
	if err := applyOptionToRuntime("LogConsumeEnabled", "false"); err != nil {
		t.Fatalf("应用日志开关失败：%v", err)
	}
	if config.LogConsumeEnabled {
		t.Fatal("LogConsumeEnabled=false 后全局变量仍为 true（改了不生效）")
	}
	if err := applyOptionToRuntime("Theme", "berry"); err != nil {
		t.Fatalf("应用主题失败：%v", err)
	}
	if config.Theme != "berry" {
		t.Fatalf("主题未同步到全局变量，实际 %q", config.Theme)
	}
	if err := applyOptionToRuntime("SMTPServer", "smtp.example.com"); err != nil {
		t.Fatalf("应用 SMTP 服务器失败：%v", err)
	}
	if config.SMTPServer != "smtp.example.com" {
		t.Fatalf("SMTP 服务器未同步到全局变量，实际 %q", config.SMTPServer)
	}

	// 2) 走 updateOptionMap：OptionMap 与全局变量必须同时更新
	if err := updateOptionMap("QuotaPerUnit", "1234.5"); err != nil {
		t.Fatalf("应用 QuotaPerUnit 失败：%v", err)
	}
	if optionMapValue(t, "QuotaPerUnit") != "1234.5" {
		t.Fatal("OptionMap 未更新 QuotaPerUnit")
	}
	if config.QuotaPerUnit != 1234.5 {
		t.Fatalf("QuotaPerUnit 未同步到全局变量，实际 %v", config.QuotaPerUnit)
	}
	if err := updateOptionMap("SMTPPort", "2525"); err != nil {
		t.Fatalf("应用 SMTPPort 失败：%v", err)
	}
	if config.SMTPPort != 2525 {
		t.Fatalf("SMTPPort 未同步到全局变量，实际 %d", config.SMTPPort)
	}

	// 3) 内存缓存开关（E9 新增键）：没有初始化过 OptionMap 时只改开关本身，
	//    不触发渠道缓存重建（重建依赖数据库，由 optionMapInitialized 把关）
	if err := updateOptionMap("MemoryCacheEnabled", "true"); err != nil {
		t.Fatalf("应用内存缓存开关失败：%v", err)
	}
	if !config.MemoryCacheEnabled {
		t.Fatal("MemoryCacheEnabled 未同步到全局变量")
	}
	if optionMapValue(t, "MemoryCacheEnabled") != "true" {
		t.Fatal("OptionMap 未更新 MemoryCacheEnabled")
	}

	// 4) 校验失败必须整体不生效：端口写成非数字时，OptionMap 与全局变量都要保持原值
	if err := updateOptionMap("SMTPPort", "not-a-port"); err == nil {
		t.Fatal("非法端口应报错")
	}
	if config.SMTPPort != 2525 {
		t.Fatalf("校验失败却改了全局变量，实际 %d", config.SMTPPort)
	}
	if optionMapValue(t, "SMTPPort") != "2525" {
		t.Fatalf("校验失败却污染了 OptionMap，实际 %q", optionMapValue(t, "SMTPPort"))
	}
}

// TestOptionHistoryRecordAndPagination 覆盖历史写入与分页查询。
func TestOptionHistoryRecordAndPagination(t *testing.T) {
	initOptionTestDB(t)
	initTestOptionMap(t)
	prevTheme := config.Theme
	t.Cleanup(func() { config.Theme = prevTheme })

	config.OptionMapRWMutex.Lock()
	config.OptionMap["Theme"] = "default"
	config.OptionMapRWMutex.Unlock()

	if _, err := UpdateOptionWithOperator("Theme", "berry", 7, "root", "手动切换主题"); err != nil {
		t.Fatalf("第一次更新失败：%v", err)
	}
	if _, err := UpdateOptionWithOperator("Theme", "air", 8, "alice", ""); err != nil {
		t.Fatalf("第二次更新失败：%v", err)
	}

	// 落库必须与内存一致
	var stored Option
	if err := DB.Where(&Option{Key: "Theme"}).First(&stored).Error; err != nil {
		t.Fatalf("读取 options 表失败：%v", err)
	}
	if stored.Value != "air" {
		t.Fatalf("options 表值错误，实际 %q", stored.Value)
	}

	items, total, err := GetOptionHistories(1, 10, "")
	if err != nil {
		t.Fatalf("查询历史失败：%v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("历史条数应为 2，实际 total=%d len=%d", total, len(items))
	}
	newest := items[0]
	if newest.Key != "Theme" || newest.OldValue != "berry" || newest.NewValue != "air" {
		t.Fatalf("最新历史内容错误：%+v", newest)
	}
	if newest.OperatorId != 8 || newest.OperatorName != "alice" {
		t.Fatalf("最新历史操作者错误：%+v", newest)
	}
	older := items[1]
	if older.OldValue != "default" || older.NewValue != "berry" || older.Remark != "手动切换主题" {
		t.Fatalf("较早历史内容错误：%+v", older)
	}
	if older.OperatorId != 7 || older.OperatorName != "root" {
		t.Fatalf("较早历史操作者错误：%+v", older)
	}

	// 分页：每页 1 条，第一页是最新的
	page1, total, err := GetOptionHistories(1, 1, "")
	if err != nil || total != 2 || len(page1) != 1 {
		t.Fatalf("第一页查询失败：err=%v total=%d len=%d", err, total, len(page1))
	}
	if page1[0].NewValue != "air" {
		t.Fatalf("第一页应为最新记录，实际 %+v", page1[0])
	}
	page2, _, err := GetOptionHistories(2, 1, "")
	if err != nil || len(page2) != 1 || page2[0].NewValue != "berry" {
		t.Fatalf("第二页查询失败：err=%v items=%+v", err, page2)
	}

	// 按 key 过滤
	filtered, filteredTotal, err := GetOptionHistories(1, 10, "Theme")
	if err != nil || filteredTotal != 2 || len(filtered) != 2 {
		t.Fatalf("按 key 过滤失败：err=%v total=%d len=%d", err, filteredTotal, len(filtered))
	}
	other, otherTotal, err := GetOptionHistories(1, 10, "LogConsumeEnabled")
	if err != nil || otherTotal != 0 || len(other) != 0 {
		t.Fatalf("无关 key 不应有历史：err=%v total=%d len=%d", err, otherTotal, len(other))
	}

	// 校验失败的写入不得落库、也不得留下历史（改造前脏值会进库并在重启后被加载）
	if _, err := UpdateOptionWithOperator("TieredRatio", "not-a-json", 1, "root", ""); err == nil {
		t.Fatal("非法 TieredRatio 应报错")
	}
	var dirty Option
	if err := DB.Where(&Option{Key: "TieredRatio"}).First(&dirty).Error; err == nil {
		t.Fatalf("校验失败的配置不应落库，实际存了 %q", dirty.Value)
	}
	_, totalAfter, _ := GetOptionHistories(1, 10, "TieredRatio")
	if totalAfter != 0 {
		t.Fatalf("校验失败的配置不应留下历史，实际 %d 条", totalAfter)
	}
}

// TestRollbackOption 覆盖回滚：值恢复原状 + 追加一条回滚历史。
func TestRollbackOption(t *testing.T) {
	initOptionTestDB(t)
	initTestOptionMap(t)
	prevTheme := config.Theme
	t.Cleanup(func() { config.Theme = prevTheme })

	config.OptionMapRWMutex.Lock()
	config.OptionMap["Theme"] = "default"
	config.OptionMapRWMutex.Unlock()

	if _, err := UpdateOptionWithOperator("Theme", "berry", 7, "root", ""); err != nil {
		t.Fatalf("第一次更新失败：%v", err)
	}
	second, err := UpdateOptionWithOperator("Theme", "air", 8, "alice", "")
	if err != nil {
		t.Fatalf("第二次更新失败：%v", err)
	}

	// 回滚第二条（air ← berry），恢复的应该是"变更前"的 berry
	rollbackHistory, err := RollbackOptionById(second.Id, 9, "operator")
	if err != nil {
		t.Fatalf("回滚失败：%v", err)
	}
	if config.Theme != "berry" {
		t.Fatalf("回滚后全局变量应为 berry，实际 %q", config.Theme)
	}
	if optionMapValue(t, "Theme") != "berry" {
		t.Fatalf("回滚后 OptionMap 应为 berry，实际 %q", optionMapValue(t, "Theme"))
	}
	var stored Option
	if err := DB.Where(&Option{Key: "Theme"}).First(&stored).Error; err != nil {
		t.Fatalf("读取 options 表失败：%v", err)
	}
	if stored.Value != "berry" {
		t.Fatalf("回滚后 options 表应为 berry，实际 %q", stored.Value)
	}

	// 回滚本身也要留痕，且标明是一次回滚
	if rollbackHistory == nil || !rollbackHistory.IsRollback {
		t.Fatalf("回滚应产生一条 is_rollback=true 的历史：%+v", rollbackHistory)
	}
	if rollbackHistory.OldValue != "air" || rollbackHistory.NewValue != "berry" {
		t.Fatalf("回滚历史内容错误：%+v", rollbackHistory)
	}
	if !strings.Contains(rollbackHistory.Remark, optionRollbackRemarkPrefix) {
		t.Fatalf("回滚历史备注应标明回滚，实际 %q", rollbackHistory.Remark)
	}
	if rollbackHistory.OperatorId != 9 || rollbackHistory.OperatorName != "operator" {
		t.Fatalf("回滚历史操作者错误：%+v", rollbackHistory)
	}
	_, total, _ := GetOptionHistories(1, 10, "Theme")
	if total != 3 {
		t.Fatalf("两条更新 + 一条回滚应共 3 条历史，实际 %d", total)
	}

	// 不存在的记录要明确报错，而不是静默成功
	if _, err := RollbackOptionById(999999, 1, "root"); err == nil {
		t.Fatal("回滚不存在的记录应报错")
	}

	// "需重启生效"的键不参与回滚：避免回滚接口返回成功却什么也没改变
	restartOnly := &OptionHistory{Key: "CorsAllowOrigins", OldValue: "", NewValue: "https://a.com", OperatorId: 1, OperatorName: "root"}
	RecordOptionHistory(restartOnly)
	if restartOnly.Id == 0 {
		t.Fatal("历史记录写入失败（Id 为 0）")
	}
	if _, err := RollbackOptionById(restartOnly.Id, 1, "root"); err == nil {
		t.Fatal("需重启生效的配置不应支持在线回滚")
	}
}

// TestOptionHistorySensitiveMasking 覆盖敏感键的脱敏与"回滚用原文"的取舍。
func TestOptionHistorySensitiveMasking(t *testing.T) {
	initOptionTestDB(t)
	initTestOptionMap(t)
	prevToken := config.SMTPToken
	prevTheme := config.Theme
	t.Cleanup(func() {
		config.SMTPToken = prevToken
		config.Theme = prevTheme
	})

	const rawSecret = "sk-abcdefghijklmnop"
	const changedSecret = "sk-zzzzzzzzzzzzzzzz"

	config.OptionMapRWMutex.Lock()
	config.OptionMap["SMTPToken"] = rawSecret
	config.OptionMap["Theme"] = "default"
	config.OptionMapRWMutex.Unlock()

	// 非敏感键：原样记录，不需要额外存原文
	plain, err := UpdateOptionWithOperator("Theme", "berry", 1, "root", "")
	if err != nil {
		t.Fatalf("更新主题失败：%v", err)
	}
	if plain.IsSensitive || plain.NewValue != "berry" || plain.NewValueRaw != "" {
		t.Fatalf("非敏感键不应脱敏/存原文：%+v", plain)
	}

	// 敏感键：展示值脱敏、原文另存
	sensitive, err := UpdateOptionWithOperator("SMTPToken", changedSecret, 1, "root", "")
	if err != nil {
		t.Fatalf("更新 SMTPToken 失败：%v", err)
	}
	if !sensitive.IsSensitive {
		t.Fatalf("SMTPToken 应被识别为敏感键：%+v", sensitive)
	}
	if sensitive.NewValue == changedSecret || !strings.Contains(sensitive.NewValue, "***") {
		t.Fatalf("敏感键展示值应脱敏，实际 %q", sensitive.NewValue)
	}
	if sensitive.OldValue == rawSecret || !strings.Contains(sensitive.OldValue, "***") {
		t.Fatalf("敏感键旧值应脱敏，实际 %q", sensitive.OldValue)
	}
	if sensitive.NewValueRaw != changedSecret || sensitive.OldValueRaw != rawSecret {
		t.Fatalf("敏感键原文未正确留存：%+v", sensitive)
	}

	// 接口回显（JSON 序列化）绝不能带出原文
	body, err := json.Marshal(sensitive)
	if err != nil {
		t.Fatalf("序列化历史失败：%v", err)
	}
	if strings.Contains(string(body), changedSecret) || strings.Contains(string(body), rawSecret) {
		t.Fatalf("历史 JSON 泄漏了密钥原文：%s", string(body))
	}
	items, _, err := GetOptionHistories(1, 10, "SMTPToken")
	if err != nil || len(items) != 1 {
		t.Fatalf("查询 SMTPToken 历史失败：err=%v len=%d", err, len(items))
	}
	listed, _ := json.Marshal(items[0])
	if strings.Contains(string(listed), changedSecret) {
		t.Fatalf("历史列表泄漏了密钥原文：%s", string(listed))
	}

	// 回滚必须恢复原文，而不是把密钥改成脱敏串
	if _, err := RollbackOptionById(sensitive.Id, 2, "root"); err != nil {
		t.Fatalf("回滚敏感键失败：%v", err)
	}
	if config.SMTPToken != rawSecret {
		t.Fatalf("回滚后应恢复原文，实际 %q", config.SMTPToken)
	}
	if optionMapValue(t, "SMTPToken") != rawSecret {
		t.Fatalf("回滚后 OptionMap 应恢复原文，实际 %q", optionMapValue(t, "SMTPToken"))
	}
	var stored Option
	if err := DB.Where(&Option{Key: "SMTPToken"}).First(&stored).Error; err != nil {
		t.Fatalf("读取 options 表失败：%v", err)
	}
	if stored.Value != rawSecret {
		t.Fatalf("回滚后库中应为原文，实际 %q", stored.Value)
	}

	// 与 GetOptions 的隐藏口径必须一致：能被隐藏的键，历史里也必须被打码
	for _, key := range []string{"SMTPToken", "GitHubClientSecret", "TurnstileSecretKey", "MidjourneyApiKey", "DingTalkSecret"} {
		if !IsSensitiveOptionKey(key) {
			t.Fatalf("%s 应被判定为敏感键", key)
		}
	}
	for _, key := range []string{"Theme", "SystemName", "ModelRatio", "SMTPPort"} {
		if IsSensitiveOptionKey(key) {
			t.Fatalf("%s 不应被判定为敏感键", key)
		}
	}
	if common.MaskSecret(rawSecret) == rawSecret {
		t.Fatal("MaskSecret 未生效，脱敏实现可能被改动")
	}
}

// TestRatioOptionKeepsTableOnInvalidJSON 覆盖"整表替换型"配置的保护：
// ratio 包的 Update*ByJSONString 是先清空再解析，非法 JSON 会把计费表清空，
// 因此必须由 applyRatioOption 把旧表写回。
func TestRatioOptionKeepsTableOnInvalidJSON(t *testing.T) {
	initTestOptionMap(t)

	original := billingratio.ModelRatio2JSONString()
	t.Cleanup(func() {
		// 把被本用例改动的全局倍率表还原，避免影响同包其它用例
		_ = billingratio.UpdateModelRatioByJSONString(original)
	})

	if err := updateOptionMap("ModelRatio", `{"gpt-4o":2.5}`); err != nil {
		t.Fatalf("写入合法倍率失败：%v", err)
	}
	if got := billingratio.GetModelRatio("gpt-4o", 0); got != 2.5 {
		t.Fatalf("合法倍率未生效，实际 %v", got)
	}
	before := billingratio.ModelRatio2JSONString()

	// 非法 JSON：必须报错，且倍率表保持原样（既不能被清空，也不能被部分覆盖）
	if err := updateOptionMap("ModelRatio", `{"gpt-4o":`); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	if after := billingratio.ModelRatio2JSONString(); after != before {
		t.Fatalf("非法配置污染了倍率表：before=%s after=%s", before, after)
	}
	if got := billingratio.GetModelRatio("gpt-4o", 0); got != 2.5 {
		t.Fatalf("非法配置后倍率应保持不变，实际 %v", got)
	}
	if optionMapValue(t, "ModelRatio") != `{"gpt-4o":2.5}` {
		t.Fatalf("非法配置污染了 OptionMap，实际 %q", optionMapValue(t, "ModelRatio"))
	}
}

// TestRestartRequiredOptions 覆盖"需重启生效"清单的内容与自一致性。
func TestRestartRequiredOptions(t *testing.T) {
	items := RestartRequiredOptions()
	if len(items) == 0 {
		t.Fatal("需重启生效清单不应为空（否则管理员无从得知哪些配置不会热生效）")
	}

	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Key) == "" || strings.TrimSpace(item.Reason) == "" {
			t.Fatalf("清单项字段不完整：%+v", item)
		}
		// EnvVar 允许为空（表示代码内固定值），非空时必须是形如 XXX_YYY 的环境变量名
		if item.EnvVar != "" && item.EnvVar != strings.ToUpper(item.EnvVar) {
			t.Fatalf("环境变量名应为大写下划线风格：%+v", item)
		}
		if strings.Contains(item.Key, " ") {
			t.Fatalf("清单键名不应含空格：%+v", item)
		}
		if seen[item.Key] {
			t.Fatalf("清单出现重复键：%s", item.Key)
		}
		seen[item.Key] = true
		if !IsRestartRequiredOption(item.Key) {
			t.Fatalf("IsRestartRequiredOption 未识别清单中的键：%s", item.Key)
		}
	}

	// 关键条目必须在清单里：
	//   - 只认环境变量/代码的启动期配置；
	//   - Theme 是"值会热同步、界面仍要重启"的另一类（ValueApplied=true），
	//     因为服务端静态目录在 router/web.go 装配时就已经定死。
	if !seen["Theme"] {
		t.Fatal("清单应包含 Theme：值能热同步，但界面切换仍要重启")
	}
	for _, item := range items {
		if item.Key == "Theme" {
			if !item.ValueApplied {
				t.Fatalf("Theme 的值是能热同步的，ValueApplied 应为 true：%+v", item)
			}
			if !strings.Contains(item.Reason, "重启") {
				t.Fatalf("Theme 的说明应点明需要重启：%q", item.Reason)
			}
		} else if item.ValueApplied {
			t.Fatalf("当前只有 Theme 属于「值已同步但仍需重启」这一类，其余应为完全无效：%+v", item)
		}
	}
	for _, key := range []string{"BatchUpdateEnabled", "SyncFrequency", "CorsAllowOrigins", "RelayProxy"} {
		if !seen[key] {
			t.Fatalf("清单缺少 %s", key)
		}
	}

	// 回滚准入用另一套判定：只认环境变量的键禁止回滚，但 Theme 允许（值是能同步的）
	if IsEnvOnlyOption("Theme") {
		t.Fatal("Theme 的值可热同步，不应被判定为‘只认环境变量’")
	}
	if !IsEnvOnlyOption("CorsAllowOrigins") || !IsEnvOnlyOption("BatchUpdateEnabled") {
		t.Fatal("只认环境变量的键应被判定为 IsEnvOnlyOption")
	}

	// 已实现热更新的键绝不能出现在清单里（否则前端会给出错误的"需重启"提示）
	for _, key := range []string{"LogConsumeEnabled", "MemoryCacheEnabled", "SMTPServer", "SMTPPort", "QuotaPerUnit"} {
		if seen[key] {
			t.Fatalf("%s 已支持热更新，不应出现在需重启清单里", key)
		}
		if IsRestartRequiredOption(key) {
			t.Fatalf("IsRestartRequiredOption 误判 %s 为需重启", key)
		}
	}

	// 返回的是副本：调用方改动不应污染包级清单
	items[0].Key = "mutated"
	if RestartRequiredOptions()[0].Key == "mutated" {
		t.Fatal("RestartRequiredOptions 应返回副本")
	}
}
