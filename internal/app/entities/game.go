package entities

// ============================ 游戏 ============================

// Game 游戏定义（商城条目）。
//
// 数据来源：kid-quest-farm-2d 的 `src/games/registry.ts`（GAMES）
// 与 `src/games/storefront.ts`（STOREFRONT）。
// 服务端只存"可售卖的游戏元数据"，游戏本体（HTML/JS 包）走静态托管。
type Game struct {
	BaseModel

	// GameID 业务 ID（如 cloudfarm / prizepark），全局唯一
	GameID string `gorm:"column:game_id;size:32;uniqueIndex;not null" json:"game_id"`
	// Name 游戏名
	Name string `gorm:"column:name;size:64;not null" json:"name"`
	// Emoji 图标（列表用图标而非截图，见 storefront.ts 的注释）
	Emoji string `gorm:"column:emoji;size:16" json:"emoji"`
	// Tagline 一句话介绍（陈述句，不写广告词）
	Tagline string `gorm:"column:tagline;size:255" json:"tagline"`

	// Kind 玩法分类：think / action / puzzle ...
	Kind string `gorm:"column:kind;size:16;index" json:"kind"`
	// Tint 图标底色档位：grass / sky / sun / berry
	Tint string `gorm:"column:tint;size:16" json:"tint"`

	// Status available / coming_soon / offline
	Status string `gorm:"column:status;size:16;default:'available';index" json:"status"`

	// PricePoints 积分售价（0 = 免费）
	PricePoints int64 `gorm:"column:price_points;default:0" json:"price_points"`
	// PriceDiamond 钻石售价（0 = 不用钻石）
	PriceDiamond int64 `gorm:"column:price_diamond;default:0" json:"price_diamond"`
	// DefaultOwned 是否默认拥有（首款免费游戏）
	DefaultOwned bool `gorm:"column:default_owned;default:false" json:"default_owned"`

	// VoucherPerCoin 游戏币 → 兑换券的汇率（1 个币换几张券）。**0 = 不开放兑换**。
	//
	// 语义跟插件 manifest 的 `economy.voucherPerCoin` 对齐
	// （见 kid-quest-farm-online 的 `src/domain/pluginEconomy.ts`）：
	//   - 不写 / 写 0 ⇒ 这款游戏**不开放**币换券；
	//   - 写 N ⇒ 1 个币换 N 张券。
	//
	// ★ 为什么汇率在服务端、且**不接受客户端传**：兑换就是印券。
	//   让客户端带汇率，等于把印钞机的旋钮交出去 —— 一个写错的插件
	//   传个 1000 就能印出无限兑换券，而账本上每一笔都「合法」。
	//   客户端自己算的那份只用来**对账**（对不上会 console.warn）。
	//
	// ★ 为什么默认 0（不开放）而不是 1：默认开放等于「没配置的游戏都能印券」。
	//   代价是：这一列上线后必须先跑一次 `go run ./cmd/seed games`，
	//   否则本来就开放兑换的游戏（claw3d / prizepark）会暂时换不了 ——
	//   报的是「这款游戏没有开放兑换」，不是静默少发。
	VoucherPerCoin int64 `gorm:"column:voucher_per_coin;default:0" json:"voucher_per_coin"`

	// EntryURL 游戏入口 URL（插件包地址，可为空）
	EntryURL string `gorm:"column:entry_url;size:512" json:"entry_url"`
	// CoverURL 封面图
	CoverURL string `gorm:"column:cover_url;size:512" json:"cover_url"`

	// AgeHint / SessionHint 适龄与单次时长（展示用）
	AgeHint     string `gorm:"column:age_hint;size:32" json:"age_hint"`
	SessionHint string `gorm:"column:session_hint;size:32" json:"session_hint"`

	// Learn 练什么（JSON 数组，3~4 条）
	Learn JSONStringArray `gorm:"column:learn;type:text" json:"learn"`
	// About 详细介绍（JSON 数组，2~3 段）
	About JSONStringArray `gorm:"column:about;type:text" json:"about"`
	// Shots 截图（JSON 数组，元素 {src, caption}）
	Shots JSONShotList `gorm:"column:shots;type:text" json:"shots"`

	// Sort 排序权重（越大越靠前）
	Sort int `gorm:"column:sort;default:0;index" json:"sort"`
}

