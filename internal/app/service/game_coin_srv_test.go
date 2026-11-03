package service

import (
	"context"
	"testing"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"
)

// newCoinSvc 造一个「一款游戏 + 一个用户 + 若干游戏币」的服务实例。
//
// 币是用 `manual_adjust` 直接记上去的 —— 走 `Buy` 的话要先把积分准备好，
// 而这两个用例要验的是**兑换**，不是买币。
func newCoinSvc(t *testing.T, gameID string, voucherPerCoin int64, coins int64) (*GameCoinService, *repository.GameCoinRepository, uint) {
	t.Helper()
	db := newTestDB(t)
	ctx := context.Background()

	gameRepo := repository.NewGameRepository(db)
	if err := db.Create(&entities.Game{
		GameID:         gameID,
		Name:           "测试游戏",
		Status:         entities.GameStatusAvailable,
		VoucherPerCoin: voucherPerCoin,
	}).Error; err != nil {
		t.Fatalf("create game: %v", err)
	}

	coinRepo := repository.NewGameCoinRepository(db)
	svc := &GameCoinService{
		Repo:      coinRepo,
		GameRepo:  gameRepo,
		WalletSrv: ProvideWalletService(repository.NewWalletRepository(db)),
		DB:        db,
	}

	const uid uint = 1
	if coins > 0 {
		if _, _, err := coinRepo.ApplyChange(ctx, repository.GameCoinChange{
			UID:    uid,
			GameID: gameID,
			Delta:  coins,
			Source: entities.GameCoinSourceAdjust,
			Memo:   "测试发币",
		}); err != nil {
			t.Fatalf("grant coins: %v", err)
		}
	}
	return svc, coinRepo, uid
}

// TestRedeemUsesPerGameRate 汇率来自 `game.voucher_per_coin`，不是写死的 1:1。
//
// ★ 这条钉的是「界面说几张、实际到几张」。原来 `rate := int64(1)` 写死，
// manifest 写 2 的游戏会**少发一半**，而且不报错（客户端只 console.warn）。
func TestRedeemUsesPerGameRate(t *testing.T) {
	const gameID = "rate3"
	svc, coinRepo, uid := newCoinSvc(t, gameID, 3, 10)
	ctx := context.Background()

	resp, err := svc.Redeem(ctx, uid, &entities.GameCoinRedeemReq{GameID: gameID, Count: 2})
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	// 2 个币 × 3 = 6 张券
	if resp.Vouchers != 6 {
		t.Fatalf("券数 = %d，应该是 6（2 币 × 汇率 3）", resp.Vouchers)
	}
	// 币扣掉 2 个
	if resp.Balance != 8 {
		t.Fatalf("币余额 = %d，应该是 8", resp.Balance)
	}
	bal, err := coinRepo.SumByGame(ctx, uid, gameID)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if bal != 8 {
		t.Fatalf("账本里币余额 = %d，应该是 8（响应和账本必须一致）", bal)
	}
}

// TestRedeemClosedWhenZero 汇率 0 = **不开放兑换**，必须明确拒绝。
//
// ★ 原来这里无条件按 1:1 发 —— 也就是说 manifest 里根本没写
// `voucherPerCoin` 的 19 款游戏（= 不开放）全都能换，等于绕开了家长/
// 作者的开关。判据不只是「返回错误」，还要**什么都没写**：
// 币不能少、券不能多。
func TestRedeemClosedWhenZero(t *testing.T) {
	const gameID = "closed"
	svc, coinRepo, uid := newCoinSvc(t, gameID, 0, 10)
	ctx := context.Background()

	_, err := svc.Redeem(ctx, uid, &entities.GameCoinRedeemReq{GameID: gameID, Count: 2})
	if err == nil {
		t.Fatal("汇率 0 还能兑换 —— 开关没生效")
	}
	if !apperrors.Is(err, apperrors.GameCoinRedeemClosed) {
		t.Fatalf("错误码不对：%v（期望 %d）", err, apperrors.GameCoinRedeemClosed)
	}

	bal, err := coinRepo.SumByGame(ctx, uid, gameID)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if bal != 10 {
		t.Fatalf("币被扣了：%d，应该还是 10（拒绝 = 什么都不写）", bal)
	}

	var n int64
	if err := svc.DB.Model(&entities.LedgerEntry{}).
		Where("uid = ? AND currency = ?", uid, entities.CurrencyCoupon).
		Count(&n).Error; err != nil {
		t.Fatalf("count coupon ledger: %v", err)
	}
	if n != 0 {
		t.Fatalf("发了 %d 张券的账 —— 拒绝时不能有任何券流水", n)
	}
}
