package service

import (
	"context"
	"testing"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"
)

// ============================================================
// /wallet/import：把本地版留下的余额一次性结转进账号
// ------------------------------------------------------------
// 回归的是「界面余额 = 服务端 + 本机旧账」：
// 实测（2026-11-03）服务端 points=219，插一行本地旧账 +1000 之后界面显示
// 1219，重开还是 1219 —— 孩子看到 1219 分，兑换时被服务端拒掉，
// 而且只说「积分不够」，不报错。
//
// 这里守三件事：
//   ① 三套币一次事务写进去，余额和账本对得上；
//   ② 同一个幂等键重试**不会**多记（客户端会在失败后重试）；
//   ③ 全是 0 的载荷被拒 —— 客户端算错了要吵，不要静默成功。
// ============================================================

func newImportSrv(t *testing.T) (*WalletService, *repository.WalletRepository) {
	t.Helper()
	db := newTestDB(t)
	repo := repository.NewWalletRepository(db)
	return &WalletService{Repo: repo}, repo
}

// TestImportLegacyWritesThreeCurrencies 三套币一次写进去，余额与账本一致
func TestImportLegacyWritesThreeCurrencies(t *testing.T) {
	srv, repo := newImportSrv(t)
	ctx := context.Background()
	const uid = uint(1)

	resp, err := srv.ImportLegacy(ctx, uid, &entities.WalletImportReq{
		Key:      "dev-a",
		Points:   1000,
		Coupons:  7,
		Diamonds: 3,
		Counts:   map[string]int64{"points": 12, "coupons": 2, "diamonds": 1},
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !resp.Imported {
		t.Fatal("第一次结转 Imported 应该是 true")
	}
	if resp.Wallet == nil || resp.Wallet.Points != 1000 || resp.Wallet.Coupon != 7 || resp.Wallet.Diamond != 3 {
		t.Fatalf("结转后余额不对：%+v", resp.Wallet)
	}

	for _, tc := range []struct {
		cur  entities.Currency
		want int64
	}{
		{entities.CurrencyPoints, 1000},
		{entities.CurrencyCoupon, 7},
		{entities.CurrencyDiamond, 3},
	} {
		sum, err := repo.SumByCurrency(ctx, uid, tc.cur)
		if err != nil {
			t.Fatalf("sum %s: %v", tc.cur, err)
		}
		if sum != tc.want {
			t.Fatalf("%s 账本合计 = %d, want %d", tc.cur, sum, tc.want)
		}
	}
}

// TestImportLegacyIdempotent 同一个 key 调两次只生效一次
//
// 客户端拿到响应之前可能断网，恢复后会重试 —— 重试多记一笔的话，
// 孩子的分数会凭空翻倍，而且账本上看不出哪笔是假的。
func TestImportLegacyIdempotent(t *testing.T) {
	srv, repo := newImportSrv(t)
	ctx := context.Background()
	const uid = uint(2)
	req := &entities.WalletImportReq{Key: "dev-b", Points: 500, Coupons: 2}

	first, err := srv.ImportLegacy(ctx, uid, req)
	if err != nil {
		t.Fatalf("第一次: %v", err)
	}
	if !first.Imported {
		t.Fatal("第一次 Imported 应该是 true")
	}

	second, err := srv.ImportLegacy(ctx, uid, req)
	if err != nil {
		t.Fatalf("第二次不该报错（重试是正常路径）: %v", err)
	}
	if second.Imported {
		t.Fatal("第二次 Imported 应该是 false")
	}
	if second.Points != 0 || second.Coupons != 0 {
		t.Fatalf("重试不该再写数量：points=%d coupons=%d", second.Points, second.Coupons)
	}
	if second.Wallet == nil || second.Wallet.Points != 500 || second.Wallet.Coupon != 2 {
		t.Fatalf("重试后余额不该变：%+v", second.Wallet)
	}

	sum, _ := repo.SumByCurrency(ctx, uid, entities.CurrencyPoints)
	if sum != 500 {
		t.Fatalf("账本合计 = %d, want 500（重试不能多记）", sum)
	}
}

// TestImportLegacyDifferentKeyAddsAgain 换台设备、载荷不同 ⇒ 各写各的
//
// 幂等键里带设备 id，就是为了让「第二台设备也有本地旧账」能搬上去，
// 而不是被第一台的键吞掉。
func TestImportLegacyDifferentKeyAddsAgain(t *testing.T) {
	srv, _ := newImportSrv(t)
	ctx := context.Background()
	const uid = uint(3)

	if _, err := srv.ImportLegacy(ctx, uid, &entities.WalletImportReq{Key: "dev-1", Points: 100}); err != nil {
		t.Fatal(err)
	}
	resp, err := srv.ImportLegacy(ctx, uid, &entities.WalletImportReq{Key: "dev-2", Points: 200})
	if err != nil {
		t.Fatalf("第二台设备: %v", err)
	}
	if !resp.Imported {
		t.Fatal("不同 key 应该照常写入")
	}
	if resp.Wallet.Points != 300 {
		t.Fatalf("points = %d, want 300（100 + 200）", resp.Wallet.Points)
	}
}

// TestImportLegacyOnTopOfExisting 账号本来就有余额时是「加」，不是「覆盖」
//
// 服务端已经有的分（云端挣的）和本机旧账（本地版挣的）是两笔真实收入，
// 都该留下。
func TestImportLegacyOnTopOfExisting(t *testing.T) {
	srv, _ := newImportSrv(t)
	ctx := context.Background()
	const uid = uint(4)

	if _, _, err := srv.Change(ctx, repository.BalanceChange{
		UID: uid, Currency: entities.CurrencyPoints, Delta: 219,
		Source: entities.SourceTaskReward, Memo: "云端挣的",
	}); err != nil {
		t.Fatalf("先垫 219: %v", err)
	}

	resp, err := srv.ImportLegacy(ctx, uid, &entities.WalletImportReq{Key: "dev-x", Points: 1000})
	if err != nil {
		t.Fatalf("结转: %v", err)
	}
	if resp.Wallet.Points != 1219 {
		t.Fatalf("points = %d, want 1219（219 + 1000）", resp.Wallet.Points)
	}
}

// TestImportLegacyPartialRetry 只搬了一半之后再重试：已搬的跳过，没搬的补上
func TestImportLegacyPartialRetry(t *testing.T) {
	srv, _ := newImportSrv(t)
	ctx := context.Background()
	const uid = uint(5)

	// 第一次只有积分（比如那次载荷里兑换券是 0）
	if _, err := srv.ImportLegacy(ctx, uid, &entities.WalletImportReq{Key: "k", Points: 100}); err != nil {
		t.Fatal(err)
	}
	// 同一个 key、这次多带兑换券
	resp, err := srv.ImportLegacy(ctx, uid, &entities.WalletImportReq{Key: "k", Points: 100, Coupons: 4})
	if err != nil {
		t.Fatalf("重试: %v", err)
	}
	if !resp.Imported {
		t.Fatal("兑换券是新的，应该写进去")
	}
	if resp.Points != 0 {
		t.Fatalf("积分已经搬过，不该再写：%d", resp.Points)
	}
	if resp.Wallet.Points != 100 || resp.Wallet.Coupon != 4 {
		t.Fatalf("余额不对：%+v", resp.Wallet)
	}
}

// TestImportLegacyRejectsEmpty 全是 0 的载荷要吵，不要静默成功
func TestImportLegacyRejectsEmpty(t *testing.T) {
	srv, _ := newImportSrv(t)
	ctx := context.Background()

	_, err := srv.ImportLegacy(ctx, uint(6), &entities.WalletImportReq{Key: "k"})
	if !apperrors.Is(err, apperrors.ImportPayloadInvalid) {
		t.Fatalf("err = %v, want ImportPayloadInvalid", err)
	}
}
