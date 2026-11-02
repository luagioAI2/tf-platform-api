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

	// 游戏通用存储（存档 KV + 游戏币）
	GameKvRepositorySet,
	GameCoinRepositorySet,

	// 存档快照与回档
	GameKvSnapshotRepositorySet,
	GameKvRestoreLogRepositorySet,

	// 任务系统
	TaskRepositorySet,
	CheckInRepositorySet,

	// 农场
	FarmStateRepositorySet,

	// 兑换商城
	RedeemRepositorySet,

	// 心愿商城
	WishRepositorySet,

	// 游戏版本分发
	GameReleaseRepositorySet,

	// 招商
	MerchantRepositorySet,
)
