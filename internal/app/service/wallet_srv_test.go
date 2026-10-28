package service

import (
	"context"
	"sync"
	"testing"

	"tf-api/internal/app/entities"
	"tf-api/internal/app/service/repository"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// newTestDB 创建内存 SQLite 数据库并建表
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := repository.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// 每个测试用独立表：清空数据
	for _, m := range repository.Models() {
		_ = db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(m).Error
	}
	return db
}

// TestWalletBalanceChange 验证记账与余额一致性
func TestWalletBalanceChange(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewWalletRepository(db)
	ctx := context.Background()

	const uid = uint(1)

	// 初始钱包
	if _, err := repo.GetOrCreateWallet(ctx, uid); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		delta   int64
		wantBal int64
	}{
		{100, 100},
		{-30, 70},
		{50, 120},
	}

	for i, c := range cases {
		bal, entry, err := repo.ApplyChange(ctx, repository.BalanceChange{
			UID:            uid,
			Currency:       entities.CurrencyPoints,
			Delta:          c.delta,
			Source:         entities.SourceTaskReward,
			Memo:           "test",
			IdempotencyKey: "test:" + itoa(uint(i)),
		})
		if err != nil {
			t.Fatalf("case %d: apply change: %v", i, err)
		}
		if bal != c.wantBal {
			t.Fatalf("case %d: balance = %d, want %d", i, bal, c.wantBal)
		}
		if entry.BalanceAfter != c.wantBal {
			t.Fatalf("case %d: entry balanceAfter = %d, want %d", i, entry.BalanceAfter, c.wantBal)
		}
	}

	// 对账：钱包余额 == 账本 sum
	w, _ := repo.GetWallet(ctx, uid)
	sum, _ := repo.SumByCurrency(ctx, uid, entities.CurrencyPoints)
	if w.Points != sum {
		t.Fatalf("reconcile mismatch: wallet=%d ledger=%d", w.Points, sum)
	}
}

// TestWalletInsufficientBalance 余额不足时必须拒绝且不写账本
func TestWalletInsufficientBalance(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewWalletRepository(db)
	ctx := context.Background()

	const uid = uint(2)
	_, _ = repo.GetOrCreateWallet(ctx, uid)

	// 先给 10
	if _, _, err := repo.ApplyChange(ctx, repository.BalanceChange{
		UID: uid, Currency: entities.CurrencyPoints, Delta: 10,
		Source: entities.SourceTaskReward,
	}); err != nil {
		t.Fatal(err)
	}

	// 扣 20 应该失败
	_, _, err := repo.ApplyChange(ctx, repository.BalanceChange{
		UID: uid, Currency: entities.CurrencyPoints, Delta: -20,
		Source: entities.SourceSeedPurchase,
	})
	if err == nil {
		t.Fatal("expected insufficient balance error")
	}

	// 余额与账本都不变
	w, _ := repo.GetWallet(ctx, uid)
	if w.Points != 10 {
		t.Fatalf("balance changed after failed change: %d", w.Points)
	}
	sum, _ := repo.SumByCurrency(ctx, uid, entities.CurrencyPoints)
	if sum != 10 {
		t.Fatalf("ledger changed after failed change: %d", sum)
	}
}

// TestWalletIdempotency 同一幂等键只生效一次
func TestWalletIdempotency(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewWalletRepository(db)
	ctx := context.Background()

	const uid = uint(3)
	_, _ = repo.GetOrCreateWallet(ctx, uid)

	change := repository.BalanceChange{
		UID:            uid,
		Currency:       entities.CurrencyPoints,
		Delta:          100,
		Source:         entities.SourceRegisterGift,
		IdempotencyKey: "gift:3",
	}

	if _, _, err := repo.ApplyChange(ctx, change); err != nil {
		t.Fatal(err)
	}
	// 第二次应返回 ErrDuplicateLedger
	_, _, err := repo.ApplyChange(ctx, change)
	if err != repository.ErrDuplicateLedger {
		t.Fatalf("expected ErrDuplicateLedger, got %v", err)
	}

	w, _ := repo.GetWallet(ctx, uid)
	if w.Points != 100 {
		t.Fatalf("idempotency failed: balance=%d, want 100", w.Points)
	}
}

