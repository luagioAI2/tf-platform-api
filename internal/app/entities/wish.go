package entities

// ============================ 心愿商城 ============================
//
// 「心愿商城」（Wish Mall）—— 孩子许愿、家长买的商城。
//
// ## 三条最要紧的口径（见 kid-quest-farm `docs/wish-mall-spec.md`）
//
//  1. **只收兑换券**，不混付积分（与「兑换商城」不同）。
//  2. **券必须真扣** —— 因为成交发生在 App 外面，不扣的话孩子能同时挂
//     10 个「已兑换」，家长每个都得去淘宝买一遍。
//  3. **盲盒先算池子再扣券** —— 池子空了要直接拒绝，不能先扣再发现没得开。

// WishKind 分类
//
// ⚠️ 这几类共用一个 `Kind` 字段而不是拆成两张表，是因为它们在列表里
// 要**混排**（用户要求：游戏盲盒优先显示）。拆表会让列表排序变成归并逻辑。
//
// 2026-10-29 用户要求：「收藏换成其他，前面再加美食，出行。」
// ⇒ 新增 `food` / `outing` 两档；`collectible` 的**显示名**从「收藏」改成
// 「其他」，但**内部名不动** —— 改名会让 `wish_item.kind` 的存量数据
// 和测试全部对不上，而界面上根本没机会看到英文名。
//
// ⚠️ 显示名（「美食」「出行」「其他」）在**客户端**的 `WISH_KIND_META` 里，
// 服务端只认内部名。别在这里造一份 label 表 —— 那就是「同一个事实存两份」。
type WishKind string

const (
	WishKindBlindbox WishKind = "blindbox" // 盲盒（数字，开游戏）
	WishKindGamebox  WishKind = "gamebox"  // 游戏盒子（数字，指定游戏）
	WishKindToy      WishKind = "toy"      // 玩具（实物）
	WishKindStudy    WishKind = "study"    // 学习（实物）
	// WishKindFood 美食（实物）。2026-10-29 新增
	WishKindFood WishKind = "food"
	// WishKindOuting 出行（实物）。2026-10-29 新增
	WishKindOuting WishKind = "outing"
	// WishKindCollectible 其他（实物）。
	//
	// 内部名保留 `collectible`（历史数据用这个），**显示名是「其他」**。
	WishKindCollectible WishKind = "collectible"
)

// 全部合法分类 —— 新增一档时**只改这一处**，
// `IsValid` / 校验 / 白名单都从这里取。
var allWishKinds = []WishKind{
	WishKindBlindbox,
	WishKindGamebox,
	WishKindToy,
	WishKindStudy,
	WishKindFood,
	WishKindOuting,
	WishKindCollectible,
}

// AllWishKinds 返回全部合法分类（副本，调用方改了不影响内部）
func AllWishKinds() []WishKind {
	out := make([]WishKind, len(allWishKinds))
	copy(out, allWishKinds)
	return out
}

// IsValid 分类是否合法
func (k WishKind) IsValid() bool {
	for _, v := range allWishKinds {
		if k == v {
			return true
		}
	}
	return false
}

// IsDigital 是否数字商品（兑换当场交付）
//
// ⚠️ 判据**不是** kind：gamebox 装包失败会走取消退回，
// 那种单子本来就不该显示成「已交付」。真正判据是
// `WishOrder.OpenedGameID != ""`（装包成功才写）。
//
// 这个方法是「商品**类别**上是不是数字」，用于**兑换前**的校验收货信息；
// 交付与否看 `OpenedGameID`。
func (k WishKind) IsDigital() bool {
	return k == WishKindBlindbox || k == WishKindGamebox
}

// WishStatus 订单状态
type WishStatus string

const (
	WishStatusWished    WishStatus = "wished"    // 孩子表明意向（券不动）
	WishStatusCommitted WishStatus = "committed" // 孩子确定兑换（**扣券**）
	WishStatusConfirmed WishStatus = "confirmed" // 家长已去外链购买
	WishStatusDone      WishStatus = "done"      // 买好了 / 收到了
	WishStatusCancelled WishStatus = "cancelled" // 取消（**退券**）
)

