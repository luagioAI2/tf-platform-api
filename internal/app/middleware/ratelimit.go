package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"tf-api/internal/app/errors"
	"tf-api/internal/app/ginx"
	"tf-api/pkg/metrics"
	"tf-api/pkg/rds"

	"github.com/gin-gonic/gin"
)

// RateLimitConfig 限流配置
type RateLimitConfig struct {
	// Name 限流器名称（用于指标与 key 前缀）
	Name string
	// Limit 窗口内允许的请求数
	Limit int
	// Window 窗口大小
	Window time.Duration
	// KeyFunc 自定义限流维度，默认按客户端 IP
	KeyFunc func(*gin.Context) string
	// FailOpen Redis 不可用时是否放行（默认 true，避免限流组件故障拖垮全站）
	FailOpen bool
}

// RateLimitMiddleware 分布式限流（Redis 滑动窗口）。
//
// 多实例部署时各实例共享计数，不会出现「N 个实例 = N 倍限额」的问题。
// Redis 不可用时默认 fail-open（放行），由上游 WAF/网关兜底。
//
// 用法：
//
//	r.POST("/login", middleware.RateLimitMiddleware(rds.NewRateLimiter(nil), middleware.RateLimitConfig{
//	    Name: "login", Limit: 10, Window: time.Minute,
//	}), handler)
func RateLimitMiddleware(limiter *rds.RateLimiter, cfg RateLimitConfig) gin.HandlerFunc {
	if cfg.Limit <= 0 {
		cfg.Limit = 60
	}
	if cfg.Window <= 0 {
		cfg.Window = time.Minute
	}
	if cfg.KeyFunc == nil {
		cfg.KeyFunc = func(c *gin.Context) string { return ginx.GetClientIP(c) }
	}

	return func(c *gin.Context) {
		// Redis 未配置：降级为本地限流，保证单机仍有保护
		if limiter == nil {
			if !localLimiter.allow(cfg.Name+":"+cfg.KeyFunc(c), cfg.Limit, cfg.Window) {
				rejectRateLimit(c, cfg.Name)
				return
			}
			c.Next()
			return
		}

		key := cfg.Name + ":" + cfg.KeyFunc(c)
		ok, _, err := limiter.Allow(c.Request.Context(), key, cfg.Limit, cfg.Window)
		if err != nil && !cfg.FailOpen {
			// 非 fail-open 模式：Redis 故障直接拒绝，保护后端
			rejectRateLimit(c, cfg.Name)
			return
		}
		if !ok {
			rejectRateLimit(c, cfg.Name)
			return
		}
		c.Next()
	}
}

func rejectRateLimit(c *gin.Context, name string) {
	metrics.RateLimitRejected.WithLabelValues(name).Inc()
	ginx.ResJSON(c, http.StatusTooManyRequests, &ginx.Resp{
		Code: errors.TooManyRequests,
		Msg:  "too many requests",
	})
	c.Abort()
}

// ============================ 本地限流兜底 ============================

// localLimiter 单实例内存限流（Redis 不可用时兜底）
var localLimiter = &memoryLimiter{buckets: make(map[string]*rlBucket)}

type rlBucket struct {
	count   int
	resetAt time.Time
}

type memoryLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rlBucket
	lastGC  time.Time
}

func (m *memoryLimiter) allow(key string, limit int, window time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	// 惰性 GC：每 30 秒清理一次过期桶，避免后台 goroutine 泄漏
	if now.Sub(m.lastGC) > 30*time.Second {
		for k, b := range m.buckets {
			if now.After(b.resetAt) {
				delete(m.buckets, k)
			}
		}
		m.lastGC = now
	}

	b, ok := m.buckets[key]
	if !ok || now.After(b.resetAt) {
		m.buckets[key] = &rlBucket{count: 1, resetAt: now.Add(window)}
		return true
	}
	if b.count >= limit {
		return false
	}
	b.count++
	return true
}

