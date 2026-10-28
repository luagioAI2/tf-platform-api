package handle

import (
	"context"

	"tf-api/internal/app/service/async"
	"tf-api/pkg/cjson"
	"tf-api/pkg/logger"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"
)

// QueueSystemNoop 占位任务类型（接入业务后删除）
const QueueSystemNoop = "system:noop"

// NoopPayload 占位任务负载
type NoopPayload struct {
	Message string `json:"message"`
}

// NewSystemNoopQueue 构造占位任务
func NewSystemNoopQueue(msg string) (*asynq.Task, error) {
	payload, err := cjson.Cjson.Marshal(NoopPayload{Message: msg})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(QueueSystemNoop, payload, asynq.MaxRetry(3)), nil
}

// NewSystemNoopHandler 构造占位 handler。
// 这是「新增一个 MQ handler」的参考实现：
//  1. 定义 task 常量 + payload 结构
//  2. 提供 NewXxxQueue 生产者函数
//  3. 提供 NewXxxHandler(srv async.IAsyncService) 消费者
//  4. 在 mq/queue.go 里 mux.HandleFunc 注册
func NewSystemNoopHandler(srv async.IAsyncService) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p NoopPayload
		if err := cjson.Cjson.Unmarshal(t.Payload(), &p); err != nil {
			return err
		}
		logger.ZInfo("mq noop handled", zap.String("message", p.Message))
		return srv.Noop()
	}
}
