// Command seed 是数据初始化工具。
//
// 用途：
//   - 写入游戏种子数据
//   - 给指定用户充值积分/钻石（开发测试用）
//
// 用法：
//
//	go run ./cmd/seed -c configs/config.yaml games
//	go run ./cmd/seed -c configs/config.yaml grant -uid 2 -points 1000
//	go run ./cmd/seed -c configs/config.yaml grant -mobile 13900139000 -diamond 100
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"tf-api/internal/app/config"
	"tf-api/internal/app/entities"
	"tf-api/internal/app/seed"
	"tf-api/internal/app/service/repository"
	"tf-api/pkg/logger"

	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func main() {
	conf := flag.String("c", "configs/config.yaml", "config file")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: seed [-c config] <games|grant> [flags]")
		os.Exit(1)
	}

	config.MustLoad(*conf)
	logger.GetLogger()

	db := openDB()
	ctx := context.Background()

	switch args[0] {
	case "games":
		if err := seed.EnsureGames(ctx, repository.NewGameRepository(db)); err != nil {
			logger.ZFatal("seed games failed", zap.Error(err))
		}
		fmt.Println("games seeded")

	case "grant":
		fs := flag.NewFlagSet("grant", flag.ExitOnError)
		uid := fs.Uint("uid", 0, "user id")
		mobile := fs.String("mobile", "", "user mobile")
		points := fs.Int64("points", 0, "points to grant")
		coupon := fs.Int64("coupon", 0, "coupon to grant")
		diamond := fs.Int64("diamond", 0, "diamond to grant")
		_ = fs.Parse(args[1:])

		userRepo := repository.NewUserRepository(db)
		var target *entities.User
		var err error
		if *uid > 0 {
			target, err = userRepo.GetByID(ctx, uint(*uid))
		} else if *mobile != "" {
			target, err = userRepo.GetByMobile(ctx, *mobile)
		} else {
			logger.ZFatal("grant requires -uid or -mobile")
		}
		if err != nil || target == nil {
			logger.ZFatal("user not found", zap.Error(err))
		}

		walletRepo := repository.NewWalletRepository(db)
		changes := []struct {
			cur   entities.Currency
			delta int64
			memo  string
		}{
			{entities.CurrencyPoints, *points, "后台充值（开发工具）"},
			{entities.CurrencyCoupon, *coupon, "后台充值（开发工具）"},
			{entities.CurrencyDiamond, *diamond, "后台充值（开发工具）"},
		}
		for _, c := range changes {
			if c.delta == 0 {
				continue
			}
			bal, _, err := walletRepo.ApplyChange(ctx, repository.BalanceChange{
				UID:            target.ID,
				Currency:       c.cur,
				Delta:          c.delta,
				Source:         entities.SourceManualAdjust,
				Memo:           c.memo,
				IdempotencyKey: fmt.Sprintf("seed:%d:%s:%d", target.ID, c.cur, time.Now().UnixNano()),
			})
			if err != nil {
				logger.ZFatal("grant failed", zap.String("currency", string(c.cur)), zap.Error(err))
			}
			fmt.Printf("granted %s +%d, balance=%d\n", c.cur, c.delta, bal)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		os.Exit(1)
	}
}

// openDB 按配置打开数据库
func openDB() *gorm.DB {
	s := config.Get().DBSettings
	cfg := &gorm.Config{
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	}

	var (
		db  *gorm.DB
		err error
	)
	if s.Driver == "sqlite" || s.Driver == "sqlite3" {
		db, err = gorm.Open(sqlite.Open(s.DataSource), cfg)
	} else {
		db, err = gorm.Open(mysql.Open(s.DataSource), cfg)
	}
	if err != nil {
		logger.ZFatal("connect db failed", zap.Error(err))
	}
	return db
}
