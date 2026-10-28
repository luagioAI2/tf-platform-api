package entities

// ============================ 游戏存档（通用 KV） ============================

// GameKv 游戏存档键值表。
//
// ## 为什么是 KV 而不是结构化表
//
// 调研了 kid-quest-farm-2d 的 21 款游戏，结论：**所有游戏都走同一套
// `host.storage.get/set/list`，落到一张通用表**。
//
// 实测各游戏用的 key：
//   - prizepark / cellguard / doodlederby / guardshift / hoopsneak / redbutton: "save"
//   - cloudfarm: "settings"
//   - claw3d: "prefs:" + 动态后缀
//   - 其余：动态 key 或封装在闭包里
//
// 值是**任意 JSON**，服务端无法也不应该理解它的结构。游戏会持续新增，
// 服务端不能每加一款就改 schema —— 所以必须用 KV。
//
// ## 命名空间
//
// `(uid, game_id, key)` 三元组唯一。这与前端 `gameKvKey(gameId, key)`
// 的 `${gameId}:${key}` 语义一致，只是把 uid 也纳入了隔离。
//
// ## 保留键
//
// `__` 开头是宿主保留键（`__coin` 币账本 / `__bundle` 包体 / `__assets` 资源根）。
// 服务端**硬校验**：客户端读写保留键直接返回 403。
// 币账本在服务端是独立表（见 GameCoinLedger），KV 里不存。
type GameKv struct {
	BaseModel

	UID    uint   `gorm:"column:uid;uniqueIndex:uk_uid_game_key;not null" json:"uid"`
	GameID string `gorm:"column:game_id;size:32;uniqueIndex:uk_uid_game_key;index:idx_uid_game;not null" json:"game_id"`
	Key    string `gorm:"column:kv_key;size:128;uniqueIndex:uk_uid_game_key;not null" json:"key"`

	// Value 存档内容（任意 JSON）。用 LONGTEXT 而不是 VARCHAR：
	// 单游戏存档可能几百 KB，MySQL 的 VARCHAR 上限 65535 字节不够。
	Value string `gorm:"column:value;type:LONGTEXT;not null" json:"value"`

	// Size 值的大小（字节），用于配额统计与限流
	Size int64 `gorm:"column:size;default:0" json:"size"`

	// Version 乐观锁版本号：多设备同时写同一 key 时，旧版本会被拒绝
	Version int64 `gorm:"column:version;default:0;not null" json:"version"`
}

func (GameKv) TableName() string { return "game_kv" }

// ============================ 游戏币账本 ============================

// GameCoinLedger 游戏币账本。
//
// ## 为什么不塞进 GameKv
//
// 前端把币账本塞在 `gameKv` 的 `__coin` 键里（值是 `{delta,memo,at}[]`），
// 但服务端**不能照抄**：
//
//	| 维度   | 前端 gameKv        | 服务端需要        |
//	| 并发   | 单用户单设备，够用  | 5W 并发，需要行锁 |
//	| 对账   | 无                 | balance_after 快照|
//	| 幂等   | 不需要             | 需要（网络重试）  |
//	| 查询   | 只读自己           | 排行榜/统计       |
//
// 所以独立成表，字段对齐宿主三套币的账本设计。
//
// ## 不变量（与前端一致）
//
//  1. 余额 = 账本 delta 之和（`balance_after` 是快照，用于对账）
//  2. 游戏币**不能**换回积分
//  3. 币的出口只有「玩」和「换兑换券」
type GameCoinLedger struct {
	BaseModel

	UID    uint   `gorm:"column:uid;index:idx_uid_game_coin;not null" json:"uid"`
	GameID string `gorm:"column:game_id;size:32;index:idx_uid_game_coin;not null" json:"game_id"`

	// Delta 变动量：正数收入，负数消费
	Delta int64 `gorm:"column:delta;not null" json:"delta"`
	// BalanceAfter 变动后余额（快照，用于对账）
	BalanceAfter int64 `gorm:"column:balance_after;not null" json:"balance_after"`

	// Source 来源/去向
	Source string `gorm:"column:source;size:32;index;not null" json:"source"`
	// RefID 关联对象（局 ID / 奖品 ID）
	RefID string `gorm:"column:ref_id;size:64;index" json:"ref_id"`
	// Memo 备注
	Memo string `gorm:"column:memo;size:128" json:"memo"`

	// IdempotencyKey 幂等键（NULL 语义，见 LedgerEntry 的说明）
	IdempotencyKey *string `gorm:"column:idempotency_key;size:64;uniqueIndex" json:"-"`
}

func (GameCoinLedger) TableName() string { return "game_coin_ledger" }

// SetIdempotencyKey 设置幂等键（空字符串存 NULL）
func (l *GameCoinLedger) SetIdempotencyKey(key string) {
	if key == "" {
		l.IdempotencyKey = nil
		return
	}
	l.IdempotencyKey = &key
}

// 游戏币来源
const (
	GameCoinSourceBuy    = "game_buy"      // 用积分买币
	GameCoinSourceSpend  = "game_spend"    // 玩一局消费
	GameCoinSourceMint   = "game_mint"     // 插件铸币（只进不出）
	GameCoinSourceRedeem = "game_redeem"   // 换成兑换券
	GameCoinSourceAdjust = "manual_adjust" // 后台调整
)

// ============================ 游戏钱包快照 ============================

// GameWallet 游戏币余额快照（每用户每游戏一条）。
//
// 与 GameCoinLedger 的关系：账本是真相，这里是加速查询的快照。
// 每次变动必须在一个事务内同时更新两者，并用 `balance_after` 校验。
type GameWallet struct {
	BaseModel

	UID    uint   `gorm:"column:uid;uniqueIndex:uk_uid_game_wallet;not null" json:"uid"`
	GameID string `gorm:"column:game_id;size:32;uniqueIndex:uk_uid_game_wallet;not null" json:"game_id"`

	// Balance 当前余额
	Balance int64 `gorm:"column:balance;default:0;not null" json:"balance"`
	// TotalEarned 累计获得（统计用）
	TotalEarned int64 `gorm:"column:total_earned;default:0;not null" json:"total_earned"`
	// TotalSpent 累计消费
	TotalSpent int64 `gorm:"column:total_spent;default:0;not null" json:"total_spent"`

	// Version 乐观锁
	Version int64 `gorm:"column:version;default:0;not null" json:"-"`
}

func (GameWallet) TableName() string { return "game_wallet" }

// ============================ 常量 ============================

const (
	// GameKvReservedPrefix 宿主保留键前缀
	GameKvReservedPrefix = "__"
	// GameKvMaxKeyLen 单键长度上限
	GameKvMaxKeyLen = 128
	// GameKvMaxValueBytes 单值大小上限（1MB）
	//
	// 为什么是 1MB：正常存档是几十 KB，`__bundle`（插件代码）前端存在 KV 里
	// 但服务端不需要存（包体走 CDN）。1MB 足够容纳任何真实存档，
	// 又能挡住恶意大 payload。
	GameKvMaxValueBytes = 1 << 20
	// GameKvMaxKeysPerGame 单游戏单用户最多几个 key
	GameKvMaxKeysPerGame = 64
)
