package seed

import (
	"context"
	"fmt"
	"net/url"

	"tf-api/internal/app/entities"
	"tf-api/internal/app/service/repository"
	"tf-api/pkg/logger"

	"go.uber.org/zap"
)

// WishBlindBoxID 盲盒的商品 id（列表要把它钉在最前面，所以需要常量）
//
// 与前端 `games/wishCatalog.ts` 的 `WISH_BLINDBOX_ID` 保持一致。
const WishBlindBoxID = "wish-blindbox"

// WishBlindBox5ID 「一次开 5 款」的盲盒。
//
// 与前端 `games/wishCatalog.ts` 的 `WISH_BLINDBOX5_ID` 保持一致。
// 池子和单开盲盒**完全一样**（全部随包游戏）—— 差别只在 `UnlockCount`：
// 开奖时先把已拥有的减掉，再从剩下的里抽 5 款，所以「已解锁的不计算」。
const WishBlindBox5ID = "wish-blindbox-5"

// taobaoSearch 淘宝搜索链接。
//
// 前端刻意**不**用具体商品链接：具体链接会失效、会换 SKU、会被下架，
// 而目录是跟着版本发布的 —— 一个失效的链接比没有链接更糟。
func taobaoSearch(keyword string) string {
	return "https://s.taobao.com/search?q=" + url.QueryEscape(keyword)
}

// PhysicalWishes 实物商品（样例数据）。
//
// 这些是**样例** —— 合作商还没正式接进来，先用手写的几条把链路跑通。
// 接商家时整块替换，不需要改任何逻辑。
//
// sortOrder 从 20 起：故意排在游戏盒子（100+）**前面**。
// 21 款游戏盒子如果排在前面，会把泡泡玛特、学习机这些**商家商品**
// 全部压到屏幕外 —— 而「接商家」才是这个商城的目的。
var PhysicalWishes = []*entities.WishItem{
	{
		ItemID:      "wish-popmart",
		Kind:        entities.WishKindCollectible,
		Name:        "泡泡玛特盲盒",
		Emoji:       "🧸",
		Tint:        "grape",
		PartnerName: "泡泡玛特",
		PriceCents:  6900,
		AgeRange:    entities.JSONIntArray{6, 99},
		Note:        "随机款，不指定款式",
		Notice:      "盲盒不指定款式。拆开之后不能退换。",
		BuyURL:      taobaoSearch("泡泡玛特 盲盒"),
		BuyLabel:    "去淘宝购买",
		SortOrder:   20,
	},
	{
		ItemID:      "wish-study-pad",
		Kind:        entities.WishKindStudy,
		Name:        "儿童学习机",
		Emoji:       "📚",
		Tint:        "sky",
		PartnerName: "小天才",
		PriceCents:  39900,
		AgeRange:    entities.JSONIntArray{6, 12},
		Note:        "10.1 英寸 · 含学习卡",
		Notice:      "大件商品，建议先和爸妈一起看看型号再决定。",
		BuyURL:      taobaoSearch("儿童学习机 10.1"),
		BuyLabel:    "去淘宝看看",
		SortOrder:   21,
	},
	{
		ItemID:      "wish-dino",
		Kind:        entities.WishKindToy,
		Name:        "恐龙模型套装",
		Emoji:       "🦖",
		Tint:        "grass",
		PartnerName: "玩具仓",
		PriceCents:  8900,
		AgeRange:    entities.JSONIntArray{4, 10},
		Note:        "12 只装 · 带收纳盒",
		BuyURL:      taobaoSearch("恐龙模型 套装 12只"),
		BuyLabel:    "去淘宝购买",
		SortOrder:   22,
	},
	{
		ItemID:      "wish-puzzle",
		Kind:        entities.WishKindToy,
		Name:        "1000 片拼图",
		Emoji:       "🧩",
		Tint:        "tan",
		PartnerName: "拼图工坊",
		PriceCents:  4500,
		AgeRange:    entities.JSONIntArray{8, 99},
		Note:        "风景款 · 成品 70×50cm",
		BuyURL:      taobaoSearch("1000片 拼图 风景"),
		BuyLabel:    "去淘宝购买",
		SortOrder:   23,
	},
	{
		ItemID:      "wish-sticker",
		Kind:        entities.WishKindToy,
		Name:        "闪光贴纸册",
		Emoji:       "✨",
		Tint:        "berry",
		PartnerName: "文具角",
		PriceCents:  1900,
		AgeRange:    entities.JSONIntArray{4, 12},
		Note:        "含 200 张 · 可反复贴",
		BuyURL:      taobaoSearch("儿童 闪光贴纸 200张"),
		BuyLabel:    "去淘宝购买",
		SortOrder:   24,
	},
	// ---- 2026-10-29 新增两档分类的样例（用户要求「前面再加美食，出行」）----
	//
	// 没有这两条的话，心愿页和兑换页会各多出一个**点进去是空的**分类按钮
	// （chips 是从分类表生成的）。和上面几条一样是样例数据，接商家时整块替换。
	{
		ItemID:      "wish-cake",
		Kind:        entities.WishKindFood,
		Name:        "生日蛋糕",
		Emoji:       "🍰",
		Tint:        "berry",
		PartnerName: "甜心烘焙",
		PriceCents:  12800,
		AgeRange:    entities.JSONIntArray{3, 99},
		Note:        "6 寸 · 可以写名字",
		Notice:      "要提前一天预订，爸妈下单时记得选日期。",
		BuyURL:      taobaoSearch("生日蛋糕 6寸 儿童"),
		BuyLabel:    "去淘宝预订",
		SortOrder:   25,
	},
	{
		ItemID:      "wish-zoo",
		Kind:        entities.WishKindOuting,
		Name:        "动物园一日游",
		Emoji:       "🎡",
		Tint:        "grass",
		PartnerName: "城市动物园",
		PriceCents:  12000,
		AgeRange:    entities.JSONIntArray{3, 12},
		Note:        "含 1 大 1 小两张门票",
		Notice:      "出行类按当天预约情况走，去之前先确认场次。",
		BuyURL:      taobaoSearch("动物园 门票 亲子"),
		BuyLabel:    "去淘宝看看",
		SortOrder:   26,
	},
}

