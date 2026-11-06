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
		ItemID:    "wish-toy-link-test",
		Kind:      entities.WishKindToy,
		Name:      "测试玩具",
		Emoji:     "🧸",
		BuyURL:    buyURL,
		BuyLabel:  buyLabel,
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
