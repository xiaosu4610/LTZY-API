package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/model"
)

// 配置历史/回滚/需重启清单三个接口的契约测试（LTZY-API 新增，E9）。
// 直接调用 handler（不挂鉴权中间件）：鉴权由 RootAuth 分组保证，这里验的是
// 请求解析与响应形状，以及"密钥不回显"这条硬约束。

// initOptionControllerTest 准备临时库与干净的 OptionMap。
func initOptionControllerTest(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.SQLitePath = filepath.Join(t.TempDir(), "option-controller-test.db")
	common.UsingSQLite = true
	common.RedisEnabled = false

	db, err := gorm.Open(sqlite.Open(common.SQLitePath), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败：%v", err)
	}
	model.DB = db
	model.LOG_DB = db
	if err := db.AutoMigrate(&model.Option{}, &model.OptionHistory{}); err != nil {
		t.Fatalf("迁移配置相关表失败：%v", err)
	}

	config.OptionMapRWMutex.Lock()
	previousMap := config.OptionMap
	config.OptionMap = map[string]string{
		"Theme":     "default",
		"SMTPToken": "sk-originalsecret",
	}
	config.OptionMapRWMutex.Unlock()

	previousToken := config.SMTPToken
	previousTheme := config.Theme
	t.Cleanup(func() {
		config.OptionMapRWMutex.Lock()
		config.OptionMap = previousMap
		config.OptionMapRWMutex.Unlock()
		config.SMTPToken = previousToken
		config.Theme = previousTheme
		// Windows 下不关连接会导致 t.TempDir 清理失败（文件被占用）
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

func newOptionTestEngine() *gin.Engine {
	engine := gin.New()
	engine.GET("/api/option/history", GetOptionHistory)
	engine.POST("/api/option/rollback", RollbackOption)
	engine.GET("/api/option/restart_required", GetRestartRequiredOptions)
	engine.PUT("/api/option/", UpdateOption)
	return engine
}

func doOptionRequest(t *testing.T, engine *gin.Engine, method string, path string, body string) map[string]any {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s %s 期望 200，实际 %d，body=%s", method, path, recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("响应不是合法 JSON：%s", recorder.Body.String())
	}
	return payload
}

// TestGetRestartRequiredOptionsEndpoint 「需重启生效」清单被正确返回。
func TestGetRestartRequiredOptionsEndpoint(t *testing.T) {
	initOptionControllerTest(t)
	engine := newOptionTestEngine()

	payload := doOptionRequest(t, engine, http.MethodGet, "/api/option/restart_required", "")
	if payload["success"] != true {
		t.Fatalf("接口应返回 success=true：%+v", payload)
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 data：%+v", payload)
	}
	items, ok := data["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("清单应非空：%+v", data)
	}
	if int(data["count"].(float64)) != len(items) {
		t.Fatalf("count 与 items 长度不一致：%+v", data)
	}
	found := false
	for _, raw := range items {
		item := raw.(map[string]any)
		// env_var 允许为空（表示该值在代码内固定），key 与 reason 必须有
		for _, field := range []string{"key", "reason"} {
			if value, _ := item[field].(string); strings.TrimSpace(value) == "" {
				t.Fatalf("清单项 %v 的 %s 为空", item, field)
			}
		}
		if item["key"] == "BatchUpdateEnabled" {
			found = true
		}
	}
	if !found {
		t.Fatalf("清单应包含 BatchUpdateEnabled：%+v", items)
	}
}

// TestOptionHistoryAndRollbackEndpoints 历史查询 → 回滚 → 再查询的完整链路。
func TestOptionHistoryAndRollbackEndpoints(t *testing.T) {
	initOptionControllerTest(t)
	engine := newOptionTestEngine()

	const newSecret = "sk-brandnewsecret1234"
	// 1) 通过真实接口改一个敏感键
	payload := doOptionRequest(t, engine, http.MethodPut, "/api/option/",
		`{"key":"SMTPToken","value":"`+newSecret+`"}`)
	if payload["success"] != true {
		t.Fatalf("更新 SMTPToken 应成功：%+v", payload)
	}
	if config.SMTPToken != newSecret {
		t.Fatalf("更新后全局变量应为新值，实际 %q", config.SMTPToken)
	}

	// 2) 历史接口：敏感值必须脱敏，且绝不回显原文
	historyResp := doOptionRequest(t, engine, http.MethodGet, "/api/option/history?p=1&page_size=10", "")
	if historyResp["success"] != true {
		t.Fatalf("查询历史应成功：%+v", historyResp)
	}
	serialized, _ := json.Marshal(historyResp)
	if strings.Contains(string(serialized), newSecret) || strings.Contains(string(serialized), "sk-originalsecret") {
		t.Fatalf("历史接口泄漏了密钥原文：%s", string(serialized))
	}
	historyData := historyResp["data"].(map[string]any)
	if int(historyData["total"].(float64)) != 1 {
		t.Fatalf("应有 1 条历史：%+v", historyData)
	}
	first := historyData["items"].([]any)[0].(map[string]any)
	if first["is_sensitive"] != true {
		t.Fatalf("敏感键历史应标记 is_sensitive：%+v", first)
	}
	newValue, _ := first["new_value"].(string)
	if !strings.Contains(newValue, "***") || newValue == newSecret {
		t.Fatalf("历史里的新值应脱敏，实际 %q", newValue)
	}
	historyId := int(first["id"].(float64))

	// 3) 回滚：恢复原文，并追加一条标记为回滚的历史
	rollbackResp := doOptionRequest(t, engine, http.MethodPost, "/api/option/rollback",
		`{"id":`+strconv.Itoa(historyId)+`}`)
	if rollbackResp["success"] != true {
		t.Fatalf("回滚应成功：%+v", rollbackResp)
	}
	if config.SMTPToken != "sk-originalsecret" {
		t.Fatalf("回滚后应恢复原文，实际 %q", config.SMTPToken)
	}
	rollbackData := rollbackResp["data"].(map[string]any)
	if rollbackData["key"] != "SMTPToken" {
		t.Fatalf("回滚响应应带 key：%+v", rollbackData)
	}
	rollbackHistory := rollbackData["history"].(map[string]any)
	if rollbackHistory["is_rollback"] != true {
		t.Fatalf("回滚产生的历史应标记 is_rollback：%+v", rollbackHistory)
	}
	serializedRollback, _ := json.Marshal(rollbackResp)
	if strings.Contains(string(serializedRollback), "sk-originalsecret") || strings.Contains(string(serializedRollback), newSecret) {
		t.Fatalf("回滚接口泄漏了密钥原文：%s", string(serializedRollback))
	}

	// 4) 再查历史：应有 2 条，链路可追溯
	historyResp2 := doOptionRequest(t, engine, http.MethodGet, "/api/option/history?key=SMTPToken", "")
	historyData2 := historyResp2["data"].(map[string]any)
	if int(historyData2["total"].(float64)) != 2 {
		t.Fatalf("更新 + 回滚应共 2 条历史：%+v", historyData2)
	}

	// 5) 不存在的 id 要明确失败
	bad := doOptionRequest(t, engine, http.MethodPost, "/api/option/rollback", `{"id":987654}`)
	if bad["success"] != false {
		t.Fatalf("回滚不存在的记录应失败：%+v", bad)
	}
	if message, _ := bad["message"].(string); !strings.Contains(message, "不存在") {
		t.Fatalf("错误信息应说明记录不存在，实际 %q", message)
	}

	// 6) 非法请求体（缺少 id）返回 400
	req := httptest.NewRequest(http.MethodPost, "/api/option/rollback", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("缺少 id 应返回 400，实际 %d", recorder.Code)
	}
}
