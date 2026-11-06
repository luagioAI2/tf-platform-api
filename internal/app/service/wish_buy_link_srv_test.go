package service

import (
	"context"
	"testing"

	"tf-api/internal/app/entities"
)

/* ============================================================
   外链下发（2026-11-06 第七轮）
   ------------------------------------------------------------
   用户原话：「有没有去淘宝的按钮，根据后台礼品有没有配置链接
   （淘宝、京东等等），如果没有就不显示。」

   「后台配的」= `wish_item.buy_url`（tf-admin 商城列表那个输入框）。
   在这一轮之前孩子端的商品视图**根本不含**这两个字段，客户端那颗
   「去购买」是自己拼的淘宝搜索页 —— 后台配没配都长一个样。

   所以这两条断言钉在**服务端**这一侧：
     · 配了就下发（孩子视角也要有）
     · 没配就是空串（客户端据此不渲染按钮）
   ============================================================ */

// seedToyWithLink 造一件实物商品，`buyURL` 传空 = 运营没配链接。
func seedToyWithLink(t *testing.T, svc *WishService, buyURL, buyLabel string) string {
	t.Helper()
	item := &entities.WishItem{
		ItemID:   "wish-toy-link-test",
		Kind:     entities.WishKindToy,
		Name:     "测试玩具",
		Emoji:    "🧸",
		BuyURL:   buyURL,
		BuyLabel: buyLabel,
		// ★ 必须显式写 true：`BuyOpen` 是 bool + `default:true`，零值会被
		//   GORM 当成「没填」而落成数据库默认值 —— 写死 true 是为了让这条
		//   测试不依赖那个默认值（默认值本身有单独一条测）。
		BuyOpen:   true,
		SortOrder: 10,
	}
	if err := svc.Repo.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	return item.ItemID
}

func TestWishListItemsCarriesBuyLink(t *testing.T) {
	svc, _, _ := newWishTestFixture(t)
	ctx := context.Background()

	const wantURL = "https://item.taobao.com/item.htm?id=42"
	itemID := seedToyWithLink(t, svc, wantURL, "去淘宝购买")

	list, err := svc.ListItems(ctx, wishTestUID, &entities.ListWishItemReq{})
	if err != nil {
		t.Fatalf("list items: %v", err)
	}

	var got *entities.WishItemView
	for _, v := range list {
		if v.ItemID == itemID {
			got = v
			break
		}
	}
	if got == nil {
		t.Fatal("列表里没有这件商品 —— 后面两条断言等于没测")
	}
	if got.BuyURL != wantURL {
		t.Fatalf("孩子端的商品视图没带外链：%q（后台配的是 %q）", got.BuyURL, wantURL)
	}
	if got.BuyLabel != "去淘宝购买" {
		t.Fatalf("按钮文字没带出来：%q", got.BuyLabel)
	}
}

func TestWishListItemsEmptyBuyLinkStaysEmpty(t *testing.T) {
	svc, _, _ := newWishTestFixture(t)
	ctx := context.Background()

	itemID := seedToyWithLink(t, svc, "", "")

	list, err := svc.ListItems(ctx, wishTestUID, &entities.ListWishItemReq{})
	if err != nil {
		t.Fatalf("list items: %v", err)
	}

	var got *entities.WishItemView
	for _, v := range list {
		if v.ItemID == itemID {
			got = v
			break
		}
	}
	if got == nil {
		t.Fatal("列表里没有这件商品")
	}
	// ⚠️ 空串是**判据**，不是「没数据」：客户端就靠它决定不渲染「去购买」。
	// 服务端要是顺手补一条默认淘宝搜索页，这条断言就是最后一道闸。
	if got.BuyURL != "" {
		t.Fatalf("后台没配链接，视图却带出了 %q —— 客户端会摆一颗后台管不到的按钮", got.BuyURL)
	}
}