func (Game) TableName() string { return "game" }

// 游戏状态
const (
	GameStatusAvailable  = "available"
	GameStatusComingSoon = "coming_soon"
	GameStatusOffline    = "offline"
)

// ============================ 游戏拥有关系 ============================

// UserGame 用户拥有的游戏。
//
// ⚠️ 与 kid-quest-farm 的差异：
// 原项目**不单独存拥有关系**，而是从账本推导（`purchasedIdsFrom`），
// 理由是"同一个事实存两份一定会走散"。
//
// 服务端这里**存一份**，因为：
//   - 免费/默认拥有的游戏没有购买记录，无法从账本推导
//   - 5w 并发下每次都扫账本推导代价太高
//   - 用 `(uid, game_id)` 唯一索引保证不重复，账本是真相、这里是索引
//
// 对账：`Source == game_purchase` 的账本记录必须能在这里找到对应行，
// 反之这里的非 default 行必须有对应账本记录（有对账脚本）。
type UserGame struct {
	BaseModel

	// UID + GameID 复合唯一索引：保证同一用户同一游戏只有一条记录，
	// 也是 ON CONFLICT (uid, game_id) 的前提（SQLite 要求冲突目标必须有唯一约束）。
	UID    uint   `gorm:"column:uid;uniqueIndex:uk_uid_game;not null" json:"uid"`
	GameID string `gorm:"column:game_id;size:32;uniqueIndex:uk_uid_game;not null" json:"game_id"`

	// Source 获得方式：default（默认拥有）/ purchase（购买）/ gift（赠送）
	Source string `gorm:"column:source;size:16;not null" json:"source"`
	// PricePaid 实付金额（默认拥有为 0）
	PricePaid int64 `gorm:"column:price_paid;default:0" json:"price_paid"`
	// Currency 支付币种（points / diamond）
	Currency Currency `gorm:"column:currency;size:16" json:"currency"`
	// OrderID 关联订单号（幂等）
	OrderID string `gorm:"column:order_id;size:64;index" json:"order_id"`
}

func (UserGame) TableName() string { return "user_game" }

// 游戏获得方式
const (
	GameOwnedByDefault  = "default"
	GameOwnedByPurchase = "purchase"
	GameOwnedByGift     = "gift"
)

// ============================ 购买订单 ============================

// GameOrder 游戏购买订单。
//
// 为什么要独立订单表（而不是只写账本）：
//   - 幂等：同一 order_id 重复提交只生效一次
//   - 审计：能看到"谁在什么时候用什么价格买了什么"
//   - 退款：有订单才有退款的对象
type GameOrder struct {
	BaseModel

	// OrderID 业务订单号（服务端生成，唯一）
	OrderID string `gorm:"column:order_id;size:64;uniqueIndex;not null" json:"order_id"`
	UID     uint   `gorm:"column:uid;index;not null" json:"uid"`
	GameID  string `gorm:"column:game_id;size:32;index;not null" json:"game_id"`

	// Currency 支付币种
	Currency Currency `gorm:"column:currency;size:16;not null" json:"currency"`
	// Amount 支付金额
	Amount int64 `gorm:"column:amount;not null" json:"amount"`

	// Status 订单状态
	Status string `gorm:"column:status;size:16;default:'paid';index" json:"status"`

	// ClientRequestID 客户端幂等键
	ClientRequestID string `gorm:"column:client_request_id;size:64;index" json:"-"`
}

func (GameOrder) TableName() string { return "game_order" }

// 订单状态
const (
	OrderStatusPending   = "pending"
	OrderStatusPaid      = "paid"
	OrderStatusRefunded  = "refunded"
	OrderStatusCancelled = "cancelled"
)
