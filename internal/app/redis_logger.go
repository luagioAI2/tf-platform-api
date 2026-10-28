package internal

import (
	"context"
	"fmt"

	"tf-api/pkg/logger"

	"go.uber.org/zap"
)

// redisLogger 把 go-redis 的内部日志接到 zap。
//
// 为什么需要：go-redis 在连接失败时会直接往标准错误输出打印
// "redis: connection pool: failed to dial..."，在 Redis 不可用时
// 会以极高频刷屏，淹没真正的业务日志。
// 这里统一降级为 debug 级别，只在需要排查时开启。
type redisLogger struct{}

// Printf 实现 go-redis 的 internal.Logging 接口
func (redisLogger) Printf(ctx context.Context, format string, v ...interface{}) {
	logger.ZDebug(fmt.Sprintf(format, v...), zap.String("component", "redis"))
}
