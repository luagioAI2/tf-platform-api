package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var SystemRepositorySet = wire.NewSet(wire.Struct(new(SystemRepository), "*"))

// SystemRepository 系统级数据访问：健康检查、运行时状态。
// 同时作为「Repository 层写法」的参考实现。
type SystemRepository struct {
	DB  *gorm.DB
	RDS *redis.Client
}

// PingDB 检查数据库连通性
func (r *SystemRepository) PingDB(ctx context.Context) error {
	if r.DB == nil {
		return fmt.Errorf("db not initialized")
	}
	sqlDB, err := r.DB.DB()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return sqlDB.PingContext(ctx)
}

// PingRedis 检查 Redis 连通性
func (r *SystemRepository) PingRedis(ctx context.Context) error {
	if r.RDS == nil {
		return fmt.Errorf("redis not initialized")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return r.RDS.Ping(ctx).Err()
}

// DBStats 返回连接池状态
func (r *SystemRepository) DBStats() map[string]interface{} {
	if r.DB == nil {
		return map[string]interface{}{"initialized": false}
	}
	sqlDB, err := r.DB.DB()
	if err != nil {
		return map[string]interface{}{"initialized": false, "error": err.Error()}
	}
	s := sqlDB.Stats()
	return map[string]interface{}{
		"initialized":   true,
		"open":          s.OpenConnections,
		"in_use":        s.InUse,
		"idle":          s.Idle,
		"max_open":      s.MaxOpenConnections,
		"wait_count":    s.WaitCount,
		"wait_duration": s.WaitDuration.String(),
	}
}