// IsValid 状态是否合法
func (s WishStatus) IsValid() bool {
	switch s {
	case WishStatusWished, WishStatusCommitted, WishStatusConfirmed,
		WishStatusDone, WishStatusCancelled:
		return true
	}
	return false
}

// IsTerminal 是否终态
func (s WishStatus) IsTerminal() bool {
	return s == WishStatusDone || s == WishStatusCancelled
}

// HasHeldVouchers 该状态是否「券还押在平台手里」
//
// 只有这两种状态可以取消退券。`wished` 还没扣、`done`/`cancelled` 已终结。
func (s WishStatus) HasHeldVouchers() bool {
	return s == WishStatusCommitted || s == WishStatusConfirmed
}

// CanTransitionTo 状态机
//
//	wished    → committed | cancelled
//	committed → confirmed | done | cancelled
//	confirmed → done | cancelled
//	done / cancelled → 终态
//
// 注意 `committed → done` 是允许的：数字商品（盲盒/盒子）
// 兑换当场就交付了，家长只需要「收尾」，不必经过 confirmed。
func (s WishStatus) CanTransitionTo(target WishStatus) bool {
	switch s {
	case WishStatusWished:
		return target == WishStatusCommitted || target == WishStatusCancelled
	case WishStatusCommitted:
		return target == WishStatusConfirmed || target == WishStatusDone || target == WishStatusCancelled
	case WishStatusConfirmed:
		return target == WishStatusDone || target == WishStatusCancelled
	default:
		return false
	}
}

// CanRedeem 是否可以从该状态推进到「已兑换」
func (s WishStatus) CanRedeem() bool {
	return s == WishStatusWished
}

// ============================ 商品目录 ============================

