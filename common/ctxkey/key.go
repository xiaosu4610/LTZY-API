package ctxkey

const (
	Config            = "config"
	Id                = "id"
	Username          = "username"
	Role              = "role"
	Status            = "status"
	Channel           = "channel"
	ChannelId         = "channel_id"
	SpecificChannelId = "specific_channel_id"
	RequestModel      = "request_model"
	ConvertedRequest  = "converted_request"
	OriginalModel     = "original_model"
	Group             = "group"
	ModelMapping      = "model_mapping"
	ChannelName       = "channel_name"
	// ChannelKey 本次请求实际使用的那把密钥（LTZY-API 新增）。
	// 多密钥渠道会在请求开始时轮流挑一把，失败时靠它精确定位到具体密钥去冷却。
	ChannelKey = "channel_key"
	TokenId           = "token_id"
	TokenName         = "token_name"
	BaseURL           = "base_url"
	AvailableModels   = "available_models"
	KeyRequestBody    = "key_request_body"
	SystemPrompt      = "system_prompt"
	TokenRpm          = "token_rpm"
	TokenTpm          = "token_tpm"
	TokenConcurrency  = "token_concurrency"
	TokenDailyLimit   = "token_daily_limit"
	TokenMonthlyLimit = "token_monthly_limit"
	TokenAllowedModes = "token_allowed_modes"
	// 调用者所属租户（LTZY-API 新增，C5）。0 = 平台视角（不隔离，可跨租户查看）。
	TenantId = "tenant_id"
)