// TestWalletConcurrent 并发扣款不丢钱、不超扣
//
// 这是最关键的一个测试：5W 并发下如果锁失效，余额会算错。
func TestWalletConcurrent(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewWalletRepository(db)
	ctx := context.Background()

	const uid = uint(4)
	const initial = int64(1000)
	const workers = 20
	const perWorker = 10
	const delta = int64(-1) // 每次扣 1

	_, _ = repo.GetOrCreateWallet(ctx, uid)
	if _, _, err := repo.ApplyChange(ctx, repository.BalanceChange{
		UID: uid, Currency: entities.CurrencyPoints, Delta: initial,
		Source: entities.SourceTaskReward,
	}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, workers*perWorker)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				_, _, err := repo.ApplyChange(ctx, repository.BalanceChange{
					UID:      uid,
					Currency: entities.CurrencyPoints,
					Delta:    delta,
					Source:   entities.SourceSeedPurchase,
					Memo:     "concurrent test",
				})
				if err != nil {
					errCh <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)

	// 并发下 SQLite 可能返回 "database is locked"，允许失败但要保证账实相符
	var failCount int
	for err := range errCh {
		failCount++
		t.Logf("concurrent change failed (expected under SQLite lock): %v", err)
	}

	w, err := repo.GetWallet(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := repo.SumByCurrency(ctx, uid, entities.CurrencyPoints)
	if err != nil {
		t.Fatal(err)
	}

	// 核心断言：钱包余额必须等于账本汇总（无论成功多少次）
	if w.Points != sum {
		t.Fatalf("RECONCILE FAILED: wallet=%d ledger=%d (failures=%d)", w.Points, sum, failCount)
	}
	t.Logf("final balance=%d ledger=%d failures=%d", w.Points, sum, failCount)

	// 成功次数 = (initial - final) / |delta|
	successCount := initial - w.Points
	if successCount < 0 || successCount > workers*perWorker {
		t.Fatalf("unexpected success count: %d", successCount)
	}
}

// TestMultiCurrencyIsolation 三套币互不影响
func TestMultiCurrencyIsolation(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewWalletRepository(db)
	ctx := context.Background()

	const uid = uint(5)
	_, _ = repo.GetOrCreateWallet(ctx, uid)

	// 给积分 100
	if _, _, err := repo.ApplyChange(ctx, repository.BalanceChange{
		UID: uid, Currency: entities.CurrencyPoints, Delta: 100,
		Source: entities.SourceTaskReward,
	}); err != nil {
		t.Fatal(err)
	}
	// 给兑换券 50
	if _, _, err := repo.ApplyChange(ctx, repository.BalanceChange{
		UID: uid, Currency: entities.CurrencyCoupon, Delta: 50,
		Source: entities.SourceFarmHarvest,
	}); err != nil {
		t.Fatal(err)
	}
	// 给钻石 10
	if _, _, err := repo.ApplyChange(ctx, repository.BalanceChange{
		UID: uid, Currency: entities.CurrencyDiamond, Delta: 10,
		Source: entities.SourceDiamondRecharge,
	}); err != nil {
		t.Fatal(err)
	}

	w, _ := repo.GetWallet(ctx, uid)
	if w.Points != 100 {
		t.Fatalf("points = %d, want 100", w.Points)
	}
	if w.Coupon != 50 {
		t.Fatalf("coupon = %d, want 50", w.Coupon)
	}
	if w.Diamond != 10 {
		t.Fatalf("diamond = %d, want 10", w.Diamond)
	}

	// 各币种账本独立
	for _, tc := range []struct {
		cur  entities.Currency
		want int64
	}{
		{entities.CurrencyPoints, 100},
		{entities.CurrencyCoupon, 50},
		{entities.CurrencyDiamond, 10},
	} {
		sum, _ := repo.SumByCurrency(ctx, uid, tc.cur)
		if sum != tc.want {
			t.Fatalf("%s ledger sum = %d, want %d", tc.cur, sum, tc.want)
		}
	}
}
