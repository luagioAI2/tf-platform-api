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
	}
	if allowAll {
		cfg.AllowOriginFunc = nil
	}
	return cors.New(cfg)
}
