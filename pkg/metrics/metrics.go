// Package metrics 提供 Prometheus 指标。
//
// 为什么必须要有：5W 并发的系统，出问题时如果只能看日志，
// 定位一个 P99 抖动要花几小时。指标是最低成本的观测手段。
package metrics

import (
	"strconv"
	"sync"
	"time"

	"tf-api/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

var (
	// HTTPRequestsTotal 请求总数（按 method/path/status 维度）
	HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tf",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "HTTP 请求总数",
		},
		[]string{"method", "path", "status"},
	)

	// HTTPDuration 请求耗时分布
	HTTPDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "tf",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP 请求耗时（秒）",
			// 覆盖 1ms ~ 10s，适配 API 服务的典型分布
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"method", "path"},
	)

	// HTTPInFlight 当前处理中的请求数
	HTTPInFlight = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "tf",
			Subsystem: "http",
			Name:      "in_flight_requests",
			Help:      "当前处理中的请求数",
		},
	)

	// HTTPRequestBodyBytes 请求体大小
	HTTPRequestBodyBytes = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "tf",
			Subsystem: "http",
			Name:      "request_body_bytes",
			Help:      "请求体大小（字节）",
			Buckets:   prometheus.ExponentialBuckets(256, 4, 8), // 256B ~ 4MB
		},
		[]string{"method"},
	)

	// CacheHits 缓存命中
	CacheHits = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tf",
			Subsystem: "cache",
			Name:      "hits_total",
			Help:      "缓存命中次数",
		},
		[]string{"cache", "layer"}, // layer: local | redis
	)

	// CacheMisses 缓存未命中
	CacheMisses = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tf",
			Subsystem: "cache",
			Name:      "misses_total",
			Help:      "缓存未命中次数",
		},
		[]string{"cache"},
	)

	// DBQueriesTotal 数据库查询数
	DBQueriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tf",
			Subsystem: "db",
			Name:      "queries_total",
			Help:      "数据库查询总数",
		},
		[]string{"result"}, // ok | error
	)

	// DBQueryDuration 数据库查询耗时
	DBQueryDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "tf",
			Subsystem: "db",
			Name:      "query_duration_seconds",
			Help:      "数据库查询耗时（秒）",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
		},
	)

	// EventsTotal 事件总线
	EventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tf",
			Subsystem: "event",
			Name:      "total",
			Help:      "事件总数",
		},
		[]string{"event_type", "result"}, // result: published | dropped | panicked
	)

	// RateLimitRejected 被限流拒绝的请求
	RateLimitRejected = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tf",
			Subsystem: "ratelimit",
			Name:      "rejected_total",
			Help:      "被限流拒绝的请求数",
		},
		[]string{"name"},
	)

	// ExternalCalls 外部调用（三方 API）
	ExternalCalls = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tf",
			Subsystem: "external",
			Name:      "calls_total",
			Help:      "外部调用总数",
		},
		[]string{"service", "result"}, // result: ok | error | circuit_open
	)
)

// registerOnce 保证只注册一次（测试中多次 NewRouter 不会 panic）
var registerOnce sync.Once

// Register 注册所有指标（幂等，重复调用安全）
func Register() {
	registerOnce.Do(func() {
		// 自定义指标
		for _, c := range []prometheus.Collector{
			HTTPRequestsTotal,
			HTTPDuration,
			HTTPInFlight,
			HTTPRequestBodyBytes,
			CacheHits,
			CacheMisses,
			DBQueriesTotal,
			DBQueryDuration,
			EventsTotal,
			RateLimitRejected,
			ExternalCalls,
		} {
			// 用 Register 而非 MustRegister：已注册时忽略，避免重复注册 panic
			if err := prometheus.Register(c); err != nil {
				if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
					// 非「已注册」错误才记录（避免启动失败）
					logger.ZWarn("register metric failed", zap.Error(err))
				}
			}
		}

		// Go 运行时与进程指标。
		// 注意：prometheus.DefaultRegisterer 默认已包含 Go/Process collector，
		// 这里先尝试注销再注册，兼容被外部注册过的场景。
		for _, c := range []prometheus.Collector{
			collectors.NewGoCollector(),
			collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		} {
			if err := prometheus.Register(c); err != nil {
				if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
					logger.ZWarn("register runtime metric failed", zap.Error(err))
				}
			}
		}
	})
}

// Handler 返回 /metrics 的 handler
func Handler() gin.HandlerFunc {
	return gin.WrapH(promhttp.Handler())
}

// GinMiddleware 记录 HTTP 指标。
//
// 注意：path 使用路由模板（c.FullPath()）而非真实路径，
// 否则 /order/123 与 /order/456 会产生两个时间序列，导致基数爆炸。
func GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		HTTPInFlight.Inc()
		defer HTTPInFlight.Dec()

		if c.Request.ContentLength > 0 {
			HTTPRequestBodyBytes.WithLabelValues(c.Request.Method).Observe(float64(c.Request.ContentLength))
		}

		c.Next()

		// 必须在 c.Next() 之后取，此时路由已匹配
		path := c.FullPath()
		if path == "" {
			path = "unmatched"
		}

		status := strconv.Itoa(c.Writer.Status())
		HTTPRequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
		HTTPDuration.WithLabelValues(c.Request.Method, path).Observe(time.Since(start).Seconds())
	}
}
