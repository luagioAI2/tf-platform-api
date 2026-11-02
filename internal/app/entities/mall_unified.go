package entities

// ============================ 兑换列表（统一行） ============================
//
// 「兑换」那一格现在有两个来源：
//
//	Source = "redeem" —— 家长自己加的兑换品（`redeem_item`）
//	Source = "wish"   —— 心愿商品被家长**定了价**（「定价 = 上架」）
//
// ## 家长口径（2026-10-29，两轮）
//
//	「爸爸妈妈已经定好价格的，相当于添加了兑换，应该出现在兑换列表里。」
//	「兑换 里的东西都是一样，都能用兑换券或者积分，包括游戏也一样。
//	  不需要去心愿商城换。心愿商城只是标明心愿的。」
//
// 所以：
//   - **不是单独一块**。没有「心愿商城 · 爸妈开放了」这个标题 ——
//     它和家里的兑换品排在**同一个分类列表**里（分类固定 `gift`，
//     只决定分组标题、不决定付款规则），按价格混排。
//   - **不是一道门**。点「换这个」就在这一行扣币、生成订单，
//     不再跳去别的收银台。
//
// ## 付款规则由来源决定，不由 `category` 决定
//
// 两个来源走**同一个** `PlanRedeemPayment`（券优先、1 券 ≡ 1 积分、可混付）——
// 2026-10-29 v3 把心愿商城从「只收券」改成和兑换一致，就是为了这里。
type RedeemRow struct {
	// Source 来源："redeem" | "wish"
	Source string `json:"source"`
	// ID 业务 ID（兑换品是 redeem_item.item_id；心愿是 wish_item.item_id）
	ID string `json:"id"`

	Name     string `json:"name"`
	Emoji    string `json:"emoji"`
	Category string `json:"category"`
	Note     string `json:"note"`

	// Price 标价（「分」，1 券 = 1 分）
	Price int64 `json:"price"`

	// Plan 按当前余额算出的分账预览（券多少 + 积分多少）
	Plan RedeemPayment `json:"plan"`
	// CanRedeem 现在能不能换
	CanRedeem bool `json:"can_redeem"`
	// Description 「30 兑换券 + 20 积分」这类可读文案
	Description string `json:"description"`
	// BlockedByLimit 是否被每日限额挡住（仅 redeem 来源，心愿没有限额概念）
	BlockedByLimit bool `json:"blocked_by_limit"`

	// ---- 仅 redeem 来源 ----
	TodayCount     int64 `json:"today_count,omitempty"`
	RemainingToday int   `json:"remaining_today,omitempty"`
	Once           bool  `json:"once,omitempty"`
	Stock          int   `json:"stock,omitempty"`

	// ---- 仅 redeem 来源 · 客户端镜像用的原样字段 ----
	//
	// 这一组只在 `raw: true` 时有意义（见 `ListRedeemItemReq.Raw`）：
	// 客户端要把商城整张表搬到本机，就必须拿到**服务端那一行本身**，
	// 而不是「算给孩子看的视图」。少了任何一个字段的表现都是静默的：
	//   - 缺 `LimitPerDay` → 孩子看到「不限次数」，点了被服务端拒绝；
	//   - 缺 `Archived`    → 家长下架的东西在自己的管理页里「不见了」；
	//   - 缺 `Visible`     → 客户端只能自己猜「换过的一次性商品要不要显示」，
	//                        猜错就是两个真相。
	Archived        bool  `json:"archived"`
	LimitPerDay     int   `json:"limit_per_day"`
	CreatedByParent bool  `json:"created_by_parent"`
	CreatedAt       int64 `json:"created_at"`
	UpdatedAt       int64 `json:"updated_at"`
	// Visible 现在该不该出现在**孩子**的列表里（raw 模式下由服务端算好）
	Visible bool `json:"visible"`

	// ---- 仅 wish 来源 ----
	Kind        WishKind `json:"kind,omitempty"`
	PartnerName string   `json:"partner_name,omitempty"`
	Notice      string   `json:"notice,omitempty"`
	// BlindBoxRemaining 盲盒池里还剩几款（只对 blindbox 有意义）
	BlindBoxRemaining int `json:"blind_box_remaining,omitempty"`
}

// ============================ 兑换记录（合并时间线） ============================
//
// ## 为什么必须合并两张表
//
// 家长原话（2026-10-29，报的 bug）：
//
//	「孩子刚兑换了一个游戏，为啥我兑换记录里看不到。」
//
// 问题出在**两张表**：家里那几件写 `redeem_record`，心愿/游戏写 `wish_order`。
// 兑换记录页只读前者 ⇒ 孩子在兑换页换了一款游戏，扣了钱、游戏也装上了，
// 记录里却一个字都没有。界面上看不出任何异常，只有家长去对账时才会发现
// 「券少了 30，找不到花在哪」。
//
// 对应前端 `domain/redeemHistory.ts` 的 `redeemHistory()`。
type HistoryRow struct {
	// ID 主键。两张表各自的 id，不会撞（`RR…` / `WO…`）
	ID string `json:"id"`
	// Source 来源："redeem" | "wish"
	Source string `json:"source"`

	Name  string `json:"name"`
	Emoji string `json:"emoji"`

	// CreatedAt 时间戳（并集的排序键）
	CreatedAt int64 `json:"created_at"`

	// Pay 「30 兑换券 + 20 积分」这种一句话，和商品行上的付款行同一套写法
	Pay string `json:"pay"`
	// CouponPaid / PointsPaid 分账（便于客户端自己算，不用解析 Pay）
	CouponPaid int64 `json:"coupon_paid"`
	PointsPaid int64 `json:"points_paid"`
	// Cost 总价
	Cost int64 `json:"cost"`

	// Tag 状态文字，例如「待兑现」/「已放进游戏柜」
	Tag string `json:"tag"`
	// Pending 还算「待兑现」（tab 上那个数字用这个算）
	Pending bool `json:"pending"`
	// Delivered 游戏单：交付凭证（`opened_game_id` 非空）
	Delivered bool `json:"delivered"`

	// ---- 仅 wish 来源（家长端要用来决定做什么） ----
	Status WishStatus `json:"status,omitempty"`
	// BuyURL 外链（**仅家长端返回**）
	BuyURL string `json:"buy_url,omitempty"`
	// BuyLabel 外链按钮文字
	BuyLabel string `json:"buy_label,omitempty"`
}

// ============================ 心愿上架条目 ============================

// WishShelfEntry 已上架的心愿商品（家长定了价 = 上架）。
//
// 排除条件（对齐前端 `wishShelf`，**一个都不能少**）：
//
//  1. 没定价 / 已关闭 —— 那是「等爸妈回应」，不是能换的东西
//  2. 已下架（`archived`）—— 目录里撤掉的商品不该还挂在兑换列表上
//  3. **已经拥有**（gamebox）—— 孩子会看到自己早就换到的游戏
//  4. **盲盒池空** —— 池子空了还摆在「能换什么」里，点进去只会被拒
//
// 另外排除**已在走流程**的单子（`committed` / `confirmed`）：
// 那件东西已经付过钱了，再列一次等于鼓励孩子换第二遍。
type WishShelfEntry struct {
	Item  *WishItem `json:"item"`
	Price int64     `json:"price"`
}
