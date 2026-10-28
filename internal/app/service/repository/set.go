package repository

import "github.com/google/wire"

// RepoSet 所有 Repository 的 wire 集合。
//
// 单独一个文件是为了避免 repo.go 同时承担「Set 声明」和「AutoMigrate」两个职责，
// 也便于新增 Repository 时只改这一处。
var RepoSet = wire.NewSet(
	// 系统
	SystemRepositorySet,

	// 用户与认证
	UserRepositorySet,
	UserOAuthRepositorySet,
	UserTokenRepositorySet,
	SmsCodeRepositorySet,

	// 钱包与账本
	WalletRepositorySet,

	// 游戏与购买
	GameRepositorySet,
)
