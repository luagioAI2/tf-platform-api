package router

import (
	"time"

	"tf-api/internal/app/api"
	"tf-api/internal/app/config"
	"tf-api/internal/app/middleware"
	"tf-api/internal/app/utils"
	"tf-api/pkg/logger"
	"tf-api/pkg/metrics"
	"tf-api/pkg/rds"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

var _ IRouter = (*Router)(nil)

var RouterSet = wire.NewSet(wire.Struct(new(Router), "*"), wire.Bind(new(IRouter), new(*Router)))

// IRouter 路由注册接口
type IRouter interface {
	Register(app *gin.Engine) error
}

// Router 路由注册入口。
// 按业务域拆分文件：router_auth.go / router_game.go / router_user.go ...
type Router struct {
	SystemAPI       *api.SystemAPI
	AuthAPI         *api.AuthAPI
	UserAPI         *api.UserAPI
	WalletAPI       *api.WalletAPI
	GameAPI         *api.GameAPI
	GameStorageAPI  *api.GameStorageAPI
	GameSnapshotAPI *api.GameSnapshotAPI
	GameCoinAPI     *api.GameCoinAPI
	TaskAPI         *api.TaskAPI
	RedeemAPI       *api.RedeemAPI
	WishAPI         *api.WishAPI
	GameReleaseAPI  *api.GameReleaseAPI

	// RDS 供中间件做黑名单/限流（可为 nil，表示降级运行）
	RDS *redis.Client

	// Limiter 分布式限流器（可为 nil，中间件内部会降级为本地限流）
	Limiter *rds.RateLimiter
}

// Register 注册全部路由
func (a *Router) Register(app *gin.Engine) error {
	// 注册 validator 多语言翻译（只需一次）
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		if err := utils.RegisterTranslations(v); err != nil {
			logger.ZInfo("RegisterTranslations", zap.Error(err))
		}
	}

	if a.RDS != nil && a.Limiter == nil {
		a.Limiter = rds.NewRateLimiter(a.RDS)
	}

	// 运维端点：不挂业务中间件，避免被超时/限流影响
	a.registerOps(app)
	a.registerAPI(app)
	return nil
}

// registerOps 运维端点（/metrics /health /healthz）
func (a *Router) registerOps(app *gin.Engine) {
	cfg := config.Get().ServiceSettings

	// 存活探针：只要进程还在就返回 200，不检查依赖
	// 用途：k8s livenessProbe。依赖故障时不应重启 Pod（重启解决不了 DB 故障）。
	app.GET("/healthz", a.SystemAPI.Live)

	// 就绪探针：检查 DB/Redis，故障时返回 503 让流量摘除
	// 用途：k8s readinessProbe、LB 健康检查。
	app.GET("/health", a.SystemAPI.Ready)

	// Prometheus 指标
	if cfg.EnableMetrics {
		app.GET("/metrics", metrics.Handler())
	}
}