// WishItem 心愿商品目录。
//
// ## 为什么入库（而前端是常量）
//
// 前端把目录做成 TS 常量（`games/wishCatalog.ts`），理由是「改文案不该触发
// Dexie 迁移」。服务端没有这个约束，而且运营需要能上架/改价/下架 ——
// 所以入库，但**种子数据**从前端常量同步过来（见 `seed/games.go`）。
//
// 可变的那部分（本家庭开不开放、卖多少券）在 `WishOffer` 里，单独一张表。
type WishItem struct {
	BaseModel

	// ItemID 业务 ID（前端 `WishItem.id` 兼容）
	ItemID string   `gorm:"column:item_id;size:64;uniqueIndex;not null" json:"id"`
	Kind   WishKind `gorm:"column:kind;size:16;index;not null" json:"kind"`

	Name  string `gorm:"column:name;size:64;not null" json:"name"`
	Emoji string `gorm:"column:emoji;size:16" json:"emoji"`
	// Tint 图标底色档位：sun / grass / sky / grape / tan / berry
	Tint string `gorm:"column:tint;size:16" json:"tint"`

	// PartnerName 商家名（如「泡泡玛特」）。实物才有
	PartnerName string `gorm:"column:partner_name;size:64" json:"partner_name"`

	// MerchantUID 这件东西归哪个商家（`user.id`，0 = 平台自营 / 没有商家）。
	//
	// ★ 它只解决**「谁能改这一件」**：商家后台按这一列过滤，
	//   一个商家改不到别人的商品。`PartnerName` 是**展示用**的名字，
	//   两者故意分开 —— 同名商家、改名、代运营都不该动到归属。
	//
	// ⚠️ 默认 0 不是「未填」而是「平台自营」：历史数据全是没有商家的，
	//   如果拿它当「空值」去 join，后台的「我的商品」会突然多出几百件。
	MerchantUID uint `gorm:"column:merchant_uid;index;default:0" json:"merchant_uid"`

	// Pool 盲盒池：候选游戏 id（JSON 数组）
	//
	// ⚠️ 真正开奖时还要**减去孩子已拥有的**（见 PickBlindBoxMany）。
	// 池子里写死「全部游戏」是对的 —— 拥有关系是每个家庭不同的，不能写进常量。
	Pool JSONStringArray `gorm:"column:pool;type:text" json:"pool"`

	// GameID 游戏盒子指定的那一款（`gamebox` 必填）
	GameID string `gorm:"column:game_id;size:32;index" json:"game_id"`

	// PriceCents 参考零售价（**分**）。只做展示，**不参与任何计算**
	PriceCents int64 `gorm:"column:price_cents;default:0" json:"price_cents"`

	// AgeRange 适龄范围 [min, max]
	AgeRange JSONIntArray `gorm:"column:age_range;type:text" json:"age_range"`

	// Note 一句话说明
	Note string `gorm:"column:note;size:255" json:"note"`
	// Notice 兑换须知（「不指定款式」这类必须写清楚）
	Notice string `gorm:"column:notice;size:255" json:"notice"`

	// BuyURL 外链（运营在 tf-admin「商城」列表里配的那条）。
	//
	// ⚠️ 这里标 `json:"-"` 是因为**这个结构体不直接序列化** ——
	// 对外的字段在 `WishItemView.BuyURL`（2026-11-06 起两端都下发，
	// 空串 = 没配 = 客户端不渲染「去购买」）。两处别各写一套口径。
	BuyURL string `gorm:"column:buy_url;size:512" json:"-"`
	// BuyLabel 外链按钮文字，如「去淘宝购买」
	BuyLabel string `gorm:"column:buy_label;size:32" json:"-"`

	// BuyOpen 是否开放购买（默认开）。
	//
	// ★ 给「淘宝 / 京东那条链接突然买不了了」用的**临时开关**：
	//   关掉之后 `BuyLink()` 返回空 ⇒ 孩子卡片弹框和家长确认页都不摆
	//   「去购买」按钮，但商品照旧在列表里（孩子能许愿、家长能自己买完
	//   再来点「已经买好了」收尾）。
	//   ⚠️ 和 `archived`（下架）不是一回事：下架是**把商品藏起来**。
	//
	// ⚠️ `default:true` 不能省：AutoMigrate 靠它给存量行补 1。
	//    没有它，加列之后老商品全读成 false —— 全站「去购买」一起消失，
	//    而且**不报错**（2026-11-06 晚加列时特意验过）。
	BuyOpen bool `gorm:"column:buy_open;default:true" json:"-"`

	Archived bool `gorm:"column:archived;default:false;index" json:"archived"`
	// SortOrder 排序权重（小的在前；盲盒永远最前，见 CompareWishItem）
	//
	// ⚠️ **不要写 `default:100`**：GORM 在字段是零值时会**省略这一列**，
	//    让数据库默认值生效 —— 于是种子里的 `SortOrder: 0` 落库变成 100，
	//    单开盲盒反而排到「5 连开」后面。后台列表和 C 端顺序就这样对不上了，
	//    而两边都不报错。
	SortOrder int `gorm:"column:sort_order" json:"sort_order"`

	// UnlockCount 一次开出几款游戏（只对 blindbox 有意义）。
	//
	// 1 = 老的单开盲盒；5 = 「5 连开」。
	//
	// ★ 「已经解锁的不计算」这条要求落在这里的语义上：开奖时先把孩子
	//   已拥有的从池子里减掉，再从剩下的里面抽 UnlockCount 款
	//   （见 PickBlindBoxMany）。所以已拥有的**不占名额**。
	//
	// ⚠️ 剩余不足 UnlockCount 时整单拒绝，不是「有几款给几款」——
	//   卖的是 5 款的价，给 3 款就是缺斤少两，而且孩子没得选。
	//   判据在 `Redeem` 里。
	UnlockCount int `gorm:"column:unlock_count;default:1" json:"unlock_count"`
}

func (WishItem) TableName() string { return "wish_item" }

// BuyLink 「去购买」按钮要用的外链和文字。
//
// ★ **两个条件同时成立才有链接**：运营配了 `buy_url`，而且 `buy_open` 开着。
//
//	四个下发点（商品列表 / 详情 / 家长订单 / 兑换记录）都调这一个方法 ——
//	各写一遍的话，总有一处忘记判 `buy_open`，表现是「后台关了，
//	某个页面还挂着一颗点进去 404 的按钮」。
func (it *WishItem) BuyLink() (url, label string) {
	if it == nil || !it.BuyOpen {
		return "", ""
	}
	return it.BuyURL, it.BuyLabel
}

