package entities

// ============================ 兑换商城 ============================
//
// 「兑换商城」（RedeemPage）—— 家庭内的兑换：零食、出去玩、特权。
//
// ## 与「心愿商城」的区别（这是设计的地基）
//
//	|          | 兑换商城         | 心愿商城           |
//	| 收什么币  | 兑换券优先 + 积分 | **只收兑换券**     |
//	| 混付      | 允许（1 券 = 1 分）| 不允许            |
//	| 谁定价    | 家长              | 家长（逐商品开放）|
//	| 成交在哪  | App 内标记        | App 外（淘宝）    |
//
// 为什么心愿商城不许混付：混付会让「积分能不能换实物」变成一个
// 是非题，而它应该是**不能**（积分是家庭内循环，券是出口）。

// RedeemCategory 兑换商品分类
type RedeemCategory string

const (
	RedeemCategorySnack     RedeemCategory = "snack"     // 零食
	RedeemCategoryGift      RedeemCategory = "gift"      // 礼物
	RedeemCategoryScreen    RedeemCategory = "screen"    // 屏幕时间
	RedeemCategoryOuting    RedeemCategory = "outing"    // 出去玩
	RedeemCategoryPrivilege RedeemCategory = "privilege" // 特权
	RedeemCategoryCustom    RedeemCategory = "custom"    // 自定义
)

// IsValid 分类是否合法
func (c RedeemCategory) IsValid() bool {
	switch c {
	case RedeemCategorySnack, RedeemCategoryGift, RedeemCategoryScreen,
		RedeemCategoryOuting, RedeemCategoryPrivilege, RedeemCategoryCustom:
		return true
	}
	return false
}

// RedeemItem 兑换商品（家长创建）。
//
// ## 价格口径
//
// `Price` 是**「分」**，不是「积分」也不是「券」。
// 因为 1 兑换券 ≡ 1 分，所以一个数字就够：
//
//	Price = 50 → 可以「50 券」也可以是「30 券 + 20 积分」
//
// 具体怎么付由 `entities.PlanRedeemPayment` 决定（券优先）。
type RedeemItem struct {
	BaseModel

	// UID 所属用户（家长）
	//
	// ⚠️ 索引名必须**全局唯一**（用 `uk_redeem_` 前缀）。
	//
	// SQLite 的索引名是**整个数据库共享**的，两张表用同一个名字会直接报
	// `index xxx already exists`，导致 auto-migrate 失败、服务起不来；
	// 而 MySQL 是每表独立的命名空间，同样的代码在 MySQL 下**不会报错**。
	//
	// 也就是说：这个错**只在开发环境（SQLite）暴露**，
	// 上生产（MySQL）反而看不出来。实测踩过 ——
	// RedeemItem 和 WishOffer 都叫 `uk_uid_item`，本地起不来。
	UID uint `gorm:"column:uid;uniqueIndex:uk_redeem_uid_item;index;not null" json:"uid"`
	// ItemID 业务 ID（前端 `RedeemItem.id` 兼容）
	ItemID string `gorm:"column:item_id;size:64;uniqueIndex:uk_redeem_uid_item;not null" json:"id"`

	Name     string         `gorm:"column:name;size:64;not null" json:"name"`
	Emoji    string         `gorm:"column:emoji;size:16" json:"emoji"`
	Category RedeemCategory `gorm:"column:category;size:16;index;not null" json:"category"`

	// Price 所需「分」（1 券 = 1 分）
	Price int64 `gorm:"column:price;not null" json:"cost"`

	// Note 说明，如「周末可以玩 30 分钟平板」
	Note string `gorm:"column:note;size:255" json:"note"`

	// LimitPerDay 每天最多换几个（0 = 不限）。**每天重置**
	LimitPerDay int `gorm:"column:limit_per_day;default:0" json:"limit_per_day"`

	// Once 一次性：换过就不再出现。**永不重置**
	//
	// ⚠️ 和 LimitPerDay 不是一回事：那个每天重置，这个永不重置。
	// 两个都填时以 Once 为准。
	// ⚠️ 判据是**兑换记录**，不是库存 —— 库存是家长手填的，记录才是事实。
	Once bool `gorm:"column:once;default:false" json:"once"`

	// Stock 库存（-1 = 无限）
	Stock int `gorm:"column:stock;default:-1" json:"stock"`

	// Archived 是否下架
	Archived bool `gorm:"column:archived;default:false;index" json:"archived"`

	// CreatedByParent 是否由家长创建（孩子不能改）
	CreatedByParent bool `gorm:"column:created_by_parent;default:true" json:"created_by_parent"`

	Sort int `gorm:"column:sort;default:0" json:"sort"`
}

func (RedeemItem) TableName() string { return "redeem_item" }

