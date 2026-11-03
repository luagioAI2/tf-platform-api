package entities

// ============================ 钱包 ============================

// Wallet 用户钱包（三套币的余额快照）。
//
// ⚠️ 设计原则（来自 kid-quest-farm 的教训）：
// 「余额永远是账本 delta 之和」—— 这里的字段是**快照/缓存**，
// 真正的真相在 LedgerEntry 表。每次变动必须：
//  1. 在一个事务内写一条 LedgerEntry
//  2. 同步更新这里的余额
//  3. 用 `balanceAfter` 做对账校验
//
// 为什么不只存余额：出问题时无法回溯「这 100 分是怎么来的」。
// 为什么不只存账本：每次读余额都要 SUM 全表，5w 并发直接打爆 DB。
//
// 三套币**互不相通**（spec §2.2）：
//
//	积分 ⇸ 兑换券    兑换券 ⇸ 积分    钻石 ⇸ 任何别的币
//
// 唯一例外：兑换商城付款时 1 兑换券 ≡ 1 积分（先用券，差额用积分补）。
type Wallet struct {
	BaseModel

	UID uint `gorm:"column:uid;uniqueIndex;not null" json:"uid"`

	// Points 积分（🪙）—— 完成任务获得，用于买种子/游戏
	Points int64 `gorm:"column:points;default:0;not null" json:"points"`
	// Coupon 兑换券（🎫）—— 农场产出卖出获得，只能用于兑换商城
	Coupon int64 `gorm:"column:coupon;default:0;not null" json:"coupon"`
	// Diamond 钻石（💎）—— 付费/兑换码获得，用于买游戏
	Diamond int64 `gorm:"column:diamond;default:0;not null" json:"diamond"`

	// Version 乐观锁版本号，防止并发更新丢钱
	Version int64 `gorm:"column:version;default:0;not null" json:"-"`
}

func (Wallet) TableName() string { return "wallet" }

// ============================ 账本 ============================

// Currency 币种
type Currency string

const (
	CurrencyPoints  Currency = "points"  // 积分
	CurrencyCoupon  Currency = "coupon"  // 兑换券
	CurrencyDiamond Currency = "diamond" // 钻石
)

// IsValid 币种是否合法
func (c Currency) IsValid() bool {
	switch c {
	case CurrencyPoints, CurrencyCoupon, CurrencyDiamond:
		return true
	}
	return false
}

// LedgerEntry 统一账本（三套币共用一张表，用 currency 区分）。
//
// ⚠️ 与 kid-quest-farm 的差异：
// 原项目**刻意把三套币分成三张表**（ledger / harvestLedger / mallLedger），
// 理由是「让『兑换券不算进积分』由表结构保证，而不是靠每个读余额的地方
// 都记得加过滤」。
//
// 服务端这里用**一张表 + currency 字段 + 强制走 Repository 方法**：
//   - 所有余额查询只能调 `SumByCurrency(ctx, uid, currency)`，
//     不允许业务代码直接拼 SQL
//   - 加 `(uid, currency, id)` 复合索引，SUM 很快
//
// 为什么改：三张表的 schema 完全一样，服务端没有"迁移风险"的顾虑
// （前端 Dexie 才有），单表更好维护、更好做对账和风控。
type LedgerEntry struct {
	BaseModel

	UID      uint     `gorm:"column:uid;index:idx_uid_currency;not null" json:"uid"`
	Currency Currency `gorm:"column:currency;size:16;index:idx_uid_currency;not null" json:"currency"`

	// Delta 变动量：正数收入，负数支出
	Delta int64 `gorm:"column:delta;not null" json:"delta"`
	// BalanceAfter 变动后余额（快照，用于对账）
	BalanceAfter int64 `gorm:"column:balance_after;not null" json:"balance_after"`

	// Source 来源/去向（枚举见下方常量）
	Source string `gorm:"column:source;size:32;index;not null" json:"source"`
	// RefID 关联对象 ID（任务实例 / 订单 / 游戏 ID）
	RefID string `gorm:"column:ref_id;size:64;index" json:"ref_id"`
	// Memo 备注（给孩子看的说明）
	Memo string `gorm:"column:memo;size:128" json:"memo"`

	// IdempotencyKey 幂等键，防止重复记账。
	//
	// ⚠️ 用指针 + NULL 而不是空字符串：
	// MySQL/SQLite 的唯一索引把多个 '' 视为冲突，而大多数账本记录
	// （如任务奖励、农场产出）并不需要幂等键。用 NULL 才能让唯一索引
	// 允许多条"无幂等键"的记录共存（SQL 标准里 NULL != NULL）。
	IdempotencyKey *string `gorm:"column:idempotency_key;size:64;uniqueIndex" json:"-"`
}

