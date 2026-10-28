package internal

import (
	"net/http/pprof"
	"path/filepath"
	"time"

	"tf-api/internal/app/buildinfo"
	"tf-api/internal/app/config"
	"tf-api/internal/app/middleware"
	"tf-api/internal/app/router"
	"tf-api/pkg/logger"
	"tf-api/pkg/metrics"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"

	// swagger 文档（由 make swag 生成）
	_ "tf-api/internal/app/swagger"
)

// InitGinEngine 装配 Gin 引擎。
//
// 中间件顺序（顺序即性能与安全的关键）：
//
//	Recovery        最外层，任何 panic 都能兜住
//	→ Metrics       尽早开始计时
//	→ Trace         requestID，后续日志都带
//	→ SecurityHeaders
//	→ CORS
//	→ BodyLimit     在业务解析前拦截超大请求
//	→ Timeout       把超时 context 传给下游
//	→ 业务路由
func InitGinEngine(r router.IRouter, info buildinfo.Info) *gin.Engine {
	version, env := info.Version, info.Env
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	cfg := config.Get().ServiceSettings

	// 生产环境关闭 gin 的调试输出，减少日志噪音
	engine := gin.New()
	engine.HandleMethodNotAllowed = true
	engine.MaxMultipartMemory = 8 << 20 // 8 MiB

	// 可信代理：决定 c.ClientIP() 是否采信 X-Forwarded-For。
	// 不配置时默认不信任任何代理，避免客户端伪造 IP 绕过限流。
	if err := engine.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		logger.ZWarn("invalid trusted proxies, fallback to no-trust",
			zap.Strings("proxies", cfg.TrustedProxies),
			zap.Error(err))
		_ = engine.SetTrustedProxies(nil)
	}

	// ---- 全局中间件链 ----
	engine.Use(middleware.RecoveryMiddleware(logger.GetLogger()))
	engine.Use(metrics.GinMiddleware())
	if cfg.EnableTrace {
		engine.Use(middleware.TraceMiddleware(logger.GetLogger()))
	}
	engine.Use(middleware.SecurityHeadersMiddleware())
	engine.Use(middleware.CORSMiddleware())
	if cfg.MaxBodyBytes > 0 {
		engine.Use(middleware.BodyLimitMiddleware(cfg.MaxBodyBytes))
	}
	if cfg.RequestTimeoutSeconds > 0 {
		engine.Use(middleware.TimeoutMiddleware(time.Duration(cfg.RequestTimeoutSeconds) * time.Second))
	}

	// ---- 静态资源 ----
	staticDir := ensureStaticDir()
	engine.Static("/public", staticDir)

	// ---- 运维端点（不挂业务中间件，避免被超时/限流影响） ----
	if cfg.EnablePprof {
		registerPprof(engine)
	}
	if cfg.EnableSwagger {
		engine.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}
	// /metrics 由 router 注册（需要在 Metrics 中间件之外，否则自我观测会污染指标）

	if err := r.Register(engine); err != nil {
		logger.ZError("register router failed", zap.Error(err))
		panic(err)
	}

	logger.ZInfo("gin engine initialized",
		zap.String("version", version),
		zap.String("env", env),
		zap.String("mode", gin.Mode()),
		zap.String("static", filepath.Clean(staticDir)),
		zap.Bool("pprof", cfg.EnablePprof),
		zap.Bool("swagger", cfg.EnableSwagger),
		zap.Bool("metrics", cfg.EnableMetrics),
		zap.Int64("max_body_bytes", cfg.MaxBodyBytes),
		zap.Int("request_timeout_seconds", cfg.RequestTimeoutSeconds))
	return engine
}

func registerPprof(engine *gin.Engine) {
	g := engine.Group("/debug/pprof")
	g.GET("/", gin.WrapF(pprof.Index))
	g.GET("/cmdline", gin.WrapF(pprof.Cmdline))
	g.GET("/profile", gin.WrapF(pprof.Profile))
	g.GET("/symbol", gin.WrapF(pprof.Symbol))
	g.POST("/symbol", gin.WrapF(pprof.Symbol))
	g.GET("/trace", gin.WrapF(pprof.Trace))
	g.GET("/allocs", gin.WrapF(pprof.Handler("allocs").ServeHTTP))
	g.GET("/block", gin.WrapF(pprof.Handler("block").ServeHTTP))
	g.GET("/goroutine", gin.WrapF(pprof.Handler("goroutine").ServeHTTP))
	g.GET("/heap", gin.WrapF(pprof.Handler("heap").ServeHTTP))
	g.GET("/mutex", gin.WrapF(pprof.Handler("mutex").ServeHTTP))
	g.GET("/threadcreate", gin.WrapF(pprof.Handler("threadcreate").ServeHTTP))
}
