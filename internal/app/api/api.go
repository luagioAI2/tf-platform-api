package api

import (
	"github.com/google/wire"
)

// APISet 所有 API Handler 的 wire 集合。
// 新增 API 时：在这里加一行 XxxAPISet。
var APISet = wire.NewSet(
	SystemAPISet,
	AuthAPISet,
	UserAPISet,
	WalletAPISet,
	GameAPISet,
	GameStorageAPISet,
	GameSnapshotAPISet,
	GameCoinAPISet,
	TaskAPISet,
)