// RedeemRecord 兑换记录。
//
// ## 为什么分账要单独存两个字段
//
// 用户 2026-09-30：「兑换页面，优先用商城币兑换。价值和积分等价的。
// 用完兑换券再用积分，可混合。」
//
// 所以一次兑换可能花掉两种币，必须分别记：
//   - `CouponPaid`（🎫 兑换券付掉多少）
//   - `PointsPaid`（🪙 积分付掉多少）
//
// **不要用 `Cost - CouponPaid` 反推 `PointsPaid` 存库** ——
// 家长改了商品价格后，历史记录会算错。
type RedeemRecord struct {
	BaseModel

	UID      uint   `gorm:"column:uid;index:idx_uid_created;not null" json:"uid"`
	RecordID string `gorm:"column:record_id;size:64;uniqueIndex" json:"id"`

	// ItemID 商品 ID
	ItemID string `gorm:"column:item_id;size:64;index;not null" json:"item_id"`
	// 以下是**快照**：商品改名/下架后历史仍可读
	Name  string `gorm:"column:name;size:64;not null" json:"name"`
	Emoji string `gorm:"column:emoji;size:16" json:"emoji"`

	// Quantity 兑换数量
	Quantity int `gorm:"column:quantity;default:1" json:"quantity"`

	// Cost 总价 = CouponPaid + PointsPaid
	Cost int64 `gorm:"column:cost;not null" json:"cost"`
	// CouponPaid 用兑换券付掉的部分
	CouponPaid int64 `gorm:"column:coupon_paid;default:0" json:"harvest_paid"`
	// PointsPaid 用积分付掉的部分
	PointsPaid int64 `gorm:"column:points_paid;default:0" json:"points_paid"`

	// PointsBalanceAfter 兑换后的积分余额（快照，便于回溯）
	PointsBalanceAfter int64 `gorm:"column:points_balance_after;default:0" json:"balance_after"`
	// CouponBalanceAfter 兑换后的兑换券余额（快照）
	CouponBalanceAfter int64 `gorm:"column:coupon_balance_after;default:0" json:"harvest_balance_after"`

	// Fulfilled 家长是否已兑现（「已经给过了」）
	Fulfilled   bool   `gorm:"column:fulfilled;default:false;index" json:"fulfilled"`
	FulfilledAt *int64 `gorm:"column:fulfilled_at" json:"fulfilled_at"`
	// FulfilledNote 兑现备注
	FulfilledNote string `gorm:"column:fulfilled_note;size:255" json:"fulfilled_note"`

	// Cancelled 家长是否取消（取消会退券退分）
	Cancelled    bool   `gorm:"column:cancelled;default:false;index" json:"cancelled"`
	CancelledAt  *int64 `gorm:"column:cancelled_at" json:"cancelled_at"`
	CancelReason string `gorm:"column:cancel_reason;size:255" json:"cancel_reason"`
}

func (RedeemRecord) TableName() string { return "redeem_record" }

// ============================ 兑换分账（纯函数） ============================

// RedeemPayment 一次兑换的分账结果
type RedeemPayment struct {
	// CouponPaid 用兑换券付掉多少（🎫）
	CouponPaid int64 `json:"harvest_paid"`
	// PointsPaid 用积分付掉多少（🪙）
	PointsPaid int64 `json:"points_paid"`
	// Total 总价 = CouponPaid + PointsPaid
	Total int64 `json:"total"`
	// Affordable 两种币加起来够不够
	Affordable bool `json:"affordable"`
	// Shortfall 不够时还差多少（够的时候是 0）
	Shortfall int64 `json:"shortfall"`
}

// PlanRedeemPayment 算出一次兑换怎么付。
//
// 规则（对齐 kid-quest-farm `domain/redeem.ts`）：
//  1. 兑换券和积分 **1:1 等价**
//  2. **先花兑换券**，花光了再用积分
//  3. 一件东西可以**两种币混着付**
//
// 纯函数：不读库、不碰状态，所以 UI 预览和真正扣款用的是**同一份规则** ——
// 预览说「30 券 + 20 分」，扣款就必须是同一个数。
//
// 为什么这条不违反「兑换券 ⇸ 积分」：那条约束禁的是
// 「兑换券变回积分」这个**内部换算**（一旦能变，农场产出 → 积分 → 买种子
// 就成了闭环印钞机）。而兑换是**出口**（换来的是零食/出去玩，退出系统），
// 券花在出口上不会回流成积分。
func PlanRedeemPayment(price, couponBalance, pointsBalance int64) RedeemPayment {
	// 取整兜底：账本里存的本来就是整数，传进负数只可能是调用方算错了
	p := price
	if p < 0 {
		p = 0
	}
	coupon := couponBalance
	if coupon < 0 {
		coupon = 0
	}
	points := pointsBalance
	if points < 0 {
		points = 0
	}

	// 先花兑换券。用 min 而不是「够就全用券」——
	// 券比价格多的时候只能花掉价格那么多，剩下的留着下次用。
	couponPaid := coupon
	if couponPaid > p {
		couponPaid = p
	}
	pointsPaid := p - couponPaid

	available := coupon + points
	shortfall := int64(0)
	if available < p {
		shortfall = p - available
	}

	return RedeemPayment{
		CouponPaid: couponPaid,
		PointsPaid: pointsPaid,
		Total:      p,
		Affordable: available >= p,
		Shortfall:  shortfall,
	}
}

// DescribeRedeemPayment 一句话描述这次是怎么付的，给提示文案用。
//
// 只出一种币时不写「+0」—— 那看着像出错。
func DescribeRedeemPayment(couponPaid, pointsPaid int64) string {
	switch {
	case couponPaid <= 0:
		return itoa64(pointsPaid) + " 积分"
	case pointsPaid <= 0:
		return itoa64(couponPaid) + " 兑换券"
	default:
		return itoa64(couponPaid) + " 兑换券 + " + itoa64(pointsPaid) + " 积分"
	}
}

// itoa64 int64 转字符串（避免引 strconv 到实体层）
func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
