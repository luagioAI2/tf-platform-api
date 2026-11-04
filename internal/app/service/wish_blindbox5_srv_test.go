package service

import (
	"context"
	"testing"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

/* ============================================================
   「5 连开」盲盒 —— 服务端这一单到底授了几款、取消时收回几款
   ------------------------------------------------------------
   纯函数 `PickBlindBoxMany` 在 entities 包里已经有测试了，但**中间那一段
   没人测**：`Redeem` 真的往 `user_game` 里写了 5 行吗？`Cancel` 真的把
   5 行都收回了吗？这一段坏了**两头都不报**：

     · 只授 1 款 ⇒ 界面照样说「兑换成功」，孩子付了 5 份的钱；
     · 只收回 1 款 ⇒ 界面照样说「取消成功、券退了」，另外 4 款白送。

   而且这两种错都不会让账本对不上（账本只看 delta 之和），所以只能靠
   直接查 `user_game` 才发现。
   ============================================================ */

const wishTestUID = uint(7)

// newWishTestDB 和 `newTestDB` 一样的库，但**连接池不是 1 条**。
//
// ⚠️ 这不是偷懒，是这个函数形状决定的：
// `WishService.Redeem` 在 `Transaction(...)` 里用 `s.Repo.GetItem(ctx)` /
// `GetOffer(ctx)` / `GetActiveOrderByItem(ctx)` —— 这些方法走的是**外层 DB 句柄**，
// 不是 `tx`。生产连接池是 100 条（`providers.go`），所以线上只是「事务里读了
// 另一个连接」；而 `newTestDB` 故意把池子钉成 1 条，于是这里**必然死锁**
// （90 秒超时、堆栈停在 `GetItem`）。
//
// 所以这条链路只能拿多连接的库测。想用 1 条连接的库测，得先给仓库加一圈
// `XxxWithTx` 变体 —— 那是另一件事，别为了写测试顺手改生产事务。
//
// `loadGuestTestConfig` 是**必须**的：`ProvideWishService` 会读
// `config.Get().ServiceSettings.Timezone`，没加载配置时拿到的是零值，
// `time.LoadLocation("")` 报错后回落 UTC —— 这次恰好没事，
// 但换个用例就会在「今天」的日界上算错，而那种错只会表现为数字差一天。
func newWishTestFixture(t *testing.T) (*WishService, *WalletService, *gorm.DB) {
	t.Helper()
	loadGuestTestConfig(t)

	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)
	if err := repository.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	walletSrv := ProvideWalletService(repository.NewWalletRepository(db))
	svc := ProvideWishService(
		repository.NewWishRepository(db),
		repository.NewGameRepository(db),
		walletSrv,
		ProvideEventService(),
		repository.NewRedeemRepository(db),
		db,
	)
	return svc, walletSrv, db
}

// seedBlindBox 造一件盲盒商品 + 本家庭的开放价，并给账号发够券。
func seedBlindBox(t *testing.T, svc *WishService, walletSrv *WalletService, pool []string, unlock int, price, coupon int64) string {
	t.Helper()
	ctx := context.Background()

	item := &entities.WishItem{
		ItemID:      "wish-blindbox-test",
		Kind:        entities.WishKindBlindbox,
		Name:        "测试盲盒",
		Emoji:       "🎁",
		Pool:        entities.JSONStringArray(pool),
		UnlockCount: unlock,
	}
	if err := svc.Repo.UpsertItem(ctx, item); err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if err := svc.Repo.UpsertOffer(ctx, &entities.WishOffer{
		UID:          wishTestUID,
		ItemID:       item.ItemID,
		VoucherPrice: price,
		OpenedAt:     1,
	}); err != nil {
		t.Fatalf("upsert offer: %v", err)
	}
	if _, _, err := walletSrv.Change(ctx, repository.BalanceChange{
		UID:      wishTestUID,
		Currency: entities.CurrencyCoupon,
		Delta:    coupon,
		Source:   entities.SourceFarmMarket,
		Memo:     "测试发券",
	}); err != nil {
		t.Fatalf("发券: %v", err)
	}
	return item.ItemID
}

// ownGame 直接把某款游戏标成「已拥有」（模拟之前换到过）。
func ownGame(t *testing.T, db *gorm.DB, gameID string) {
	t.Helper()
	if err := db.Create(&entities.UserGame{
		UID:      wishTestUID,
		GameID:   gameID,
		Source:   entities.GameOwnedByDefault,
		Currency: entities.CurrencyCoupon,
	}).Error; err != nil {
		t.Fatalf("造拥有关系 %s: %v", gameID, err)
	}
}