// SetIdempotencyKey 设置幂等键（空字符串视为未设置，存 NULL）
func (l *LedgerEntry) SetIdempotencyKey(key string) {
	if key == "" {
		l.IdempotencyKey = nil
		return
	}
	l.IdempotencyKey = &key
}

func (LedgerEntry) TableName() string { return "ledger_entry" }

// ============================ 账本来源 ============================

// 积分来源（LedgerSource 的等价物）
const (
	// 收入
	SourceTaskReward   = "task_reward"   // 任务完成奖励
	SourceCheckIn      = "checkin"       // 签到奖励
	SourceStreakBonus  = "streak_bonus"  // 连击奖励
	SourceTierBonus    = "tier_bonus"    // 阶梯奖励
	SourceManualAdjust = "manual_adjust" // 家长手工调整
	SourceRedeemRefund = "redeem_refund" // 兑换退款
	SourceRegisterGift = "register_gift" // 注册赠送
	SourceInviteReward = "invite_reward" // 邀请奖励

	// 农场（2026-11-02 在线版）
	//
	// ★ 名字和客户端 `src/domain/types.ts` 的 `LedgerSource` / `HarvestSource`
	//   **逐字一致**。账本来源是要给家长看流水的（「卖出 12 券」），
	//   两边各起一套名字就必然有一边翻错 —— 而翻错不会报错，
	//   只会让流水读不懂，家长以为钱花错了地方。
	SourceFarmPlant    = "farm_plant"    // 买种子种下去（扣积分）
	SourceFarmPurchase = "farm_purchase" // 买动物 / 解锁地块（扣积分）
	SourceFarmMarket   = "farm_market"   // 市场卖出产出（得兑换券）

	// 支出
	SourceSeedPurchase   = "seed_purchase"   // 买种子
	SourceAnimalPurchase = "animal_purchase" // 买动物
	SourceGamePurchase   = "game_purchase"   // 买游戏（用钻石）
	SourceGameSpend      = "game_spend"      // 游戏内消费（买游戏币等）
	SourceRedeem         = "redeem"          // 兑换商城消费
	SourceRedeemPoints   = "redeem_points"   // 兑换时用积分补差
	SourceRedeemCoupon   = "redeem_coupon"   // 兑换时用兑换券支付
)

// 兑换券来源
const (
	SourceFarmHarvest   = "farm_harvest"   // 农作物收获卖出
	SourceAnimalProduce = "animal_produce" // 动物产出卖出
	SourceClawPrize     = "claw_prize"     // 奖品兑换（保留兼容）
	SourcePluginMint    = "plugin_mint"    // 插件游戏发券
	// SourceLegacyImport 从本地版结转过来的余额（见 WalletImportReq）。
	// 单独一个来源，家长看流水时能一眼分清「云端挣的」和「本机搬来的」。
	SourceLegacyImport = "legacy_import"
)

// 钻石来源
const (
	SourceDiamondRecharge = "diamond_recharge" // 充值
	SourceRedemptionCode  = "redemption_code"  // 兑换码
)

// 心愿商城来源
//
// 心愿商城**只收兑换券**（不混付积分），所以只有这两个方向：
// 兑换时扣、取消时退。
const (
	SourceWishRedeem = "wish_redeem" // 心愿兑换（扣券）
	SourceWishRefund = "wish_refund" // 取消心愿（退券）
)

// IsIncome 是否为收入
func IsIncome(source string) bool {
	switch source {
	case SourceTaskReward, SourceCheckIn, SourceStreakBonus, SourceTierBonus,
		SourceManualAdjust, SourceRedeemRefund, SourceRegisterGift, SourceInviteReward,
		SourceFarmHarvest, SourceAnimalProduce, SourceClawPrize, SourcePluginMint,
		SourceFarmMarket,
		SourceDiamondRecharge, SourceRedemptionCode, SourceWishRefund:
		return true
	}
	return false
}
