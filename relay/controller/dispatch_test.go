package controller

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/ctxkey"
	"github.com/xiaosu4610/LTZY-API/model"
	"github.com/xiaosu4610/LTZY-API/relay/channeltype"
)

// ModelessChannelDispatch 的单测（A6）。
//
// 用真实 SQLite 而不是 mock：这个钩子的全部价值就在「从 abilities/channels 两张表里
// 挑出一个渠道」，把数据库 mock 掉等于什么都没验。

// dispatchTestContext 用一个只挂 ModelessChannelDispatch 的最小路由，
// 记录下一个处理器看到的上下文，用来断言钩子做了什么。
type dispatchTestContext struct {
	requestModel      string
	specificChannelId string
}

func setupDispatchTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.SQLitePath = filepath.Join(t.TempDir(), "dispatch-test.db")
	common.UsingSQLite = true
	common.RedisEnabled = false

	db, err := gorm.Open(sqlite.Open(common.SQLitePath), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败：%v", err)
	}
	previousDB := model.DB
	model.DB = db
	model.LOG_DB = db
	if err := db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Ability{}); err != nil {
		t.Fatalf("迁移测试表失败：%v", err)
	}
	// 默认（未配置时）关闭内存缓存，路由走数据库查询；显式关掉以避免依赖环境变量
	memoryCacheEnabled := config.MemoryCacheEnabled
	config.MemoryCacheEnabled = false

	t.Cleanup(func() {
		config.MemoryCacheEnabled = memoryCacheEnabled
		model.DB = previousDB
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

// seedDispatchUser 建一个属于 default 分组的用户。
func seedDispatchUser(t *testing.T, id int) {
	t.Helper()
	user := &model.User{
		Id:          id,
		Username:    "dispatch-user",
		AccessToken: "dispatch-access-token",
		AffCode:     "dispatch-aff",
		Group:       "default",
		Status:      model.UserStatusEnabled,
		Quota:       1000000,
	}
	if err := model.DB.Create(user).Error; err != nil {
		t.Fatalf("创建测试用户失败：%v", err)
	}
}

// seedDispatchChannel 建一个渠道 + 它的可用模型（ability）。
func seedDispatchChannel(t *testing.T, channelId int, channelType int, modelName string, priority int64) {
	t.Helper()
	channel := &model.Channel{
		Id:       channelId,
		Type:     channelType,
		Key:      "sk-test",
		Status:   model.ChannelStatusEnabled,
		Name:     modelName,
		Models:   modelName,
		Group:    "default",
		Priority: &priority,
	}
	if err := model.DB.Create(channel).Error; err != nil {
		t.Fatalf("创建测试渠道失败：%v", err)
	}
	enabled := true
	ability := &model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelId,
		Enabled:   enabled,
		Priority:  &priority,
	}
	if err := model.DB.Create(ability).Error; err != nil {
		t.Fatalf("创建测试 ability 失败：%v", err)
	}
}

// runDispatch 走一遍钩子 + 一个记录上下文的终止处理器。
func runDispatch(t *testing.T, method string, path string, presetChannelId string, availableModels string) dispatchTestContext {
	t.Helper()
	gin.SetMode(gin.TestMode)
	result := dispatchTestContext{}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(ctxkey.Id, 1)
		if presetChannelId != "" {
			c.Set(ctxkey.SpecificChannelId, presetChannelId)
		}
		if availableModels != "" {
			c.Set(ctxkey.AvailableModels, availableModels)
		}
		if presetModel := c.Query("request_model"); presetModel != "" {
			c.Set(ctxkey.RequestModel, presetModel)
		}
		c.Next()
	})
	engine.Use(ModelessChannelDispatch())
	engine.Any("/*path", func(c *gin.Context) {
		result.requestModel = c.GetString(ctxkey.RequestModel)
		result.specificChannelId = c.GetString(ctxkey.SpecificChannelId)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(method, path, nil)
	engine.ServeHTTP(httptest.NewRecorder(), req)
	return result
}

// TestModelessChannelDispatchPinsOpenAIChannel 无模型端点（GET /v1/files）
// 应当被定到一个 OpenAI 协议渠道上，并补上一个真实模型名。
func TestModelessChannelDispatchPinsOpenAIChannel(t *testing.T) {
	setupDispatchTestDB(t)
	seedDispatchUser(t, 1)
	seedDispatchChannel(t, 7, channeltype.OpenAI, "gpt-4o-mini", 0)

	result := runDispatch(t, http.MethodGet, "/v1/files", "", "")
	if result.specificChannelId != "7" {
		t.Fatalf("应把请求定到渠道 7，实际 %q", result.specificChannelId)
	}
	if result.requestModel != "gpt-4o-mini" {
		t.Fatalf("应补上分组内的模型名，实际 %q", result.requestModel)
	}
}

// TestModelessChannelDispatchPrefersOpenAIProtocol 多个可选渠道时，
// 优先原生 OpenAI / OpenAI 兼容渠道，而不是优先级更高的第三方渠道。
func TestModelessChannelDispatchPrefersOpenAIProtocol(t *testing.T) {
	setupDispatchTestDB(t)
	seedDispatchUser(t, 1)
	// 优先级更高的第三方渠道（走 OpenAI 协议适配器，但未必支持 files/assistants）
	seedDispatchChannel(t, 8, channeltype.Moonshot, "moonshot-v1-8k", 100)
	// 原生 OpenAI 渠道优先级更低
	seedDispatchChannel(t, 7, channeltype.OpenAI, "gpt-4o-mini", 0)
	// 非 OpenAI 协议渠道，任何时候都不该被选中
	seedDispatchChannel(t, 9, channeltype.Anthropic, "claude-3-5-sonnet", 999)

	result := runDispatch(t, http.MethodGet, "/v1/files", "", "")
	if result.specificChannelId != "7" {
		t.Fatalf("应优先选中原生 OpenAI 渠道 7，实际 %q", result.specificChannelId)
	}
}

// TestModelessChannelDispatchKeepsPinnedChannel 管理员用 sk-xxx-<渠道id> 指定的渠道
// 必须被尊重（钩子不得改写）。
func TestModelessChannelDispatchKeepsPinnedChannel(t *testing.T) {
	setupDispatchTestDB(t)
	seedDispatchUser(t, 1)
	seedDispatchChannel(t, 7, channeltype.OpenAI, "gpt-4o-mini", 0)

	result := runDispatch(t, http.MethodGet, "/v1/files", "42", "")
	if result.specificChannelId != "42" {
		t.Fatalf("已指定渠道时应保持 42，实际 %q", result.specificChannelId)
	}
	if result.requestModel != "" {
		t.Fatalf("已指定渠道时不该改写模型，实际 %q", result.requestModel)
	}
}

// TestModelessChannelDispatchRespectsTokenModelWhitelist 令牌配了模型白名单时，
// 只能从白名单内的模型上挑渠道，避免这些端点变成绕过白名单的口子。
func TestModelessChannelDispatchRespectsTokenModelWhitelist(t *testing.T) {
	setupDispatchTestDB(t)
	seedDispatchUser(t, 1)
	seedDispatchChannel(t, 7, channeltype.OpenAI, "gpt-4o-mini", 0)
	seedDispatchChannel(t, 8, channeltype.OpenAI, "gpt-4o", 0)

	// 白名单只放开 gpt-4o
	result := runDispatch(t, http.MethodGet, "/v1/files", "", "gpt-4o")
	if result.specificChannelId != "8" {
		t.Fatalf("只应在白名单模型对应的渠道 8 上挑，实际 %q", result.specificChannelId)
	}

	// 白名单里没有任何可用模型 → 不猜测，交回 Distribute
	result = runDispatch(t, http.MethodGet, "/v1/files", "", "not-a-real-model")
	if result.specificChannelId != "" {
		t.Fatalf("白名单无可用模型时不该越权选渠道，实际 %q", result.specificChannelId)
	}
}

// TestModelessChannelDispatchIgnoresOtherEndpoints 非透传端点必须完全不受影响。
func TestModelessChannelDispatchIgnoresOtherEndpoints(t *testing.T) {
	setupDispatchTestDB(t)
	seedDispatchUser(t, 1)
	seedDispatchChannel(t, 7, channeltype.OpenAI, "gpt-4o-mini", 0)

	for _, path := range []string{"/v1/chat/completions", "/v1/embeddings", "/v1/responses", "/v1/models"} {
		result := runDispatch(t, http.MethodPost, path, "", "")
		if result.specificChannelId != "" || result.requestModel != "" {
			t.Fatalf("%s 不该被钩子改动：channel=%q model=%q", path, result.specificChannelId, result.requestModel)
		}
	}
}

// TestModelessChannelDispatchWithoutEligibleChannel 分组里只有非 OpenAI 协议渠道时，
// 不硬凑：请求交回 Distribute 按常规报错，而不是发一个注定失败的上游请求。
func TestModelessChannelDispatchWithoutEligibleChannel(t *testing.T) {
	setupDispatchTestDB(t)
	seedDispatchUser(t, 1)
	seedDispatchChannel(t, 9, channeltype.Anthropic, "claude-3-5-sonnet", 0)

	result := runDispatch(t, http.MethodGet, "/v1/files", "", "")
	if result.specificChannelId != "" {
		t.Fatalf("没有可用渠道时不该指定渠道，实际 %q", result.specificChannelId)
	}
}