func TestWishDetailCarriesBuyLinkForChild(t *testing.T) {
	svc, _, _ := newWishTestFixture(t)
	ctx := context.Background()

	const wantURL = "https://item.jd.com/100012043978.html"
	itemID := seedToyWithLink(t, svc, wantURL, "去京东看看")

	// isParent = false：孩子视角。这一支原来是**专门把外链藏掉**的。
	resp, err := svc.Detail(ctx, wishTestUID, itemID, false)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if resp.BuyURL != wantURL {
		t.Fatalf("孩子视角的详情页没带外链：%q", resp.BuyURL)
	}
	if resp.BuyLabel != "去京东看看" {
		t.Fatalf("按钮文字没带出来：%q", resp.BuyLabel)
	}
}

/* ============================================================
   `buy_open`：临时关掉购买（2026-11-06 晚）
   ------------------------------------------------------------
   用户原话：「最后多一个标记，是否开启购买。有时候淘宝京东无法购买了，
   就会在后台临时关闭。」

   ★ 判据有两半，缺一不可：
     ① 关掉之后**不下发外链**（两端都不摆「去购买」）；
     ② 商品**还在列表里**（这不是下架 —— 关的是「怎么买」，不是「卖不卖」）。
   只写 ① 的话，把 `archived` 也一起置 true 的写法照样能过，
   而孩子会发现整件商品凭空消失。
   ============================================================ */

// seedClosedToy 造一件「配了链接但关了购买」的商品。
//
// ⚠️ 关的动作走 `Update`（map 更新），和 tf-admin `UpdateItem` 一模一样 ——
//
//	直接 `Create` 一个 `BuyOpen:false` 的会被 `default:true` 吃掉，
//	测出来永远是「开」，等于没测。
func seedClosedToy(t *testing.T, svc *WishService, buyURL, buyLabel string) string {
	t.Helper()
	itemID := seedToyWithLink(t, svc, buyURL, buyLabel)
	if err := svc.Repo.DB.Model(&entities.WishItem{}).
		Where("item_id = ?", itemID).
		Update("buy_open", false).Error; err != nil {
		t.Fatalf("关掉购买: %v", err)
	}
	return itemID
}

func TestWishBuyOpenClosedHidesLinkButKeepsItem(t *testing.T) {
	svc, _, _ := newWishTestFixture(t)
	ctx := context.Background()

	const wantURL = "https://item.taobao.com/item.htm?id=42"
	itemID := seedClosedToy(t, svc, wantURL, "去淘宝购买")

	list, err := svc.ListItems(ctx, wishTestUID, &entities.ListWishItemReq{})
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	var got *entities.WishItemView
	for _, v := range list {
		if v.ItemID == itemID {
			got = v
			break
		}
	}
	if got == nil {
		t.Fatal("关了购买商品却从列表里消失了 —— 那不是临时关闭，是下架")
	}
	if got.BuyURL != "" || got.BuyLabel != "" {
		t.Fatalf("关了购买还带出外链 %q / %q —— 客户端会摆一颗点了买不了的按钮", got.BuyURL, got.BuyLabel)
	}

	// 详情页同一条判据（两支代码路径，别只测列表）
	resp, err := svc.Detail(ctx, wishTestUID, itemID, false)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if resp.BuyURL != "" {
		t.Fatalf("关了购买，详情页还带出 %q", resp.BuyURL)
	}
}

func TestWishBuyLinkHelper(t *testing.T) {
	// 纯函数口径：四个下发点都靠它，别在别处再判一次。
	cases := []struct {
		name     string
		item     *entities.WishItem
		wantURL  string
		wantText string
	}{
		{"开了 + 配了链接", &entities.WishItem{BuyURL: "https://a.example/x", BuyLabel: "去京东", BuyOpen: true}, "https://a.example/x", "去京东"},
		{"关了 + 配了链接", &entities.WishItem{BuyURL: "https://a.example/x", BuyLabel: "去京东", BuyOpen: false}, "", ""},
		{"开了 + 没配链接", &entities.WishItem{BuyOpen: true}, "", ""},
		{"nil 商品", nil, "", ""},
	}
	for _, c := range cases {
		url, label := c.item.BuyLink()
		if url != c.wantURL || label != c.wantText {
			t.Fatalf("%s：拿到 %q / %q，期望 %q / %q", c.name, url, label, c.wantURL, c.wantText)
		}
	}
}
