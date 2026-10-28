package internal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tf-api/internal/app/buildinfo"
	"tf-api/internal/app/config"
	"tf-api/internal/app/i18n"
	"tf-api/internal/app/mq"
	"tf-api/internal/app/task"
	"tf-api/internal/app/utils"
	"tf-api/pkg/logger"
	"tf-api/pkg/metrics"
	"tf-api/pkg/rds"

	"go.uber.org/zap"
)

type options struct {
	ConfigFile string
	Version    string
}

type Option func(*options)

// SetConfigFile 指定配置文件路径
func SetConfigFile(s string) Option { return func(o *options) { o.ConfigFile = s } }

// SetVersion 指定版本号
func SetVersion(s string) Option { return func(o *options) { o.Version = s } }

// Init 初始化并启动全部组件，返回优雅关闭函数
func Init(ctx context.Context, opts ...Option) (func(), error) {
	defer utils.PrintPanicStack()

	var o options
	for _, opt := range opts {
		opt(&o)
	}

	// 1. 配置（含启动期校验）
	config.MustLoad(o.ConfigFile)
	cfg := config.Get()
	logger.ZInfo("config loaded",
		zap.String("file", o.ConfigFile),
		zap.String("config", config.PrintWithJSON()))

	// 2. 日志
	logger.GetLogger()

	// 3. 指标
	if cfg.ServiceSettings.EnableMetrics {
		metrics.Register()
		logger.ZInfo("metrics registered")
	}

	// 4. 多语言
	if err := i18n.InitI18n(cfg.ServiceSettings.Language); err != nil {
		return nil, fmt.Errorf("init i18n failed: %w", err)
	}

	// 5. 时区
	if cfg.ServiceSettings.Timezone != "" {
		loc, err := time.LoadLocation(cfg.ServiceSettings.Timezone)
		if err != nil {
			return nil, fmt.Errorf("invalid timezone %q: %w", cfg.ServiceSettings.Timezone, err)
		}
		time.Local = loc
		logger.ZInfo("timezone set",
			zap.String("timezone", loc.String()),
			zap.Time("now", time.Now()))
	}

	// 6. 依赖注入
	injector, injectorClean, err := BuildInjector(buildinfo.Info{
		Version: o.Version,
		Env:     cfg.ServiceSettings.Environment,
	})
	if err != nil {
		return nil, fmt.Errorf("build injector failed: %w", err)
	}

	// 7. HTTP
	httpClean := InitHTTPServer(ctx, injector)

	// 8. MQ
	if cfg.ServiceSettings.EnableMQ {
		mq.Start(injector.Service)
	}

	// 9. Cron
	if cfg.ServiceSettings.EnableTask {
		if err := task.Start(injector.Service); err != nil {
			return nil, fmt.Errorf("start cron failed: %w", err)
		}
	}

	// 10. 可关闭的后台服务
	shutdownTimeout := time.Duration(cfg.ServiceSettings.ShutdownTimeoutSeconds) * time.Second
	if shutdownTimeout <= 0 {
		shutdownTimeout = 15 * time.Second
	}

	return func() {
		logger.ZInfo("shutting down...")

		// 关闭顺序：先停止接收新流量，再停后台任务，最后释放连接
		httpClean()
		mq.Close()
		injectorClean()

		if c, ok := injector.Service.(interface{ Close() }); ok {
			c.Close()
		}

		// 关闭 Redis 连接
		if client := rds.GetClient(); client != nil {
			_ = client.Close()
		}

		logger.ZInfo("all services stopped gracefully")
	}, nil
}

// InitHTTPServer 启动 HTTP 服务，返回关闭函数
func InitHTTPServer(ctx context.Context, injector *Injector) func() {
	cfg := config.Get().ServiceSettings
	readHeader, read, write, idle, maxHeader := cfg.HTTPTimeout()

	srv := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           injector.Engine,
		ReadHeaderTimeout: readHeader,
		ReadTimeout:       read,
		WriteTimeout:      write,
		IdleTimeout:       idle,
		MaxHeaderBytes:    maxHeader,
	}

	go func() {
		logger.ZInfo("http server listening",
			zap.String("addr", cfg.ListenAddress),
			zap.Duration("read_header_timeout", readHeader),
			zap.Duration("read_timeout", read),
			zap.Duration("write_timeout", write),
			zap.Duration("idle_timeout", idle))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ZFatal("http server error", zap.Error(err))
		}
	}()

	shutdownTimeout := time.Duration(cfg.ShutdownTimeoutSeconds) * time.Second
	if shutdownTimeout <= 0 {
		shutdownTimeout = 15 * time.Second
	}

	return func() {
		// 优雅关闭：先停止 keep-alive，再等待处理中的请求完成
		srv.SetKeepAlivesEnabled(false)

		shutdownCtx, cancel := context.WithTimeout(ctx, shutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.ZError("http server shutdown error", zap.Error(err))
		}
	}
}

// Run 启动并阻塞等待退出信号
func Run(ctx context.Context, opts ...Option) error {
	state := 1
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	cleanFunc, err := Init(ctx, opts...)
	if err != nil {
		return err
	}

EXIT:
	for {
		sig := <-sc
		logger.ZInfo("receive signal", zap.String("signal", sig.String()))
		switch sig {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			state = 0
			break EXIT
		case syscall.SIGHUP:
			// 忽略挂起信号，保持服务运行
		default:
			break EXIT
		}
	}

	cleanFunc()
	time.Sleep(100 * time.Millisecond)
	os.Exit(state)
	return nil
}
