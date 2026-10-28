package service

import (
	"context"
	"fmt"
	"time"

	"tf-api/internal/app/service/repository"
	"tf-api/pkg/logger"
	"tf-api/pkg/rds"

	"github.com/google/wire"
	"go.uber.org/zap"
)

var SystemServiceSet = wire.NewSet(ProvideSystemService)

// SystemService 系统状态查询（健康检查、运行时信息）
type SystemService struct {
	Repo *repository.SystemRepository
}

func ProvideSystemService(repo *repository.SystemRepository) *SystemService {
	return &SystemService{Repo: repo}
}

// HealthStatus 健康检查结果
type HealthStatus struct {
	Status   string                 `json:"status"` // ok | degraded
	Version  string                 `json:"version"`
	Env      string                 `json:"env"`
	Uptime   string                 `json:"uptime"`
	Checks   map[string]string      `json:"checks"`
	Database map[string]interface{} `json:"database,omitempty"`
}

// 依赖状态取值
const (
	CheckUp       = "up"
	CheckDown     = "down"
	CheckDisabled = "disabled" // 未配置/降级跳过，不影响整体状态

	CheckOK       = "ok"
	CheckDegraded = "degraded"
)

var startedAt = time.Now()

// Health 执行依赖探活。
//
// 状态判定：
//   - 任一依赖 down   -> degraded（HTTP 返回 503，便于 LB / k8s 摘除节点）
//   - 依赖为 disabled -> 不影响状态（如开发环境未启动 Redis）
func (s *SystemService) Health(ctx context.Context, version, env string) *HealthStatus {
	checks := make(map[string]string, 2)

	// 数据库
	if s.Repo == nil || s.Repo.DB == nil {
		checks["database"] = CheckDisabled
	} else if err := s.Repo.PingDB(ctx); err != nil {
		checks["database"] = CheckDown
		logger.ZWarn("health check: database down", zap.Error(err))
	} else {
		checks["database"] = CheckUp
	}

	// Redis（未配置时视为 disabled，不算故障）
	if s.Repo == nil || s.Repo.RDS == nil {
		checks["redis"] = CheckDisabled
	} else if err := s.Repo.PingRedis(ctx); err != nil {
		checks["redis"] = CheckDown
		logger.ZWarn("health check: redis down", zap.Error(err))
	} else {
		checks["redis"] = CheckUp
	}

	status := CheckOK
	for _, v := range checks {
		if v == CheckDown {
			status = CheckDegraded
			break
		}
	}

	var dbStats map[string]interface{}
	if s.Repo != nil {
		dbStats = s.Repo.DBStats()
	}

	return &HealthStatus{
		Status:   status,
		Version:  version,
		Env:      env,
		Uptime:   time.Since(startedAt).Truncate(time.Second).String(),
		Checks:   checks,
		Database: dbStats,
	}
}

// WithLock 分布式锁的标准用法（供业务 Service 参考）
func (s *SystemService) WithLock(ctx context.Context, key string, ttl time.Duration, fn func() error) error {
	return rds.WithLock(ctx, key, ttl, fn)
}

// ErrNotImplemented 占位错误
func ErrNotImplemented(name string) error {
	return fmt.Errorf("%s not implemented", name)
}