// EnsureWishCatalog 幂等地写入心愿商城商品目录。
//
// ## 两类商品，来源不同
//
//   - **盲盒**：一条，池子 = **全部游戏的 id**
//   - **游戏盒子**：从 `game` 表**推导**，不手写
//
// 为什么游戏盒子要推导而不是手写：21 款游戏各写一条的话，
// 下次加游戏就一定会漏 —— 而漏掉的那款在界面上表现为
// 「盒子里没有它」，**不报错**。
//
//   - **实物**：手写（这些是商家给的东西，没法推导）
func EnsureWishCatalog(ctx context.Context, wishRepo *repository.WishRepository, gameRepo *repository.GameRepository) error {
	// 1. 全部游戏（盲盒池 + 游戏盒子都从它推导）
	games, _, err := gameRepo.ListGames(ctx, &entities.GameListReq{})
	if err != nil {
		return fmt.Errorf("list games for wish catalog: %w", err)
	}

	pool := make([]string, 0, len(games))
	for _, g := range games {
		pool = append(pool, g.GameID)
	}

	// 2. 盲盒
	blindbox := &entities.WishItem{
		ItemID:    WishBlindBoxID,
		Kind:      entities.WishKindBlindbox,
		Name:      "游戏盲盒",
		Emoji:     "🎁",
		Tint:      "sun",
		Pool:      entities.JSONStringArray(pool),
		Note:      "随机开出 1 款你还没有的游戏",
		Notice:    "不会开出你已经有的游戏。全部都有了就不能买啦。",
		SortOrder: 0,
	}
	if err := wishRepo.UpsertItem(ctx, blindbox); err != nil {
		return fmt.Errorf("upsert blindbox: %w", err)
	}

	// 2b. 5 连开盲盒
	//
	// 池子照抄单开的（全部游戏）：拥有关系是每个家庭不同的，
	// 不能把「剩下的那几款」写进池子。
	blindbox5 := &entities.WishItem{
		ItemID:      WishBlindBox5ID,
		Kind:        entities.WishKindBlindbox,
		Name:        "游戏盲盒 · 5 连开",
		Emoji:       "🎉",
		Tint:        "grape",
		Pool:        entities.JSONStringArray(pool),
		UnlockCount: 5,
		Note:        "一次开出 5 款你还没有的游戏",
		Notice:      "只会开出你还没有的游戏。没解锁的不足 5 款时换不了 —— 等新的游戏再来。",
		SortOrder:   1,
	}
	if err := wishRepo.UpsertItem(ctx, blindbox5); err != nil {
		return fmt.Errorf("upsert blindbox5: %w", err)
	}

	// 3. 游戏盒子（一款一条，从 game 表推导）
	//    sortOrder 从 100 起：故意排在实物后面（见 PhysicalWishes 注释）
	for i, g := range games {
		box := &entities.WishItem{
			ItemID:    "wish-game-" + g.GameID,
			Kind:      entities.WishKindGamebox,
			Name:      g.Name,
			Emoji:     g.Emoji,
			Tint:      g.Tint,
			GameID:    g.GameID,
			Note:      g.Tagline,
			SortOrder: 100 + i,
		}
		if err := wishRepo.UpsertItem(ctx, box); err != nil {
			return fmt.Errorf("upsert game box %s: %w", g.GameID, err)
		}
	}

	// 4. 实物
	for _, p := range PhysicalWishes {
		if err := wishRepo.UpsertItem(ctx, p); err != nil {
			return fmt.Errorf("upsert physical wish %s: %w", p.ItemID, err)
		}
	}

	logger.ZInfo("wish catalog seeded",
		zap.Int("games", len(games)),
		zap.Int("physical", len(PhysicalWishes)),
		// 2 = 单开盲盒 + 5 连开盲盒。写死 1 的话日志会少报一条，
		// 而「库里条数对不上」正是排查目录问题时最先看的那一行。
		zap.Int("total", 2+len(games)+len(PhysicalWishes)))
	return nil
}