// ownedGames 当前**真的**拥有的游戏（走仓库，过滤掉软删的行）。
func ownedGames(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	ids, err := repository.NewGameRepository(db).ListOwnedGameIDs(context.Background(), wishTestUID)
	if err != nil {
		t.Fatalf("查拥有关系: %v", err)
	}
	return ids
}

func couponOf(t *testing.T, walletSrv *WalletService) int64 {
	t.Helper()
	w, err := walletSrv.GetWallet(context.Background(), wishTestUID)
	if err != nil {
		t.Fatalf("查钱包: %v", err)
	}
	return w.Coupon
}

func mustRedeem(t *testing.T, svc *WishService, itemID string) *entities.WishOrderView {
	t.Helper()
	view, err := svc.Redeem(context.Background(), wishTestUID, &entities.WishRedeemReq{ItemID: itemID})
	if err != nil {
		t.Fatalf("兑换失败: %v", err)
	}
	return view
}

// TestWishRedeemBlindBox5GrantsFive 一单真的授 5 款、清单真的落库、券只扣一次。
func TestWishRedeemBlindBox5GrantsFive(t *testing.T) {
	svc, walletSrv, db := newWishTestFixture(t)
	pool := []string{"g1", "g2", "g3", "g4", "g5", "g6", "g7"}
	const price = int64(20)
	itemID := seedBlindBox(t, svc, walletSrv, pool, 5, price, 100)

	view := mustRedeem(t, svc, itemID)

	// ① 库里 5 行，互不重复，全在池子里
	got := ownedGames(t, db)
	if len(got) != 5 {
		t.Fatalf("一单应授 5 款，实际 %d 款：%v", len(got), got)
	}
	seen := map[string]bool{}
	for _, gid := range got {
		if seen[gid] {
			t.Fatalf("同一款授了两次：%s（%v）", gid, got)
		}
		seen[gid] = true
		inPool := false
		for _, p := range pool {
			if p == gid {
				inPool = true
				break
			}
		}
		if !inPool {
			t.Fatalf("开出了池子外的 %q（%v）", gid, got)
		}
	}

	// ② 订单上两个字段都要有：清单是完整 5 个，单数字段是第一个
	if len(view.OpenedGameIDs) != 5 {
		t.Fatalf("opened_game_ids 应有 5 个，实际 %d（%v）", len(view.OpenedGameIDs), view.OpenedGameIDs)
	}
	if view.OpenedGameID == "" || view.OpenedGameID != view.OpenedGameIDs[0] {
		t.Fatalf("opened_game_id 应等于清单第一个：%q vs %v", view.OpenedGameID, view.OpenedGameIDs)
	}
	if view.Status != entities.WishStatusCommitted {
		t.Fatalf("数字商品兑换后应直接 committed，实际 %s", view.Status)
	}

	// ③ 每款的 PricePaid 加起来必须等于实付 —— 每款都写全额的话
	//    「这个游戏花了多少」会翻 5 倍。
	var sum int64
	var rows []entities.UserGame
	if err := db.Where("uid = ?", wishTestUID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		sum += r.PricePaid
	}
	if sum != price {
		t.Fatalf("5 款的 price_paid 之和应等于 %d，实际 %d", price, sum)
	}

	// ④ 券只扣一次
	if bal := couponOf(t, walletSrv); bal != 100-price {
		t.Fatalf("券应剩 %d，实际 %d", 100-price, bal)
	}
}

