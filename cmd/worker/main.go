// Command worker 是只运行 MQ 消费者与 Cron 定时任务的进程。
//
// 为什么要把 worker 从 HTTP 进程拆出来：
//   - HTTP 服务需要横向扩容到 N 个副本承接流量
//   - Cron 任务只需要跑 1 份，N 份会重复执行（如重复发奖、重复清理）
//   - MQ 消费者虽然可以多副本，但与 HTTP 混部会互相抢占资源
//
// 部署方式：
//   - HTTP：kubectl replicas=5，环境变量 TF_ENABLE_TASK=false
//   - Worker：kubectl replicas=1（或 Deployment + 单副本），只跑本进程
//
// 用法：
//
//	go run ./cmd/worker -c configs/config.yaml
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	app "tf-api/internal/app"
	"tf-api/internal/app/buildinfo"
	"tf-api/internal/app/config"
	"tf-api/internal/app/i18n"
	"tf-api/internal/app/mq"
	"tf-api/internal/app/task"
	"tf-api/pkg/logger"

	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
)

// VERSION 版本号
var VERSION = "dev"

func main() {
	cliApp := cli.NewApp()
	cliApp.Name = "tf-worker"
	cliApp.Version = VERSION
	cliApp.Usage = "tf-api background worker (mq consumer + cron)"
	cliApp.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "conf",
			Aliases: []string{"c"},
			Usage:   "config file path",
			Value:   "configs/config.yaml",
		},
	}
	cliApp.Action = func(c *cli.Context) error {
		return run(c.String("conf"))
	}

	if err := cliApp.Run(os.Args); err != nil {
		log.Fatalf("worker error: %v", err)
	}
}

func run(configFile string) error {
	config.MustLoad(configFile)
	cfg := config.Get()

	logger.GetLogger()
	logger.ZInfo("worker starting",
		zap.String("version", VERSION),
		zap.String("env", cfg.ServiceSettings.Environment))

	if err := i18n.InitI18n(cfg.ServiceSettings.Language); err != nil {
		return err
	}
	if cfg.ServiceSettings.Timezone != "" {
		if loc, err := time.LoadLocation(cfg.ServiceSettings.Timezone); err == nil {
			time.Local = loc
		}
	}

	// 只构建依赖，不启动 HTTP
	injector, clean, err := app.BuildInjector(buildinfo.Info{
		Version: VERSION,
		Env:     cfg.ServiceSettings.Environment,
	})
	if err != nil {
		return err
	}
	_ = injector

	// MQ 消费者
	if cfg.ServiceSettings.EnableMQ {
		mq.Start(injector.Service)
		logger.ZInfo("mq consumer started")
	} else {
		logger.ZWarn("EnableMQ=false, mq consumer not started")
	}

	// Cron
	if cfg.ServiceSettings.EnableTask {
		if err := task.Start(injector.Service); err != nil {
			return err
		}
		logger.ZInfo("cron started")
	} else {
		logger.ZWarn("EnableTask=false, cron not started")
	}

	// 等待退出信号
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	sig := <-sc
	logger.ZInfo("worker receive signal", zap.String("signal", sig.String()))

	mq.Close()
	clean()
	logger.ZInfo("worker stopped")
	return nil
}
