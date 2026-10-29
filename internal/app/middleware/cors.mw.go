package middleware

import (
	"time"

	"tf-api/internal/app/config"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// CORSMiddleware 按配置构建 CORS。
//
// 安全约束：AllowAllOrigins 与 AllowCredentials 互斥，
// 配置里若同时开启，这里强制关闭 AllowCredentials 并记录告警（见 app.go 启动校验）。
func CORSMiddleware() gin.HandlerFunc {
	s := config.Get().CorsSettings

	allowAll := len(s.AllowOrigins) == 1 && s.AllowOrigins[0] == "*"
	allowCreds := s.AllowCredentials && !allowAll

	cfg := cors.Config{
		AllowOrigins:     s.AllowOrigins,
		AllowMethods:     s.AllowMethods,
		AllowHeaders:     s.AllowHeaders,
		ExposeHeaders:    s.ExposeHeaders,
		AllowCredentials: allowCreds,
		MaxAge:           time.Duration(s.MaxAge) * time.Second,
		// iOS 的 Capacitor WebView 发出的 Origin 是 `capacitor://localhost`，
		// 而 gin-contrib/cors 默认只认 http:// / https:// —— 不在这里登记，
		// `cors.New()` 会在**启动时 panic**（`bad origin: origins must contain '*' or include http://,https://`），
		// 整个服务起不来，看起来像「端口被占」。
		// Android 侧是 `https://localhost`，本身就在默认允许的 scheme 里。
		CustomSchemas: []string{"capacitor://"},
	}
	if allowAll {
		cfg.AllowOriginFunc = nil
	}
	return cors.New(cfg)
}
