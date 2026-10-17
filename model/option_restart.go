package model

// 需重启生效的配置清单（LTZY-API 新增，E9）。
//
// 为什么要有这份清单：applyOptionToRuntime 覆盖不到、或覆盖到了但行为仍需重启的键，
// 表现都是"接口返回成功、值也存进了 options 表，但运行时行为毫无变化"—— 这种静默
// 失效比报错更难排查。与其假装能热更新，不如把"改了要重启"显式告诉管理员
// （GET /api/option/restart_required）。
//
// 清单分两类，用 ValueApplied 区分：
//  1. ValueApplied=false（只有环境变量/代码能配置）：值在进程启动时读进 config 全局变量，
//     或被闭包捕获进中间件/协程，写进 options 表不会有任何效果；
//  2. ValueApplied=true（值会热同步，但行为要重启）：例如 Theme —— config.Theme 会立刻
//     变成新值，但服务端真正生效的是"启动时选定的前端产物目录"（router/web.go），
//     因此界面要重启后才切换。

// RestartRequiredOption 一个"需重启生效"的配置项。
type RestartRequiredOption struct {
	// Key 是配置键名。管理员如果通过 PUT /api/option 写了这个键，需要知道它不会（完全）生效。
	Key string `json:"key"`
	// EnvVar 是真正生效的环境变量名。为空表示该值在代码内固定，连环境变量都没有，
	// 必须改代码/升级才能调整（前端可据此区分"改环境变量"与"改代码"两种处理方式）。
	EnvVar string `json:"env_var"`
	// ValueApplied 表示配置值本身是否会被热同步到内存（仍不足以让行为生效）。
	// false 的键写库是彻底无效的；true 的键值会变、但行为要重启。
	ValueApplied bool `json:"value_applied"`
	// Reason 说明"为什么不能热更新"，供前端直接展示给管理员。
	Reason string `json:"reason"`
}