// ============================ 家长定价 ============================

// WishOffer 家长对本商品的开放设置（**本家庭的**可变状态）。
//
// ⚠️ 商品的 `open` 是「本家庭的设置」，不是商品的全局属性。
// 所以它必须按 (uid, item_id) 存，不能写进 WishItem。
type WishOffer struct {
	BaseModel

	// ⚠️ 索引名全局唯一（SQLite 与 MySQL 的差异见 RedeemItem 的注释）
	UID    uint   `gorm:"column:uid;uniqueIndex:uk_wish_offer_uid_item;not null" json:"uid"`
	ItemID string `gorm:"column:item_id;size:64;uniqueIndex:uk_wish_offer_uid_item;not null" json:"item_id"`

	// VoucherPrice 券价
	VoucherPrice int64 `gorm:"column:voucher_price;default:0" json:"voucher_price"`
	// DiamondPrice 钻石价，测试用；0 = 不卖
	DiamondPrice int64 `gorm:"column:diamond_price;default:0" json:"diamond_price"`

	// OpenedAt 开放时间（0 = 未开放）
	OpenedAt int64 `gorm:"column:opened_at;default:0" json:"opened_at"`
	// ClosedAt 关闭时间（0 = 仍开放）
	ClosedAt int64 `gorm:"column:closed_at;default:0" json:"closed_at"`

	// OneTime 是否「一次性兑换」（2026-11-06）。
	//
	// true  = 兑换成功那一刻自动关闭这一行（`closed_at` 置为当前时间）：
	//         商品从「兑换」列表里消失，心愿商城里回到「我想要」，
	//         孩子可以重新许愿、家长重新定价。
	// false = 可以反复兑换，「兑换」列表里一直留着，并显示已兑换次数。
	//
	// ⚠️ 关闭动作必须在**兑换的那个事务里**做（`WishService.Redeem`），
	//    不能交给客户端再发一次 set-offer —— 那一次失败时券已经扣了、
	//    商品却还挂在兑换列表上，而且不报错。
	OneTime bool `gorm:"column:one_time;default:false" json:"one_time"`
}

func (WishOffer) TableName() string { return "wish_offer" }

// IsOpen 本家庭是否已开放该商品
func (o *WishOffer) IsOpen() bool {
	return o != nil && o.OpenedAt > 0 && o.ClosedAt == 0
}

// ============================ 心愿订单 ============================