// registerAPI 业务路由统一挂在 /api 下
func (a *Router) registerAPI(app *gin.Engine) {
	r := app.Group("/api")

	// ---- 全局限流（按 IP）----
	cfg := config.Get().ServiceSettings
	if cfg.RateLimit.Enable {
		window := time.Duration(cfg.RateLimit.GlobalWindowSeconds) * time.Second
		if window <= 0 {
			window = time.Minute
		}
		r.Use(middleware.RateLimitMiddleware(a.Limiter, middleware.RateLimitConfig{
			Name:   "global",
			Limit:  cfg.RateLimit.GlobalLimit,
			Window: window,
		}))
	}

	// ---- 公开接口 ----
	r.POST("/ping", a.SystemAPI.Ping)

	// 认证：发送验证码单独限流（防短信轰炸）
	authGroup := r.Group("/auth")
	{
		authGroup.POST("/send-sms-code",
			middleware.RateLimitMiddleware(a.Limiter, middleware.RateLimitConfig{
				Name:   "sms",
				Limit:  10,
				Window: time.Minute,
			}),
			a.AuthAPI.SendSmsCode,
		)
		authGroup.POST("/register", a.AuthAPI.Register)
		authGroup.POST("/login", a.AuthAPI.Login)
		authGroup.POST("/sms-login", a.AuthAPI.SmsLogin)
		authGroup.POST("/wechat-login", a.AuthAPI.WechatLogin)
		authGroup.POST("/refresh", a.AuthAPI.Refresh)
		authGroup.POST("/reset-password", a.AuthAPI.ResetPassword)

		// 需要登录
		authGroup.POST("/logout", middleware.JWTMiddleware(a.RDS), a.AuthAPI.Logout)
		authGroup.POST("/bind-mobile", middleware.JWTMiddleware(a.RDS), a.AuthAPI.BindMobile)
		authGroup.POST("/change-password", middleware.JWTMiddleware(a.RDS), a.AuthAPI.ChangePassword)
	}

	// ---- 用户（需登录）----
	userGroup := r.Group("/user").Use(middleware.JWTMiddleware(a.RDS))
	{
		userGroup.POST("/profile", a.UserAPI.Profile)
		userGroup.POST("/update-profile", a.UserAPI.UpdateProfile)
	}

	// ---- 钱包（需登录）----
	walletGroup := r.Group("/wallet").Use(middleware.JWTMiddleware(a.RDS))
	{
		walletGroup.POST("/info", a.WalletAPI.Info)
		walletGroup.POST("/ledger", a.WalletAPI.Ledger)
	}

	// ---- 游戏商城 ----
	gameGroup := r.Group("/game")
	{
		// 列表/详情：登录可选（未登录也能浏览，但 owned 全 false）
		gameGroup.POST("/list", middleware.OptionalJWTMiddleware(a.RDS), a.GameAPI.List)
		gameGroup.POST("/detail", middleware.OptionalJWTMiddleware(a.RDS), a.GameAPI.Detail)

		// 购买/我的游戏：必须登录
		gameGroup.POST("/purchase", middleware.JWTMiddleware(a.RDS), a.GameAPI.Purchase)
		gameGroup.POST("/my-games", middleware.JWTMiddleware(a.RDS), a.GameAPI.MyGames)
	}

	// ---- 游戏存档（通用 KV，需登录）----
	kvGroup := r.Group("/game/kv").Use(middleware.JWTMiddleware(a.RDS))
	{
		kvGroup.POST("/get", a.GameStorageAPI.Get)
		kvGroup.POST("/set", a.GameStorageAPI.Set)
		kvGroup.POST("/list", a.GameStorageAPI.List)
		kvGroup.POST("/delete", a.GameStorageAPI.Delete)
		kvGroup.POST("/usage", a.GameStorageAPI.Usage)
	}

	// ---- 存档快照与回档（需登录）----
	snapGroup := r.Group("/game/snapshot").Use(middleware.JWTMiddleware(a.RDS))
	{
		snapGroup.POST("/list", a.GameSnapshotAPI.List)
		snapGroup.POST("/get", a.GameSnapshotAPI.Get)
		snapGroup.POST("/create", a.GameSnapshotAPI.Create)
		snapGroup.POST("/restore", a.GameSnapshotAPI.Restore)
		snapGroup.POST("/restore-by-time", a.GameSnapshotAPI.RestoreByTime)
		snapGroup.POST("/restore-logs", a.GameSnapshotAPI.RestoreLogs)
	}

	// ---- 游戏币（需登录）----
	coinGroup := r.Group("/game/coin").Use(middleware.JWTMiddleware(a.RDS))
	{
		coinGroup.POST("/info", a.GameCoinAPI.Info)
		coinGroup.POST("/list", a.GameCoinAPI.ListInfo)
		coinGroup.POST("/buy", a.GameCoinAPI.Buy)
		coinGroup.POST("/spend", a.GameCoinAPI.Spend)
		coinGroup.POST("/mint", a.GameCoinAPI.Mint)
		coinGroup.POST("/redeem", a.GameCoinAPI.Redeem)
		coinGroup.POST("/ledger", a.GameCoinAPI.Ledger)
	}

	// ---- 任务系统（需登录）----
	taskGroup := r.Group("/task").Use(middleware.JWTMiddleware(a.RDS))
	{
		// 任务定义
		taskGroup.POST("/create", a.TaskAPI.CreateTask)
		taskGroup.POST("/update", a.TaskAPI.UpdateTask)
		taskGroup.POST("/delete", a.TaskAPI.DeleteTask)
		taskGroup.POST("/list", a.TaskAPI.ListTasks)

		// 任务板与实例
		taskGroup.POST("/board", a.TaskAPI.Board)
		taskGroup.POST("/instances", a.TaskAPI.Instances)

		// 计时
		taskGroup.POST("/start-timer", a.TaskAPI.StartTimer)
		taskGroup.POST("/pause-timer", a.TaskAPI.PauseTimer)
		taskGroup.POST("/resume-timer", a.TaskAPI.ResumeTimer)

		// 提交与审核
		taskGroup.POST("/submit", a.TaskAPI.Submit)
		taskGroup.POST("/review", a.TaskAPI.Review)
		taskGroup.POST("/skip", a.TaskAPI.Skip)
		taskGroup.POST("/pending-review", a.TaskAPI.PendingReview)
		taskGroup.POST("/stats", a.TaskAPI.Stats)

		// 签到
		taskGroup.POST("/check-in", a.TaskAPI.CheckIn)
		taskGroup.POST("/check-in-status", a.TaskAPI.CheckInStatus)
	}

	// ---- 系统（需登录）----
	sysGroup := r.Group("/system").Use(middleware.JWTMiddleware(a.RDS))
	{
		sysGroup.POST("/info", a.SystemAPI.RuntimeInfo)
	}

	// ---- 兑换商城（需登录）----
	redeemGroup := r.Group("/redeem").Use(middleware.JWTMiddleware(a.RDS))
	{
		// 商品管理（家长）
		redeemGroup.POST("/item/create", a.RedeemAPI.CreateItem)
		redeemGroup.POST("/item/update", a.RedeemAPI.UpdateItem)
		redeemGroup.POST("/item/delete", a.RedeemAPI.DeleteItem)

		// 列表与兑换
		redeemGroup.POST("/list", a.RedeemAPI.ListItems)
		redeemGroup.POST("/redeem", a.RedeemAPI.Redeem)
		// 兑换记录：家里换的 + 心愿换的，合成一条时间线
		redeemGroup.POST("/history", a.RedeemAPI.History)

		// 记录与处理
		redeemGroup.POST("/records", a.RedeemAPI.ListRecords)
		redeemGroup.POST("/fulfill", a.RedeemAPI.Fulfill)
		redeemGroup.POST("/cancel", a.RedeemAPI.Cancel)
		redeemGroup.POST("/stats", a.RedeemAPI.Stats)
	}

	// ---- 心愿商城（需登录）----
	wishGroup := r.Group("/wish").Use(middleware.JWTMiddleware(a.RDS))
	{
		wishGroup.POST("/list", a.WishAPI.ListItems)
		wishGroup.POST("/detail", a.WishAPI.Detail)

		// 家长定价
		wishGroup.POST("/set-offer", a.WishAPI.SetOffer)

		// 孩子操作
		wishGroup.POST("/wish", a.WishAPI.Wish)
		wishGroup.POST("/redeem", a.WishAPI.Redeem)

		// 家长推进
		wishGroup.POST("/confirm", a.WishAPI.Confirm)
		wishGroup.POST("/done", a.WishAPI.Done)
		wishGroup.POST("/cancel", a.WishAPI.Cancel)

		// 查询
		wishGroup.POST("/orders", a.WishAPI.Orders)
		wishGroup.POST("/order-detail", a.WishAPI.OrderDetail)
		wishGroup.POST("/pending", a.WishAPI.Pending)

		// 兑换码
		wishGroup.POST("/redeem-code", a.WishAPI.RedeemCode)
	}

	// ---- 游戏分发 ----
	{
		// 目录与签名：**公开**（客户端装包前就要拿到，地址烧进 APK）
		r.GET("/game/catalog", a.GameReleaseAPI.Catalog)
		r.GET("/game/catalog.sig", a.GameReleaseAPI.CatalogSig)

		// 需登录
		gameGroup.POST("/update-check", middleware.JWTMiddleware(a.RDS), a.GameReleaseAPI.CheckUpdate)
		gameGroup.POST("/download", middleware.JWTMiddleware(a.RDS), a.GameReleaseAPI.Download)
	}

	// ---- 后台（需后台令牌）----
	admin := r.Group("/admin").Use(middleware.AdminMiddleware())
	{
		// 游戏版本发布
		admin.POST("/game/release/publish", a.GameReleaseAPI.Publish)
		admin.POST("/game/release/list", a.GameReleaseAPI.List)
		admin.POST("/game/release/delete", a.GameReleaseAPI.Delete)
		admin.POST("/game/release/self-check", a.GameReleaseAPI.SelfCheck)
	}
}
