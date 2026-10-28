package middleware

import (
	"time"

	"tf-api/internal/app/ginx"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// 慢请求阈值：超过则记录完整日志
const slowRequestThreshold = 500 * time.Millisecond

// TraceMiddleware 为每个请求分配 requestID，写入上下文与响应头，并记录访问日志。
// 必须作为全局中间件最早注册，保证后续日志都能带上 requestID。
//
// 高并发下的日志策略：
//   - 正常请求不记录（5W QPS 全量记日志会把磁盘打满）
//   - 只记录：慢请求（>500ms）、4xx、5xx、panic
func TraceMiddleware(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			reqID = uuid.NewString()
			c.Request.Header.Set("X-Request-ID", reqID)
		}
		c.Set("requestID", reqID)
		c.Header("X-Request-ID", reqID)

		start := time.Now()
		c.Next()

		cost := time.Since(start)
		status := c.Writer.Status()

		// 正常且不慢的请求直接跳过
		if cost < slowRequestThreshold && status < 400 {
			return
		}

		fields := []zap.Field{
			zap.String("request_id", reqID),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", status),
			zap.Duration("cost", cost),
			zap.String("ip", ginx.GetClientIP(c)),
			zap.Uint("uid", ginx.Mine(c)),
		}

		// 5xx 用 error 级别，4xx 与慢请求用 warn
		switch {
		case status >= 500:
			log.Error("request failed", fields...)
		default:
			log.Warn("slow or client-error request", fields...)
		}
	}
}

// RecoveryMiddleware 捕获 panic，记录堆栈并返回 500，避免进程崩溃。
// gin.Recovery() 只输出到 stderr，这里改为结构化日志 + 统一响应体。
func RecoveryMiddleware(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic recovered",
					zap.Any("panic", r),
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method),
					zap.String("request_id", c.GetString("requestID")),
					zap.Stack("stack"),
				)
				if !c.Writer.Written() {
					ginx.ResJSON(c, 500, &ginx.Resp{
						Code: 500,
						Msg:  "internal server error",
					})
				}
				c.Abort()
			}
		}()
		c.Next()
	}
}