// restartRequiredOptions 静态清单。
//
// 为什么用常量清单而不是运行时探测：这些键的失效原因来自"启动顺序/闭包捕获"这类
// 代码结构事实，只能靠人读代码维护。为降低漂移风险，单测会断言：
//  1. 清单里的键名合法（非空、无空格、不重复），且 reason 非空；
//  2. 值的语义与 ValueApplied 自洽 —— 值完全不生效的键（ValueApplied=false）不允许出现在
//     applyOptionToRuntime 的处理范围里，反之（如 Theme）必须确实只做"值同步"。
var restartRequiredOptions = []RestartRequiredOption{
	{
		Key:          "Theme",
		EnvVar:       "THEME",
		ValueApplied: true,
		Reason:       "主题值本身会热同步（config.Theme 立即变化），但真正决定界面的是启动时选定的前端产物目录（router/web.go 把 web/build/<主题> 挂成静态路由），因此切换主题仍要重启服务。",
	},
	{
		Key:    "BatchUpdateEnabled",
		EnvVar: "BATCH_UPDATE_ENABLED",
		Reason: "批量更新器协程只在启动时按环境变量创建；运行中打开会让渠道/用户/令牌的写操作进入没有消费者的缓冲通道，通道满后请求挂起，因此不支持热更新。",
	},
	{
		Key:    "SyncFrequency",
		EnvVar: "SYNC_FREQUENCY",
		Reason: "配置与渠道缓存的同步周期在协程启动时被固化进 sleep 时长，改库不会改变已在运行的循环。",
	},
	{
		Key:    "CorsAllowOrigins",
		EnvVar: "CORS_ALLOW_ORIGINS",
		Reason: "CORS 白名单在 gin 引擎装配中间件时解析一次，之后请求不再读取配置。",
	},
	{
		Key:    "RelayProxy",
		EnvVar: "RELAY_PROXY",
		Reason: "中继用的 http.Client 在 client.Init() 时按代理构建，运行中改代理需要重建客户端。",
	},
	{
		Key:    "UserContentRequestProxy",
		EnvVar: "USER_CONTENT_REQUEST_PROXY",
		Reason: "抓取用户内容用的 http.Client 同样在启动时构建，改代理需重启。",
	},
	{
		Key:    "UserContentRequestTimeout",
		EnvVar: "USER_CONTENT_REQUEST_TIMEOUT",
		Reason: "超时被写进启动时创建的 http.Client.Timeout，运行中改无效。",
	},
	{
		Key:    "RelayTimeout",
		EnvVar: "RELAY_TIMEOUT",
		Reason: "同 UserContentRequestTimeout：超时在 client.Init() 时固化进 http.Client。",
	},
	{
		Key:    "DebugEnabled",
		EnvVar: "DEBUG",
		Reason: "调试开关影响 gin 运行模式与限流退化等进程级行为，在启动时判定。",
	},
	{
		Key:    "DebugSQLEnabled",
		EnvVar: "DEBUG_SQL",
		Reason: "SQL 调试日志在建立数据库连接时挂到 GORM 上，运行中改需要重建连接。",
	},
	{
		Key:    "EnableMetric",
		EnvVar: "ENABLE_METRIC",
		Reason: "指标消费协程在启动时按开关创建；运行中打开会往无人消费的通道写入，导致协程堆积。",
	},
	{
		Key:    "OnlyOneLogFile",
		EnvVar: "ONLY_ONE_LOG_FILE",
		Reason: "日志文件句柄在日志初始化时按该开关选定，运行中改不会切换文件。",
	},
	{
		Key:    "GlobalApiRateLimitNum",
		EnvVar: "GLOBAL_API_RATE_LIMIT",
		Reason: "限流阈值与时长在路由装配时被闭包捕获，运行中改不会影响已注册的中间件。",
	},
	{
		Key:    "GlobalWebRateLimitNum",
		EnvVar: "GLOBAL_WEB_RATE_LIMIT",
		Reason: "同上：限流阈值在路由装配时固化。",
	},
	{
		Key:    "CriticalRateLimitNum",
		EnvVar: "",
		Reason: "同上：限流阈值在路由装配时固化，且该值在代码内固定（无环境变量），调整需要改代码后重启。",
	},
	{
		Key:    "UploadRateLimitNum",
		EnvVar: "",
		Reason: "同上：限流阈值在路由装配时固化，且该值在代码内固定（无环境变量）。",
	},
	{
		Key:    "DownloadRateLimitNum",
		EnvVar: "",
		Reason: "同上：限流阈值在路由装配时固化，且该值在代码内固定（无环境变量）。",
	},
	{
		Key:    "GeminiSafetySetting",
		EnvVar: "GEMINI_SAFETY_SETTING",
		Reason: "没有对应的 options 键，值来自进程启动时读取的环境变量。",
	},
	{
		Key:    "TestPrompt",
		EnvVar: "TEST_PROMPT",
		Reason: "没有对应的 options 键，值来自进程启动时读取的环境变量。",
	},
	{
		Key:    "EnforceIncludeUsage",
		EnvVar: "ENFORCE_INCLUDE_USAGE",
		Reason: "没有对应的 options 键，值来自进程启动时读取的环境变量。",
	},
}

// RestartRequiredOptions 返回"需重启生效"的配置清单（管理面接口的数据源）。
// 返回副本，避免调用方（含前端序列化）意外改动包级清单。
func RestartRequiredOptions() []RestartRequiredOption {
	out := make([]RestartRequiredOption, len(restartRequiredOptions))
	copy(out, restartRequiredOptions)
	return out
}

// IsRestartRequiredOption 判断某个配置键是否属于"需重启生效"清单
// （用于管理面提示与接口展示，不代表回滚被禁止）。
func IsRestartRequiredOption(key string) bool {
	for _, option := range restartRequiredOptions {
		if option.Key == key {
			return true
		}
	}
	return false
}

// IsEnvOnlyOption 判断某个键是否"只能通过环境变量/代码配置"：
// 这类键写库是彻底无效的，因此回滚也一并拒绝 —— 否则回滚接口会返回成功却什么也没改变，
// 属于任务里明确禁止的"假装能热更新"。
//
// 注意与 IsRestartRequiredOption 的区别：Theme 也需重启，但它的值是能热同步的
// （options 表与 config.Theme 都会变），回滚 Theme 是有意义的，因此不在此列。
func IsEnvOnlyOption(key string) bool {
	for _, option := range restartRequiredOptions {
		if option.Key == key {
			return !option.ValueApplied
		}
	}
	return false
}
