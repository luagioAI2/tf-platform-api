package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tf-api/internal/app/config"
	"tf-api/internal/app/seed"
	"tf-api/internal/app/service"
	"tf-api/internal/app/service/async"
	"tf-api/internal/app/service/cache"
	"tf-api/internal/app/service/repository"
	"tf-api/pkg/logger"
	"tf-api/pkg/rds"

	"github.com/glebarez/sqlite"
	"github.com/google/wire"
	redis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
	"gorm.io/plugin/dbresolver"
)

// ProviderSet 基础设施 provider 集合
var ProviderSet = wire.NewSet(
	provideMysql,
	provideRDS,
	provideCacheAdapter,
	provideRateLimiter,
	provideService,

	// 缓存工厂（L1 本地 + L2 Redis + singleflight）
	cache.CacheSet,
)

// provideRateLimiter 创建分布式限流器。
// Redis 为 nil 时返回 nil，中间件会自动降级为本地限流。
func provideRateLimiter(client *redis.Client) *rds.RateLimiter {
	if client == nil {
		return nil
	}
	return rds.NewRateLimiter(client)
}

// provideService 把 AsyncServiceManager 绑定到 IAsyncService 接口
func provideService(srv *service.AsyncServiceManager) async.IAsyncService {
	return srv
}

// provideCacheAdapter 把 *redis.Client 适配成 pkg/cache 需要的 RedisClient。
// Redis 为 nil 时返回 nil，缓存自动降级为纯本地。
func provideCacheAdapter(client *redis.Client) *rds.RedisCache {
	if client == nil {
		return nil
	}
	return rds.NewRedisCache(client)
}

// gormLogLevel 把配置字符串映射为 gorm 日志级别
func gormLogLevel(s config.DBSettings) gormlogger.LogLevel {
	if s.Trace {
		return gormlogger.Info
	}
	switch s.LogLevel {
	case "info":
		return gormlogger.Info
	case "warn":
		return gormlogger.Warn
	case "error":
		return gormlogger.Error
	default:
		return gormlogger.Silent
	}
}

// provideMysql 建立 GORM 连接。
// Driver 支持 mysql / sqlite：
//   - mysql：生产用，支持主从读写分离
//   - sqlite：本地开发用（纯 Go 驱动，无需 CGO），开箱即跑
func provideMysql() *gorm.DB {
	s := config.Get().DBSettings

	gormCfg := &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormLogLevel(s)),
		NamingStrategy: schema.NamingStrategy{
			SingularTable: true, // 表名不加复数：user / order
		},
		SkipDefaultTransaction: true,
		// 关闭自动 ping，避免每次查询多一次往返
		DisableAutomaticPing: true,
	}

	var (
		db  *gorm.DB
		err error
	)

	switch s.Driver {
	case "sqlite", "sqlite3":
		dsn := s.DataSource
		if dsn == "" {
			dsn = "./data/tf.db"
		}
		// 确保 db 文件所在目录存在（:memory: 与绝对路径除外）
		if dsn != ":memory:" && !strings.Contains(dsn, "mode=memory") {
			if dir := filepath.Dir(dsn); dir != "" && dir != "." {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					panic(fmt.Errorf("create sqlite dir %s failed: %w", dir, err))
				}
			}
		}
		db, err = gorm.Open(sqlite.Open(dsn), gormCfg)
		if err != nil {
			panic(fmt.Errorf("connect sqlite failed: %w", err))
		}
		logger.ZInfo("database connected", zap.String("driver", "sqlite"), zap.String("dsn", dsn))
	default: // mysql
		db, err = gorm.Open(mysql.New(mysql.Config{
			DSN:                       s.DataSource,
			DefaultStringSize:         256,
			DisableDatetimePrecision:  true,
			DontSupportRenameIndex:    true,
			DontSupportRenameColumn:   true,
			SkipInitializeWithVersion: false,
		}), gormCfg)
		if err != nil {
			panic(fmt.Errorf("connect mysql failed: %w", err))
		}
		logger.ZInfo("database connected", zap.String("driver", "mysql"))
	}

	sqlDB, err := db.DB()
	if err != nil {
		panic(fmt.Errorf("get sql.DB failed: %w", err))
	}
	maxIdle, maxOpen := s.MaxIdleConns, s.MaxOpenConns
	if maxIdle <= 0 {
		maxIdle = 10
	}
	if maxOpen <= 0 {
		maxOpen = 100
	}
	lifetime := time.Duration(s.ConnMaxLifetimeSeconds) * time.Second
	if lifetime <= 0 {
		lifetime = time.Hour
	}
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetConnMaxLifetime(lifetime)
	// 连接最大空闲时间：避免 MySQL 侧 wait_timeout 断开后拿到坏连接
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)

	// 读写分离（仅 mysql 且配置了从库时启用）
	if s.Driver != "sqlite" && (len(s.DataSourceReplicas) > 0 || len(s.DataSourceSearchReplicas) > 0) {
		writers := make([]gorm.Dialector, 0, len(s.DataSourceReplicas))
		for _, dsn := range s.DataSourceReplicas {
			writers = append(writers, mysql.Open(dsn))
		}
		readers := make([]gorm.Dialector, 0, len(s.DataSourceSearchReplicas))
		for _, dsn := range s.DataSourceSearchReplicas {
			readers = append(readers, mysql.Open(dsn))
		}
		if len(writers) == 0 {
			writers = append(writers, mysql.Open(s.DataSource))
		}
		if err := db.Use(dbresolver.Register(dbresolver.Config{
			Sources:  writers,
			Replicas: readers,
			Policy:   dbresolver.RandomPolicy{},
		}).
			SetMaxIdleConns(maxIdle).
			SetMaxOpenConns(maxOpen).
			SetConnMaxLifetime(lifetime)); err != nil {
			panic(fmt.Errorf("register dbresolver failed: %w", err))
		}
		logger.ZInfo("read-write splitting enabled",
			zap.Int("writers", len(writers)),
			zap.Int("readers", len(readers)))
	}

	// 自动建表：仅开发/测试环境生效
	if s.EnableAutoMigrate {
		if config.Get().ServiceSettings.IsProduction() {
			logger.ZWarn("EnableAutoMigrate ignored in production, use migrations/ instead")
		} else if err := repository.AutoMigrate(db); err != nil {
			panic(fmt.Errorf("auto migrate failed: %w", err))
		} else {
			logger.ZInfo("auto migrate done")
		}
	}

	// 种子数据：仅在开发/测试环境自动写入，避免污染生产
	if !config.Get().ServiceSettings.IsProduction() {
		gameRepo := repository.NewGameRepository(db)
		if err := seed.EnsureGames(context.Background(), gameRepo); err != nil {
			logger.ZWarn("seed games failed", zap.Error(err))
		}
		// 心愿商品目录依赖游戏列表（盲盒池 + 游戏盒子都从它推导），
		// 所以必须**在 EnsureGames 之后**执行
		if err := seed.EnsureWishCatalog(context.Background(), repository.NewWishRepository(db), gameRepo); err != nil {
			logger.ZWarn("seed wish catalog failed", zap.Error(err))
		}
		// 官方任务 / 兑换模板：只在「该 kind 还没有官方模板」时写，
		// 运营改过之后重启不许覆盖（见 seed/templates.go）。
		if err := seed.EnsureTemplates(context.Background(), db); err != nil {
			logger.ZWarn("seed official templates failed", zap.Error(err))
		}
	}

	// 注册 SQL 指标（在 migrate 之后，避免建表语句污染指标）
	if err := repository.RegisterMetrics(db); err != nil {
		logger.ZWarn("register db metrics failed", zap.Error(err))
	}

	return db
}

