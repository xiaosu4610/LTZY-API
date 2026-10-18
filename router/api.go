package router

import (
	"github.com/xiaosu4610/LTZY-API/controller"
	"github.com/xiaosu4610/LTZY-API/controller/auth"
	"github.com/xiaosu4610/LTZY-API/middleware"
	"github.com/xiaosu4610/LTZY-API/model"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

func SetApiRouter(router *gin.Engine) {
	apiRouter := router.Group("/api")
	apiRouter.Use(gzip.Gzip(gzip.DefaultCompression))
	apiRouter.Use(middleware.GlobalAPIRateLimit())
	{
		apiRouter.GET("/status", controller.GetStatus)
		apiRouter.GET("/models", middleware.UserAuth(), controller.DashboardListModels)
		// 模型广场（LTZY-API 新增）：当前用户分组的可用模型 + 价格 + 健康度。
		// 走 UserAuth 而非 AdminAuth —— 这是给用户看的「有哪些模型、多少钱、快不快」。
		apiRouter.GET("/model/plaza", middleware.UserAuth(), controller.GetModelPlaza)
		apiRouter.GET("/notice", controller.GetNotice)
		apiRouter.GET("/about", controller.GetAbout)
		apiRouter.GET("/home_page_content", controller.GetHomePageContent)
		apiRouter.GET("/verification", middleware.CriticalRateLimit(), middleware.TurnstileCheck(), controller.SendEmailVerification)
		apiRouter.GET("/reset_password", middleware.CriticalRateLimit(), middleware.TurnstileCheck(), controller.SendPasswordResetEmail)
		apiRouter.POST("/user/reset", middleware.CriticalRateLimit(), controller.ResetPassword)
		apiRouter.GET("/oauth/github", middleware.CriticalRateLimit(), auth.GitHubOAuth)
		apiRouter.GET("/oauth/oidc", middleware.CriticalRateLimit(), auth.OidcAuth)
		apiRouter.GET("/oauth/lark", middleware.CriticalRateLimit(), auth.LarkOAuth)
		apiRouter.GET("/oauth/state", middleware.CriticalRateLimit(), auth.GenerateOAuthCode)
		apiRouter.GET("/oauth/wechat", middleware.CriticalRateLimit(), auth.WeChatAuth)
		apiRouter.GET("/oauth/wechat/bind", middleware.CriticalRateLimit(), middleware.UserAuth(), auth.WeChatBind)
		apiRouter.GET("/oauth/email/bind", middleware.CriticalRateLimit(), middleware.UserAuth(), controller.EmailBind)
		apiRouter.POST("/topup", middleware.AdminAuth(), controller.AdminTopUp)

		userRoute := apiRouter.Group("/user")
		{
			userRoute.POST("/register", middleware.CriticalRateLimit(), middleware.TurnstileCheck(), controller.Register)
			userRoute.POST("/login", middleware.CriticalRateLimit(), controller.Login)
			userRoute.GET("/logout", controller.Logout)

			selfRoute := userRoute.Group("/")
			selfRoute.Use(middleware.UserAuth())
			{
				selfRoute.GET("/dashboard", controller.GetUserDashboard)
				selfRoute.GET("/self", controller.GetSelf)
				selfRoute.PUT("/self", controller.UpdateSelf)
				selfRoute.DELETE("/self", controller.DeleteSelf)
				selfRoute.GET("/token", controller.GenerateAccessToken)
				selfRoute.GET("/aff", controller.GetAffCode)
				selfRoute.POST("/topup", controller.TopUp)
				selfRoute.GET("/available_models", controller.GetUserAvailableModels)
			}

			adminRoute := userRoute.Group("/")
			// 改用「权限点」校验（LTZY-API 新增，C4）：内置默认配置下与 AdminAuth 完全等价，
			// 但管理员可以据此精细调整角色能力，甚至新建自定义角色。
			adminRoute.Use(middleware.UserAuth())
			// 用户列表/搜索按租户隔离（LTZY-API 新增，C5）
			adminRoute.Use(middleware.TenantScope())
			{
				adminRoute.GET("/", middleware.PermissionAuth(model.PermUserRead), controller.GetAllUsers)
				adminRoute.GET("/search", middleware.PermissionAuth(model.PermUserRead), controller.SearchUsers)
				adminRoute.GET("/:id", middleware.PermissionAuth(model.PermUserRead), controller.GetUser)
				adminRoute.POST("/", middleware.PermissionAuth(model.PermUserWrite), controller.CreateUser)
				adminRoute.POST("/manage", middleware.PermissionAuth(model.PermUserWrite), controller.ManageUser)
				adminRoute.PUT("/", middleware.PermissionAuth(model.PermUserWrite), controller.UpdateUser)
				adminRoute.DELETE("/:id", middleware.PermissionAuth(model.PermUserWrite), controller.DeleteUser)
			}
		}
		optionRoute := apiRouter.Group("/option")
		optionRoute.Use(middleware.RootAuth())
		{
			optionRoute.GET("/", controller.GetOptions)
			// 系统设置写入是最高危操作，审计记录谁改了哪个配置（D4）
			optionRoute.PUT("/", middleware.AuditOptionUpdate(), controller.UpdateOption)
			// 配置版本与回滚（LTZY-API 新增，E9）：历史查询 / 按记录回滚 / 需重启生效清单。
			// 只新增路由，不改动既有路由的行为；鉴权沿用本分组的 RootAuth。
			optionRoute.GET("/history", controller.GetOptionHistory)
			optionRoute.POST("/rollback", controller.RollbackOption)
			optionRoute.GET("/restart_required", controller.GetRestartRequiredOptions)
		}
		// 权限点清单与本人权限（LTZY-API 新增，C4）：前端据此控制菜单与按钮显隐
		permissionRoute := apiRouter.Group("/permission")
		permissionRoute.Use(middleware.UserAuth())
		{
			permissionRoute.GET("/", controller.GetPermissionList)
		}
		// 子账号（LTZY-API 新增，C6）：一个钱包、多把钥匙、各自限额。
		// 走 UserAuth 而非 AdminAuth —— 这是个人/团队内部的钱包管理，不是平台管理能力；
		// 每个接口内部都会校验「目标用户确实是调用者的子账号」。
		childRoute := apiRouter.Group("/subaccount")
		childRoute.Use(middleware.UserAuth())
		{
			childRoute.GET("/", controller.GetChildAccounts)
			childRoute.POST("/", controller.AddChildAccount)
			childRoute.POST("/:id/transfer", controller.TransferQuotaToChild)
			childRoute.POST("/:id/reclaim", controller.ReclaimQuotaFromChild)
			childRoute.PUT("/:id/password", controller.ResetChildPassword)
			childRoute.DELETE("/:id", controller.DeleteChildAccount)
		}
		// 审批流（LTZY-API 新增，C7）：提交申请任何登录用户都能做（提额本就得用户发起），
		// 查看全部与审批是管理能力，单独挂权限点。
		approvalRoute := apiRouter.Group("/approval")
		approvalRoute.Use(middleware.UserAuth())
		{
			approvalRoute.POST("/", controller.CreateApproval)
			approvalRoute.GET("/self", controller.GetMyApprovals)
			approvalRoute.GET("/", middleware.PermissionAuth(model.PermUserRead), controller.GetApprovals)
			approvalRoute.POST("/:id/approve", middleware.PermissionAuth(model.PermUserWrite), controller.ApproveApproval)
			approvalRoute.POST("/:id/reject", middleware.PermissionAuth(model.PermUserWrite), controller.RejectApproval)
		}
		// 租户管理（LTZY-API 新增，C5）：创建租户与变更归属会直接改变数据可见范围，
		// 属最高危操作，与系统设置同级，仅 root 可用。
		tenantRoute := apiRouter.Group("/tenant")
		tenantRoute.Use(middleware.RootAuth())
		{
			tenantRoute.GET("/", controller.GetAllTenants)
			tenantRoute.GET("/:id", controller.GetTenant)
			tenantRoute.POST("/", controller.AddTenant)
			tenantRoute.PUT("/", controller.UpdateTenant)
			tenantRoute.DELETE("/:id", controller.DeleteTenant)
		}
		channelRoute := apiRouter.Group("/channel")
		channelRoute.Use(middleware.UserAuth())
		{
			channelRoute.GET("/", middleware.PermissionAuth(model.PermChannelRead), controller.GetAllChannels)
			channelRoute.GET("/search", middleware.PermissionAuth(model.PermChannelRead), controller.SearchChannels)
			channelRoute.GET("/models", middleware.PermissionAuth(model.PermChannelRead), controller.ListAllModels)
			channelRoute.GET("/health", middleware.PermissionAuth(model.PermChannelRead), controller.GetChannelHealth)
			channelRoute.GET("/:id", middleware.PermissionAuth(model.PermChannelRead), controller.GetChannel)
			channelRoute.GET("/test", middleware.PermissionAuth(model.PermChannelTest), controller.TestChannels)
			channelRoute.GET("/test/:id", middleware.PermissionAuth(model.PermChannelTest), controller.TestChannel)
			channelRoute.GET("/update_balance", middleware.PermissionAuth(model.PermChannelRead), controller.UpdateAllChannelsBalance)
			channelRoute.GET("/update_balance/:id", middleware.PermissionAuth(model.PermChannelRead), controller.UpdateChannelBalance)
			// 渠道含上游密钥，增删改全部审计（body 中 key 字段已脱敏）
			channelRoute.POST("/", middleware.PermissionAuth(model.PermChannelWrite), middleware.AuditAction("channel.create", ""), controller.AddChannel)
			channelRoute.PUT("/", middleware.PermissionAuth(model.PermChannelWrite), middleware.AuditAction("channel.update", ""), controller.UpdateChannel)
			channelRoute.DELETE("/disabled", middleware.PermissionAuth(model.PermChannelWrite), middleware.AuditAction("channel.delete_disabled", ""), controller.DeleteDisabledChannel)
			channelRoute.DELETE("/:id", middleware.PermissionAuth(model.PermChannelWrite), middleware.AuditActionFromParam("channel.delete", "id"), controller.DeleteChannel)
		}
		tokenRoute := apiRouter.Group("/token")
		tokenRoute.Use(middleware.UserAuth())
		{
			tokenRoute.GET("/", controller.GetAllTokens)
			tokenRoute.GET("/search", controller.SearchTokens)
			tokenRoute.GET("/:id", controller.GetToken)
			// 令牌增删改审计：令牌是计费凭据，误删/恶意创建需要可回溯
			tokenRoute.POST("/", middleware.AuditAction("token.create", ""), controller.AddToken)
			tokenRoute.PUT("/", middleware.AuditAction("token.update", ""), controller.UpdateToken)
			tokenRoute.DELETE("/:id", middleware.AuditActionFromParam("token.delete", "id"), controller.DeleteToken)
		}
		redemptionRoute := apiRouter.Group("/redemption")
		redemptionRoute.Use(middleware.UserAuth())
		{
			redemptionRoute.GET("/", middleware.PermissionAuth(model.PermRedemptionRead), controller.GetAllRedemptions)
			redemptionRoute.GET("/search", middleware.PermissionAuth(model.PermRedemptionRead), controller.SearchRedemptions)
			redemptionRoute.GET("/:id", middleware.PermissionAuth(model.PermRedemptionRead), controller.GetRedemption)
			redemptionRoute.POST("/", middleware.PermissionAuth(model.PermRedemptionWrite), controller.AddRedemption)
			redemptionRoute.PUT("/", middleware.PermissionAuth(model.PermRedemptionWrite), controller.UpdateRedemption)
			redemptionRoute.DELETE("/:id", middleware.PermissionAuth(model.PermRedemptionWrite), controller.DeleteRedemption)
		}
		logRoute := apiRouter.Group("/log")
		// 日志按租户隔离（LTZY-API 新增，C5）：只挂在管理类日志接口上，
		// /self 系列本来就只返回本人数据，不需要多一次租户查询。
		logRoute.GET("/", middleware.UserAuth(), middleware.TenantScope(), middleware.PermissionAuth(model.PermLogRead), controller.GetAllLogs)
		logRoute.DELETE("/", middleware.UserAuth(), middleware.TenantScope(), middleware.PermissionAuth(model.PermLogRead), controller.DeleteHistoryLogs)
		logRoute.GET("/stat", middleware.UserAuth(), middleware.TenantScope(), middleware.PermissionAuth(model.PermLogRead), controller.GetLogsStat)
		logRoute.GET("/cost", middleware.UserAuth(), middleware.TenantScope(), middleware.PermissionAuth(model.PermLogRead), controller.GetCostAnalysis)
		// 审计日志查询（LTZY-API 新增，D4）：与消费日志分开放，避免权限耦合
		logRoute.GET("/audit", middleware.UserAuth(), middleware.TenantScope(), middleware.PermissionAuth(model.PermAuditRead), controller.GetAuditLogs)
		logRoute.GET("/self/stat", middleware.UserAuth(), controller.GetLogsSelfStat)
		logRoute.GET("/search", middleware.UserAuth(), middleware.TenantScope(), middleware.PermissionAuth(model.PermLogRead), controller.SearchAllLogs)
		logRoute.GET("/self", middleware.UserAuth(), controller.GetUserLogs)
		logRoute.GET("/self/search", middleware.UserAuth(), controller.SearchUserLogs)
		groupRoute := apiRouter.Group("/group")
		groupRoute.Use(middleware.UserAuth())
		{
			groupRoute.GET("/", middleware.PermissionAuth(model.PermGroupRead), controller.GetGroups)
		}
		// Midjourney 任务（LTZY-API 新增，A4）
		// 用 UserAuth 而非 TokenAuth：这些接口按「会话用户」扣费、且只能操作本人任务，
		// 与 /api/token、/api/log/self 一致；TokenAuth 是 /v1 中继的 API Key 鉴权，语义不同。
		mjRoute := apiRouter.Group("/mj")
		mjRoute.Use(middleware.UserAuth())
		{
			mjRoute.POST("/submit", controller.SubmitMidjourneyTask)
			mjRoute.GET("/tasks", controller.GetUserMidjourneyTasks)
			mjRoute.GET("/task/:id/fetch", controller.FetchMidjourneyTask)
		}
		// 管理面任务排查：只有管理员能看全站任务（用户侧接口已强制按 userId 过滤）
		mjAdminRoute := apiRouter.Group("/mj/admin")
		mjAdminRoute.Use(middleware.AdminAuth())
		{
			mjAdminRoute.GET("/tasks", controller.GetAllMidjourneyTasks)
		}
	}
}
