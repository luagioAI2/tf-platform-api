package repository

import (
	"context"
	"time"

	"tf-api/pkg/metrics"

	"gorm.io/gorm"
)

// metricsStartKey 用于在 gorm 语句上暂存开始时间
const metricsStartKey = "metrics_start"

// RegisterMetrics 注册 GORM 回调，采集查询次数与耗时。
//
// 为什么重要：5W 并发下，慢 SQL 是最常见的雪崩原因。
// 有了 db_query_duration_seconds 的 P99，出问题时才能快速定位。
func RegisterMetrics(db *gorm.DB) error {
	if db == nil {
		return nil
	}

	before := func(tx *gorm.DB) {
		tx.InstanceSet(metricsStartKey, time.Now())
	}

	after := func(tx *gorm.DB) {
		v, ok := tx.InstanceGet(metricsStartKey)
		if !ok {
			return
		}
		start, ok := v.(time.Time)
		if !ok {
			return
		}
		metrics.DBQueryDuration.Observe(time.Since(start).Seconds())
		if tx.Error != nil {
			metrics.DBQueriesTotal.WithLabelValues("error").Inc()
		} else {
			metrics.DBQueriesTotal.WithLabelValues("ok").Inc()
		}
	}

	// 显式逐条注册，避免泛型/接口抽象带来的复杂度
	if err := db.Callback().Query().Before("gorm:query").Register("metrics:before_query", before); err != nil {
		return err
	}
	if err := db.Callback().Query().After("gorm:query").Register("metrics:after_query", after); err != nil {
		return err
	}
	if err := db.Callback().Create().Before("gorm:create").Register("metrics:before_create", before); err != nil {
		return err
	}
	if err := db.Callback().Create().After("gorm:create").Register("metrics:after_create", after); err != nil {
		return err
	}
	if err := db.Callback().Update().Before("gorm:update").Register("metrics:before_update", before); err != nil {
		return err
	}
	if err := db.Callback().Update().After("gorm:update").Register("metrics:after_update", after); err != nil {
		return err
	}
	if err := db.Callback().Delete().Before("gorm:delete").Register("metrics:before_delete", before); err != nil {
		return err
	}
	if err := db.Callback().Delete().After("gorm:delete").Register("metrics:after_delete", after); err != nil {
		return err
	}

	return nil
}

// WithTimeout 给单次查询加超时（供 Repository 复用）
func WithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}