// provideRDS 建立 Redis 连接。
// 开发环境连不上时降级为 nil（相关功能自动跳过），生产环境直接失败。
func provideRDS() *redis.Client {
	s := config.Get().RDBSettings
	env := config.Get().ServiceSettings

	poolSize := s.PoolSize
	if poolSize <= 0 {
		poolSize = 20
	}
	minIdle := s.MinIdleConns
	if minIdle <= 0 {
		minIdle = 5
	}
	// 关闭 go-redis 的内部日志（默认会往 stderr 刷连接失败），
	// 改由本项目的 zap 统一记录，避免日志刷屏。
	// 注意：go-redis 没有直接提供 DisableLogging 选项，只能设置极小的
	// MaxRetries 并配合下面的 SetLogger 来抑制。
	client := redis.NewClient(&redis.Options{
		Addr:         s.DataSource,
		Password:     s.Password,
		DB:           s.DB,
		PoolSize:     poolSize,
		MinIdleConns: minIdle,
		PoolTimeout:  time.Duration(s.PoolTimeout) * time.Second,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		MaxRetries:   0, // 不重试，快速失败
	})
	// 把 go-redis 内部日志重定向到 zap（降级为 debug，不污染错误日志）
	redis.SetLogger(redisLogger{})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		if env.IsProduction() {
			// 生产环境不允许降级：缓存/限流/分布式锁都依赖 Redis，
			// 带病运行会导致限流失效、并发写冲突，比直接失败更危险。
			logger.ZFatal("connect redis failed (required in production)",
				zap.String("addr", s.DataSource),
				zap.Error(err))
		}
		logger.ZWarn("redis unavailable, running in degraded mode (cache/lock disabled)",
			zap.String("addr", s.DataSource),
			zap.Error(err))
		_ = client.Close()
		// 显式清空全局客户端，保证 rds.GetClient() 返回 nil 而不是旧值
		rds.SetClient(nil)
		return nil
	}
	// 注册为全局客户端，供 pkg/rds 的锁/限流/幂等使用
	rds.SetClient(client)
	return client
}
