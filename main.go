package main

import (
	"embed"
	"fmt"
	"os"
	"strconv"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	_ "github.com/joho/godotenv/autoload"

	"github.com/xiaosu4610/LTZY-API/common"
	"github.com/xiaosu4610/LTZY-API/common/client"
	"github.com/xiaosu4610/LTZY-API/common/config"
	"github.com/xiaosu4610/LTZY-API/common/i18n"
	"github.com/xiaosu4610/LTZY-API/common/logger"
	"github.com/xiaosu4610/LTZY-API/controller"
	"github.com/xiaosu4610/LTZY-API/middleware"
	"github.com/xiaosu4610/LTZY-API/model"
	"github.com/xiaosu4610/LTZY-API/monitor"
	"github.com/xiaosu4610/LTZY-API/relay/adaptor/openai"
	"github.com/xiaosu4610/LTZY-API/router"
)

//go:embed web/build/*
var buildFS embed.FS

func main() {
	common.Init()
	logger.SetupLogger()
	logger.SysLogf("LTZY-API %s started", common.Version)

	if os.Getenv("GIN_MODE") != gin.DebugMode {
		gin.SetMode(gin.ReleaseMode)
	}
	if config.DebugEnabled {
		logger.SysLog("running in debug mode")
	}

	// Initialize SQL Database
	model.InitDB()
	model.InitLogDB()

	var err error
	err = model.CreateRootAccountIfNeed()
	if err != nil {
		logger.FatalLog("database init error: " + err.Error())
	}
	defer func() {
		err := model.CloseDB()
		if err != nil {
			logger.FatalLog("failed to close database: " + err.Error())
		}
	}()

	// Initialize Redis
	err = common.InitRedisClient()
	if err != nil {
		logger.FatalLog("failed to initialize Redis: " + err.Error())
	}

	// Initialize options
	model.InitOptionMap()
	// 渠道级上游进价（LTZY-API 新增）：必须在计费开始前装载，否则前几笔消费会按全局进价记账
	model.RefreshChannelCostRatios()
	logger.SysLog(fmt.Sprintf("using theme %s", config.Theme))
	if common.RedisEnabled {
		// for compatibility with old versions
		config.MemoryCacheEnabled = true
	}
	if config.MemoryCacheEnabled {
		logger.SysLog("memory cache enabled")
		logger.SysLog(fmt.Sprintf("sync frequency: %d seconds", config.SyncFrequency))
		model.InitChannelCache()
	}
	if config.MemoryCacheEnabled {
		go model.SyncOptions(config.SyncFrequency)
		go model.SyncChannelCache(config.SyncFrequency)
	}
	if os.Getenv("CHANNEL_TEST_FREQUENCY") != "" {
		frequency, err := strconv.Atoi(os.Getenv("CHANNEL_TEST_FREQUENCY"))
		if err != nil {
			logger.FatalLog("failed to parse CHANNEL_TEST_FREQUENCY: " + err.Error())
		}
		go controller.AutomaticallyTestChannels(frequency)
	}
	if os.Getenv("BATCH_UPDATE_ENABLED") == "true" {
		config.BatchUpdateEnabled = true
		logger.SysLog("batch update enabled with interval " + strconv.Itoa(config.BatchUpdateInterval) + "s")
		model.InitBatchUpdater()
	}
	if config.EnableMetric {
		logger.SysLog("metric enabled, will disable channel if too much request failed")
	}
	// 渠道保活检测（LTZY-API 新增）：定时把「已禁用但上游已恢复」的渠道自动救回，
	// 周期与范围由 ChannelKeepAliveEnabled / ChannelKeepAliveMinutes / ChannelKeepAliveScope 控制。
	controller.StartChannelKeepAlive()
	// 用量/成本告警（LTZY-API 新增，D6）：阈值未配置时不启动，零开销
	monitor.StartUsageAlert()
	openai.InitTokenEncoders()
	client.Init()

	// Initialize i18n
	if err := i18n.Init(); err != nil {
		logger.FatalLog("failed to initialize i18n: " + err.Error())
	}

	// Initialize HTTP server
	server := gin.New()
	server.Use(gin.Recovery())
	// This will cause SSE not to work!!!
	//server.Use(gzip.Gzip(gzip.DefaultCompression))
	server.Use(middleware.RequestId())
	server.Use(middleware.Language())
	middleware.SetUpLogger(server)
	// Initialize session store
	store := cookie.NewStore([]byte(config.SessionSecret))
	server.Use(sessions.Sessions("session", store))

	router.SetRouter(server, buildFS)
	var port = os.Getenv("PORT")
	if port == "" {
		port = strconv.Itoa(*common.Port)
	}
	// LISTEN_HOST 用于限制监听地址：生产环境设为 127.0.0.1 时，应用只对本机开放，
	// 公网入口统一交给 Nginx（多 IP 场景下无需应用层适配多个网卡）。
	// 不设置时保持原行为（监听全部网卡），因此不影响本地开发与既有部署。
	addr := ":" + port
	if host := os.Getenv("LISTEN_HOST"); host != "" {
		addr = host + ":" + port
	}
	logger.SysLogf("server started on http://%s", addr)
	err = server.Run(addr)
	if err != nil {
		logger.FatalLog("failed to start HTTP server: " + err.Error())
	}
}