// ============================ 超时 ============================

// TimeoutExempt 描述一个「用更长的超时」的路径前缀。
//
// 为什么需要：20 秒是给 JSON 接口设的。游戏包上传是**上行流量**，
// 6MB 的包在 150KB/s 的链路上要 40 秒 —— 实测 24 秒时被 504 掐断
// （`{"code":501,"error":"request timeout"}`），而客户端那头只看到「发布失败」。
// 同一个包在局域网里 1.4 秒就传完，所以这个问题**只会在真机/慢网出现**，
// 本地怎么试都是绿的。
type TimeoutExempt struct {
	Prefix   string
	Duration time.Duration
}

// TimeoutMiddleware 为请求设置超时，超时后返回 504。
//
// 注意：Go 的 http.Server 没有内置的「单请求处理超时」，
// 必须靠 context 传递到下游（DB / Redis / HTTP 调用）才能真正中断。
func TimeoutMiddleware(d time.Duration, exempts ...TimeoutExempt) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := d
		path := c.Request.URL.Path
		for _, e := range exempts {
			if e.Prefix != "" && strings.HasPrefix(path, e.Prefix) {
				limit = e.Duration
				break
			}
		}
		if limit <= 0 {
			limit = d
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), limit)
		defer cancel()

		c.Request = c.Request.WithContext(ctx)

		done := make(chan struct{})
		go func() {
			defer func() { _ = recover() }()
			c.Next()
			close(done)
		}()

		select {
		case <-done:
		case <-ctx.Done():
			// 超时：如果 handler 还没写响应，返回 504
			if !c.Writer.Written() {
				ginx.ResJSON(c, http.StatusGatewayTimeout, &ginx.Resp{
					Code: errors.ServerError,
					Msg:  "request timeout",
				})
				c.Abort()
			}
		}
	}
}

// ============================ 请求体限制 ============================

// BodyLimitExempt 描述一个「不吃全局上限」的路径前缀，以及它自己的上限。
//
// 为什么需要它：全局上限是给 JSON 接口设的（4MB 足够）。
// 但游戏包上传是同一条链上的另一个量级（实测 21 款里 13 款 > 4MB），
// 一刀切会把正常业务拦死，而报错只有一句 `10030201`，看不出是「上限」问题。
type BodyLimitExempt struct {
	Prefix   string
	MaxBytes int64 // 0 表示不限制
}

// BodyLimitMiddleware 限制请求体大小（防止大 payload 打爆内存）。
//
// maxBytes 建议 1~10MB。文件上传这类**必然超过**的路径走 `exempts` 单独放宽，
// 不要直接把全局上限抬高 —— 那等于把所有 JSON 接口的防护一起拆掉。
func BodyLimitMiddleware(maxBytes int64, exempts ...BodyLimitExempt) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := maxBytes
		path := c.Request.URL.Path
		for _, e := range exempts {
			if e.Prefix != "" && strings.HasPrefix(path, e.Prefix) {
				limit = e.MaxBytes
				break
			}
		}
		if limit <= 0 {
			c.Next()
			return
		}
		if c.Request.ContentLength > limit {
			/* 把上限写进 msg：原来只有一句 `request body too large`，
			   发布脚本那头只能看到 code=10030201，得回来翻源码才知道是 4MB。
			   实测就是这样卡了半个下午。 */
			ginx.ResJSON(c, http.StatusRequestEntityTooLarge, &ginx.Resp{
				Code: errors.FileTooLarge,
				Msg:  fmt.Sprintf("request body too large: %d bytes > limit %d", c.Request.ContentLength, limit),
			})
			c.Abort()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

// ============================ 安全响应头 ============================

// SecurityHeadersMiddleware 设置通用安全响应头
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-XSS-Protection", "0") // 现代浏览器已废弃，显式关闭避免旧漏洞
		c.Next()
	}
}