// TestWishRedeemBlindBox5RejectsWhenNotEnough 剩余不足 5 款 ⇒ 整单拒绝、一分不扣。
//
// 「有几款给几款」看起来更友好，但卖的是 5 款的价，给 3 款就是缺斤少两；
// 更糟的是**扣了钱才发现** —— 所以判据是「钱包一分没动 + 没有订单」。
func TestWishRedeemBlindBox5RejectsWhenNotEnough(t *testing.T) {
	svc, walletSrv, db := newWishTestFixture(t)
	pool := []string{"g1", "g2", "g3", "g4", "g5", "g6", "g7"}
	itemID := seedBlindBox(t, svc, walletSrv, pool, 5, 20, 100)

	// 已拥有 4 款 ⇒ 只剩 3 款，不够 5
	for _, gid := range []string{"g1", "g2", "g3", "g4"} {
		ownGame(t, db, gid)
	}

	_, err := svc.Redeem(context.Background(), wishTestUID, &entities.WishRedeemReq{ItemID: itemID})
	if err == nil {
		t.Fatal("剩余 3 款还开 5 连开，应该被拒")
	}
	if !apperrors.Is(err, apperrors.WishBlindBoxEmpty) {
		t.Fatalf("期望 WishBlindBoxEmpty，实际 %v", err)
	}

	if bal := couponOf(t, walletSrv); bal != 100 {
		t.Fatalf("整单拒绝后券应还是 100，实际 %d（扣了就是「扣完才发现开不了」）", bal)
	}
	if got := ownedGames(t, db); len(got) != 4 {
		t.Fatalf("被拒的单不该授任何游戏，现在拥有 %v", got)
	}
	var orders int64
	if err := db.Model(&entities.WishOrder{}).Where("uid = ?", wishTestUID).Count(&orders).Error; err != nil {
		t.Fatal(err)
	}
	if orders != 0 {
		t.Fatalf("被拒的单不该留下订单，实际 %d 条", orders)
	}
}

// TestWishCancelBlindBox5RevokesAllFive 取消要**全部**收回，并且券全退。
func TestWishCancelBlindBox5RevokesAllFive(t *testing.T) {
	svc, walletSrv, db := newWishTestFixture(t)
	pool := []string{"g1", "g2", "g3", "g4", "g5", "g6", "g7"}
	itemID := seedBlindBox(t, svc, walletSrv, pool, 5, 20, 100)

	view := mustRedeem(t, svc, itemID)
	if len(ownedGames(t, db)) != 5 {
		t.Fatal("前置：兑换后应有 5 款")
	}

	if _, err := svc.Cancel(context.Background(), wishTestUID, &entities.WishHandleReq{
		OrderID: view.OrderID, Reason: "先不换了",
	}); err != nil {
		t.Fatalf("取消失败: %v", err)
	}

	if got := ownedGames(t, db); len(got) != 0 {
		t.Fatalf("取消后应一款都不剩（只看 opened_game_id 会白送 4 款），实际 %v", got)
	}

	// ★ 物理行也要清掉：`user_game` 有 `(uid, game_id)` 唯一索引，
	//   软删的行仍然占着这个位置 ⇒ 下次再兑换到同一款时
	//   `ON CONFLICT DO NOTHING` 静默不写，孩子「换到了」但库里没有。
	var physical int64
	if err := db.Unscoped().Model(&entities.UserGame{}).Where("uid = ?", wishTestUID).Count(&physical).Error; err != nil {
		t.Fatal(err)
	}
	if physical != 0 {
		t.Fatalf("收回应删物理行，实际残留 %d 行软删记录", physical)
	}

	if bal := couponOf(t, walletSrv); bal != 100 {
		t.Fatalf("取消后券应退回 100，实际 %d", bal)
	}
}

// TestWishRedeemAgainAfterCancelGetsGamesBack 取消 → 再兑换，游戏必须真的回来。
//
// 这条是上一条的**后果**：软删的行占着唯一索引，再授予就是静默失败。
// 单独一条用例是因为「取消后余额对了、界面也说成功」会掩盖它 ——
// 只有再查一次拥有关系才看得见。
func TestWishRedeemAgainAfterCancelGetsGamesBack(t *testing.T) {
	svc, walletSrv, db := newWishTestFixture(t)
	pool := []string{"g1", "g2", "g3", "g4", "g5", "g6", "g7"}
	itemID := seedBlindBox(t, svc, walletSrv, pool, 5, 20, 100)

	first := mustRedeem(t, svc, itemID)
	if _, err := svc.Cancel(context.Background(), wishTestUID, &entities.WishHandleReq{
		OrderID: first.OrderID, Reason: "先不换了",
	}); err != nil {
		t.Fatalf("取消失败: %v", err)
	}

	mustRedeem(t, svc, itemID)

	if got := ownedGames(t, db); len(got) != 5 {
		t.Fatalf("取消后再兑换应重新拥有 5 款，实际 %d 款：%v", len(got), got)
	}
	if bal := couponOf(t, walletSrv); bal != 100-20 {
		t.Fatalf("第二次兑换后券应剩 %d，实际 %d", 100-20, bal)
	}
}