// WishOrder 心愿订单。
type WishOrder struct {
	BaseModel

	// ⚠️ 索引名全局唯一（与 TaskInstance 的 idx_uid_status 冲突过）
	UID     uint   `gorm:"column:uid;index:idx_wish_uid_status;not null" json:"uid"`
	OrderID string `gorm:"column:order_id;size:64;uniqueIndex;not null" json:"id"`

	ItemID string   `gorm:"column:item_id;size:64;index;not null" json:"item_id"`
	Kind   WishKind `gorm:"column:kind;size:16;index" json:"kind"`

	// 以下是**快照**：商品改名/下架/改价后历史仍可读
	ItemName    string `gorm:"column:item_name;size:64;not null" json:"item_name"`
	ItemEmoji   string `gorm:"column:item_emoji;size:16" json:"item_emoji"`
	PartnerName string `gorm:"column:partner_name;size:64" json:"partner_name"`

	// VoucherPrice 下单时的券价（家长之后改价不影响这一单）
	VoucherPrice int64 `gorm:"column:voucher_price;default:0" json:"voucher_price"`

	Status WishStatus `gorm:"column:status;size:16;default:'wished';index:idx_wish_uid_status" json:"status"`

	// VouchersHeld 这一单实际扣掉的券。
	//
	// `wished` 阶段是 0；`committed` 起等于分账算出来的券部分。
	// **退款按这个数退，不重新读商品** —— 商品改价了也不能退错。
	VouchersHeld int64 `gorm:"column:vouchers_held;default:0" json:"vouchers_held"`

	// PointsHeld 这一单实际扣掉的**积分**（2026-10-29 起可券 + 积分混付）。
	//
	// 缺省按 0 读（老订单全是纯券单）。
	//
	// ⚠️ **退款时必须和 VouchersHeld 一起退** —— 一笔成交扣两种币，
	// 退款只退一种的话，孩子的另一半就凭空蒸发了，而且**不报错**。
	// 前端 `WishOrder.pointsHeld` 与这里语义一致。
	PointsHeld int64 `gorm:"column:points_held;default:0" json:"points_held"`

	// OpenedGameID 盲盒开出的游戏 id（只有 blindbox 会有）
	//
	// ⚠️ 「一次开 N 款」时这里存**第一个**，完整清单在 OpenedGameIDs。
	//   两个都写是故意的：老客户端 / 老逻辑只看这一个字段，
	//   只写清单会让它们在界面上显示成「一款都没开出」。
	OpenedGameID string `gorm:"column:opened_game_id;size:32;index" json:"opened_game_id"`

	// OpenedGameIDs 一次开出多款时的**完整清单**（`unlock_count > 1`）。
	//
	// 空数组 = 老的单开单子（或者不是数字商品）。读的时候必须兼容：
	// 清单为空就退回看 OpenedGameID。
	OpenedGameIDs JSONStringArray `gorm:"column:opened_game_ids;type:text" json:"opened_game_ids"`

	// CodeTail 兑换码尾号（只存尾号，不存明文）
	CodeTail string `gorm:"column:code_tail;size:8" json:"code_tail"`
	// CodeHash 兑换码 sha256（**不是明文**）
	CodeHash string `gorm:"column:code_hash;size:64;index" json:"-"`

	// Ship 收货信息（敏感，见「隐私」一节）
	Ship *WishShipInfo `gorm:"column:ship;type:text;serializer:json" json:"ship"`
	// TrackingNo 快递单号
	TrackingNo string `gorm:"column:tracking_no;size:64" json:"tracking_no"`

	CommittedAt *int64 `gorm:"column:committed_at" json:"committed_at"`
	ConfirmedAt *int64 `gorm:"column:confirmed_at" json:"confirmed_at"`
	DoneAt      *int64 `gorm:"column:done_at" json:"done_at"`
	CancelledAt *int64 `gorm:"column:cancelled_at" json:"cancelled_at"`
	// CancelReason 取消原因（对孩子可见）
	CancelReason string `gorm:"column:cancel_reason;size:255" json:"cancel_reason"`
}

func (WishOrder) TableName() string { return "wish_order" }

// WishShipInfo 收货信息（敏感）。
//
// ## 隐私要求（见 spec §9）
//
//   - 只在家长端出现，孩子端接口**不返回**
//   - `PurgeAfter` 到期自动抹除（默认下单后 90 天）
type WishShipInfo struct {
	Receiver string `json:"receiver"`
	Phone    string `json:"phone"`
	Region   string `json:"region"`
	Address  string `json:"address"`
	Note     string `json:"note,omitempty"`
	// PurgeAfter 到期自动抹除的时间戳
	PurgeAfter int64 `json:"purge_after"`
}

// Masked 脱敏后的收货信息（给孩子端/列表用）
func (s *WishShipInfo) Masked() *WishShipInfo {
	if s == nil {
		return nil
	}
	phone := s.Phone
	if len(phone) > 4 {
		phone = "****" + phone[len(phone)-4:]
	}
	return &WishShipInfo{
		Receiver: s.Receiver,
		Phone:    phone,
		Region:   s.Region,
		Address:  "", // 详细地址不回
	}
}

// ============================ 兑换码（P2） ============================

