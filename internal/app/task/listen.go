package task

import (
	"tf-api/internal/app/config"
	"tf-api/internal/app/service/async"
	"time"

	"tf-api/pkg/logger"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

// Start 启动定时任务。
//
// 注意：cron 只在单实例上跑。多副本部署时请把本进程的 EnableTask 设为 false，
// 或单独部署 cmd/worker（见 README）。
func Start(service async.IAsyncService) error {
	loc, err := time.LoadLocation(config.Get().ServiceSettings.Timezone)
	if err != nil {
		return err
	}

	c := cron.New(
		cron.WithLocation(loc),
		cron.WithChain(cron.Recover(cron.DefaultLogger)),
	)

	// TODO: 在此 AddJob 注册业务定时任务
	// 示例：
	// if _, err := c.AddJob("@every 5m", ProcessSampleJob{Srv: service}); err != nil {
	// 	return err
	// }

	logger.ZInfo("cron started", zap.String("timezone", loc.String()))
	go c.Start()
	return nil
}

// Job 定时任务接口（所有 job 实现它）
type Job interface {
	Run()
}

// ProcessSampleJob 示例任务：演示 job 的标准写法
type ProcessSampleJob struct {
	Srv async.IAsyncService
}

func (j ProcessSampleJob) Run() {
	defer func() {
		if r := recover(); r != nil {
			logger.ZError("cron job panic", zap.Any("panic", r))
		}
	}()
	if err := j.Srv.Noop(); err != nil {
		logger.ZError("cron job failed", zap.Error(err))
	}
}
