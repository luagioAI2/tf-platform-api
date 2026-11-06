package service

import (
	"context"
	"testing"

	"tf-api/internal/app/entities"
)

/* ============================================================
   一次性兑换（2026-11-06）
   ------------------------------------------------------------
   用户原话：「兑换完后，如果是一次性的，就删除，心愿商城 物品状态
   又变回 我想要。」

   「删除」= 关掉这一件商品的 offer（`closed_at`），不是删商品 ——
   目录是常量，孩子的兑换历史还要能解释。

   ## 为什么必须在 Redeem 的事务里关

   交给客户端兑换成功之后再发一次 `set-offer(0)` 的话，那次请求失败时
   券已经扣了、商品却还挂在兑换列表上 ⇒ **孩子能再换一遍**，
   而且两边都不报错（账本只看 delta 之和）。所以这两条断言都钉在
   服务端这一侧：关掉 + 再换必须被拒。
   ============================================================ */

func boolPtr(b bool) *bool { return &b }

func TestWishRedeemOneTimeClosesOffer(t *testing.T) {
	svc, walletSrv, _ := newWishTestFixture(t)
	ctx := context.Background()

	itemID := seedBlindBox(t, svc, walletSrv, []string{"game-a", "game-b"}, 1, 10, 100)

	// 家长定价时勾「只兑换一次」
	if _, err := svc.SetOffer(ctx, wishTestUID, &entities.WishSetOfferReq{
		ItemID:       itemID,
		VoucherPrice: 10,
		OneTime:      boolPtr(true),
	}); err != nil {
		t.Fatalf("set offer: %v", err)
	}

	if _, err := svc.Redeem(ctx, wishTestUID, &entities.WishRedeemReq{ItemID: itemID}); err != nil {
		t.Fatalf("redeem: %v", err)
	}

	offer, err := svc.Repo.GetOffer(ctx, wishTestUID, itemID)
	if err != nil {
		t.Fatalf("get offer: %v", err)
	}
	if offer == nil {
		t.Fatal("offer 行被删了 —— 关掉要写 closed_at，历史还要能解释")
	}
	if offer.ClosedAt == 0 {
		t.Fatal("一次性商品兑换成功后 offer 没被关掉 —— 它会一直挂在兑换列表上")
	}
	if !offer.OneTime {
		t.Fatal("一次性标记丢了")
	}

	// 关掉之后不能再换：这条是「孩子能再换一遍」的直接判据
	if _, err := svc.Redeem(ctx, wishTestUID, &entities.WishRedeemReq{ItemID: itemID}); err == nil {
		t.Fatal("offer 已经关了还能再兑换一次")
	}
}

func TestWishRedeemRepeatableKeepsOffer(t *testing.T) {
	svc, walletSrv, _ := newWishTestFixture(t)
	ctx := context.Background()

	itemID := seedBlindBox(t, svc, walletSrv, []string{"game-a", "game-b"}, 1, 10, 100)

	// 不勾一次性（默认）
	if _, err := svc.SetOffer(ctx, wishTestUID, &entities.WishSetOfferReq{
		ItemID:       itemID,
		VoucherPrice: 10,
	}); err != nil {
		t.Fatalf("set offer: %v", err)
	}

	if _, err := svc.Redeem(ctx, wishTestUID, &entities.WishRedeemReq{ItemID: itemID}); err != nil {
		t.Fatalf("redeem: %v", err)
	}

	offer, err := svc.Repo.GetOffer(ctx, wishTestUID, itemID)
	if err != nil {
		t.Fatalf("get offer: %v", err)
	}
	if offer.ClosedAt != 0 {
		t.Fatal("可重复兑换的商品被关掉了 —— 它会从兑换列表里消失")
	}
}

/* 关闭商品（`voucher_price = 0`）时**不能**顺手把一次性标记冲成 false。

   客户端「关闭」那条请求不带 `one_time`（指针 nil = 这次没提这件事）。
   用 bool 而不是 *bool 的话，关闭会把它写成 false —— 家长下次重新开放时，
   一件明明是一次性的玩具就变成可以无限兑换了，而界面上看不出任何异常。 */
func TestWishSetOfferCloseKeepsOneTime(t *testing.T) {
	svc, walletSrv, _ := newWishTestFixture(t)
	ctx := context.Background()

	itemID := seedBlindBox(t, svc, walletSrv, []string{"game-a", "game-b"}, 1, 10, 100)

	if _, err := svc.SetOffer(ctx, wishTestUID, &entities.WishSetOfferReq{
		ItemID:       itemID,
		VoucherPrice: 10,
		OneTime:      boolPtr(true),
	}); err != nil {
		t.Fatalf("open: %v", err)
	}

	// 关闭：价 0、不带 one_time
	if _, err := svc.SetOffer(ctx, wishTestUID, &entities.WishSetOfferReq{
		ItemID:       itemID,
		VoucherPrice: 0,
	}); err != nil {
		t.Fatalf("close: %v", err)
	}

	offer, err := svc.Repo.GetOffer(ctx, wishTestUID, itemID)
	if err != nil {
		t.Fatalf("get offer: %v", err)
	}
	if offer.ClosedAt == 0 {
		t.Fatal("关闭没写 closed_at")
	}
	if !offer.OneTime {
		t.Fatal("关闭商品把「一次性」冲成了 false —— 重新开放后就能无限兑换了")
	}
}
