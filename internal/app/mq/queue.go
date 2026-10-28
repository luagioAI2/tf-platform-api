package mq

import (
	"tf-api/internal/app/config"
	"tf-api/internal/app/mq/handle"
	"tf-api/internal/app/service/async"

	"tf-api/pkg/logger"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"
)

// MClient 全局队列客户端（未启用 MQ 时为 nil，Enqueue 直接返回）
var MClient *asynq.Client

// Start 启动 asynq 服务端（生产消费）与客户端（生产投递）
func Start(service async.IAsyncService) {
	setting := config.Get().RDBSettings
	redisOpt := asynq.RedisClientOpt{
		Addr:     setting.DataSource,
		Password: setting.Password,
		DB:       setting.DB,
	}
	initClient(redisOpt)
	go initListen(redisOpt, service)
}

func initClient(redisOpt asynq.RedisClientOpt) {
	MClient = asynq.NewClient(redisOpt)
}

func initListen(redisOpt asynq.RedisClientOpt, service async.IAsyncService) {
	defer func() {
		if r := recover(); r != nil {
			logger.ZError("Recovered in MQ initListen", zap.Any("error", r))
		}
	}()

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: 10,
		Queues: map[string]int{
			"critical": 6,
			"default":  3,
			"low":      1,
		},
		Logger: logger.GetLogger().Sugar(),
	})

	mux := asynq.NewServeMux()
	mux.HandleFunc(handle.QueueSystemNoop, handle.NewSystemNoopHandler(service))
	// TODO: 在此注册业务 handler

	if err := srv.Run(mux); err != nil {
		logger.ZError("MQ server run failed", zap.Error(err))
	}
}

// Enqueue 投递任务
func Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if MClient == nil {
		return nil, nil
	}
	return MClient.Enqueue(task, opts...)
}

// Close 关闭客户端
func Close() {
	if MClient != nil {
		_ = MClient.Close()
	}
}