// WishCode 兑换码（**存 sha256，不存明文**）。
//
// 用途：家长在线下买了东西，拿到一个码，孩子在 App 里输码解锁。
type WishCode struct {
	BaseModel

	// CodeHash 明文码的 sha256（唯一）
	CodeHash string `gorm:"column:code_hash;size:64;uniqueIndex;not null" json:"-"`
	// CodeTail 后 4 位（便于家长核对是哪张）
	CodeTail string `gorm:"column:code_tail;size:8" json:"code_tail"`

	// ItemID 对应的商品（空 = 通用码）
	ItemID string `gorm:"column:item_id;size:64;index" json:"item_id"`
	// Batch 批次号（运营用）
	Batch string `gorm:"column:batch;size:64;index" json:"batch"`

	// Used 是否已使用
	Used bool `gorm:"column:used;default:false;index" json:"used"`
	// UsedByUID 使用者
	UsedByUID uint `gorm:"column:used_by_uid;index" json:"used_by_uid"`
	// UsedAt 使用时间
	UsedAt *int64 `gorm:"column:used_at" json:"used_at"`

	// ExpireAt 过期时间（0 = 永不过期）
	ExpireAt int64 `gorm:"column:expire_at;default:0" json:"expire_at"`
}

func (WishCode) TableName() string { return "wish_code" }

// ============================ 盲盒（纯函数） ============================

// BlindBoxRemaining 盲盒还能开出哪些。
//
// `pool` 是目录里写死的候选，`owned` 是这个家庭**当前已拥有**的游戏 id。
// 交集之外的才是能开出来的。
//
// 去重是必须的：池子里同一个 id 写两次会让那一款的概率翻倍，
// 而这种错误在代码里看不出来，只有概率分布变了。
func BlindBoxRemaining(pool, owned []string) []string {
	have := make(map[string]struct{}, len(owned))
	for _, id := range owned {
		have[id] = struct{}{}
	}
	seen := make(map[string]struct{}, len(pool))
	out := make([]string, 0, len(pool))
	for _, id := range pool {
		if _, ok := have[id]; ok {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// PickBlindBoxMany 一次开 n 款盲盒（「5 连开」）。
//
// 返回的每一款都**不在 `owned` 里**，而且互不重复 —— 这正是
// 「已经解锁的不计算」那条要求：孩子已有的那几款先减掉，不占名额。
//
// ★ 剩余不足 n 时返回**短的列表**（有几款给几款），由调用方决定是
//
//	「整单拒绝」还是「按剩余给」。现在的口径是整单拒绝（见 `Redeem`）。
//
// `randVal` 是 [0,1) 的随机数，可注入是为了让测试钉死结果；
// 内部由它派生一个确定性的伪随机序列来做洗牌。
func PickBlindBoxMany(pool, owned []string, n int, randVal float64) []string {
	if n <= 0 {
		return nil
	}
	remaining := BlindBoxRemaining(pool, owned)
	if len(remaining) == 0 {
		return nil
	}
	if n > len(remaining) {
		n = len(remaining)
	}
	if randVal < 0 {
		randVal = 0
	}
	if randVal >= 1 {
		randVal = 0.999999
	}
	// 用 randVal 派生一个确定性的 LCG 序列，做 Fisher-Yates 洗牌。
	// 不在这里调 rand.Float64()：那样测试断言不了「抽的是哪几款」。
	seed := uint64(randVal*1e9) + 1
	next := func(m int) int {
		seed = seed*6364136223846793005 + 1442695040888963407
		return int((seed >> 33) % uint64(m))
	}
	deck := append([]string(nil), remaining...)
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		j := i + next(len(deck)-i)
		deck[i], deck[j] = deck[j], deck[i]
		out = append(out, deck[i])
	}
	return out
}

// DaysToSave 「大约要攒几天」。
//
// 这是详情页上唯一一个**比价格更像财商教育**的数字：教的是「想要的东西要等」。
// 日均是 0 或负数时返回 -1 —— 显示「要攒 ∞ 天」比不显示更糟。
func DaysToSave(price, perDay int64) int {
	if price <= 0 {
		return 0
	}
	if perDay <= 0 {
		return -1
	}
	d := (price + perDay - 1) / perDay // 向上取整
	if d < 1 {
		d = 1
	}
	return int(d)
}

// WishStatusText 状态的中文说法。列表、详情、家长端共用，别在组件里各写一份。
var WishStatusText = map[WishStatus]string{
	WishStatusWished:    "等爸妈定价",
	WishStatusCommitted: "等爸妈确认",
	WishStatusConfirmed: "爸妈去买啦",
	WishStatusDone:      "已经拿到",
	WishStatusCancelled: "已取消",
}
