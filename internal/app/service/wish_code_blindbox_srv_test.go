package service

import (
	"context"
	"testing"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"tf-api/internal/app/service/repository"
)

/* ============================================================
   盲盒兑换码 —— 一张卡换一次 N 连开
   ------------------------------------------------------------
   2026-11-12 用户要求「盲盒 5 连游戏的激活码」。
   线下卖的是**一张卡**，孩子输一次就该开出 5 款 —— 不是输 5 次。

   这段代码有两种坏法，**两种都不报错**：

     · 只授 1 款（拿 `item.GameID` 当唯一答案）⇒ 界面说「兑换成功」，
       孩子只拿到 1 款，而码已经烧了，家长连补都没法补；
     · 池子只剩 3 款却照烧码 ⇒ 卡上写「5 连」，给 3 款，同样是码没了。

   所以这里断言的不是「有没有报错」，而是**库里到底多了几行**、
   **码到底烧没烧**。
   ============================================================ */

// newCodeTestFixture 和 `newWishTestFixture` 同一个库配置。
//
// ⚠️ 还是必须多连接：`RedeemCode` 在事务里用 `s.Repo.GetItem` / `s.ownedGameSet`
// 走的是外层 DB 句柄，池子钉成 1 条就死锁（见 `wish_blindbox5_srv_test.go` 的长注释）。
func newCodeTestFixture(t *testing.T) (*WishService, *gorm.DB) {
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
	return svc, db
}

// seedCodeFor 给某件商品插一张码（表里只存哈希，和后台同一个口径）。
func seedCodeFor(t *testing.T, db *gorm.DB, plain, itemID string) {
	t.Helper()
	row := &entities.WishCode{
		CodeHash: hashWishCode(plain),
		CodeTail: plain,
		ItemID:   itemID,
		Batch:    "TEST",
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("造码: %v", err)
	}
}

// codeUsed 这张码在库里是不是已被标记使用。
func codeUsed(t *testing.T, db *gorm.DB, plain string) bool {
	t.Helper()
	var row entities.WishCode
	if err := db.Where("code_hash = ?", hashWishCode(plain)).First(&row).Error; err != nil {
		t.Fatalf("查码: %v", err)
	}
	return row.Used
}

// TestRedeemCodeBlindBoxGrantsFive ★ 盲盒卡授 5 款（不是 1 款），池子里已有的不占名额。
func TestRedeemCodeBlindBoxGrantsFive(t *testing.T) {
	svc, db := newCodeTestFixture(t)
	ctx := context.Background()

	pool := []string{"g1", "g2", "g3", "g4", "g5", "g6", "g7", "g8"}
	if err := svc.Repo.UpsertItem(ctx, &entities.WishItem{
		ItemID:      "wish-blindbox-5",
		Kind:        entities.WishKindBlindbox,
		Name:        "游戏盲盒 · 5 连开",
		Emoji:       "🎉",
		Pool:        entities.JSONStringArray(pool),
		UnlockCount: 5,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	ownGame(t, db, "g1") // 已经有一款：不该被开出来，也不占名额
	seedCodeFor(t, db, "ABCDEF", "wish-blindbox-5")

	resp, err := svc.RedeemCode(ctx, wishTestUID, &entities.WishCodeRedeemReq{Code: "abcdef"})
	if err != nil {
		t.Fatalf("核销失败: %v", err)
	}

	// ① 响应里是 5 个 id，且第一款也在里面（老客户端只看 game_id）
	if len(resp.GameIDs) != 5 {
		t.Fatalf("应该授 5 款，实际 %d 款：%v", len(resp.GameIDs), resp.GameIDs)
	}
	if resp.GameID != resp.GameIDs[0] {
		t.Fatalf("game_id 必须是第一款：game_id=%s game_ids=%v", resp.GameID, resp.GameIDs)
	}

	// ② 库里真的多了 5 行，全在池子里、互不重复、不含已有的 g1
	got := ownedGames(t, db)
	if len(got) != 6 { // g1 + 新开的 5 款
		t.Fatalf("库里应该有 6 款（1 已有的 + 5 新开的），实际 %d：%v", len(got), got)
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("授重了：%s", id)
		}
		seen[id] = true
	}
	if !seen["g1"] {
		t.Fatalf("原有的 g1 被弄丢了：%v", got)
	}
	for _, id := range resp.GameIDs {
		if id == "g1" {
			t.Fatalf("开出了已经拥有的 g1")
		}
		if !seen[id] {
			t.Fatalf("响应说开了 %s，但库里没有", id)
		}
	}

	// ③ 码烧掉了
	if !codeUsed(t, db, "ABCDEF") {
		t.Fatalf("核销成功但码没被标记已用 —— 同一张卡能被用第二次")
	}
}

// TestRedeemCodeBlindBoxKeepsCodeWhenNotEnough ★ 池子不够 5 款 ⇒ 拒绝，**码不烧**。
//
// 这条是最要紧的：码一旦烧掉，家长连「等新游戏再来」都做不到。
func TestRedeemCodeBlindBoxKeepsCodeWhenNotEnough(t *testing.T) {
	svc, db := newCodeTestFixture(t)
	ctx := context.Background()

	if err := svc.Repo.UpsertItem(ctx, &entities.WishItem{
		ItemID:      "wish-blindbox-5",
		Kind:        entities.WishKindBlindbox,
		Name:        "游戏盲盒 · 5 连开",
		Emoji:       "🎉",
		Pool:        entities.JSONStringArray([]string{"g1", "g2", "g3", "g4"}),
		UnlockCount: 5,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	// 只有 3 款没拥有 ⇒ 只剩 1 款可开，凑不出 5 连
	ownGame(t, db, "g1")
	ownGame(t, db, "g2")
	ownGame(t, db, "g3")
	seedCodeFor(t, db, "ABCDEF", "wish-blindbox-5")

	_, err := svc.RedeemCode(ctx, wishTestUID, &entities.WishCodeRedeemReq{Code: "ABCDEF"})
	if err == nil {
		t.Fatalf("池子不够却核销成功了 —— 卡上写「5 连」，给了 1 款")
	}
	if !apperrors.Is(err, apperrors.WishBlindBoxEmpty) {
		t.Fatalf("错的错误码（应该报「盲盒池空了」）：%v", err)
	}
	if codeUsed(t, db, "ABCDEF") {
		t.Fatalf("被拒绝了却把码烧了 —— 家长连补都补不了")
	}
	if n := len(ownedGames(t, db)); n != 3 {
		t.Fatalf("被拒绝时不该授任何游戏，实际有 %d 款", n)
	}
}

// TestRedeemCodeBlindBoxCannotBeUsedTwice 同一张卡第二个人输进来必须被拒。
func TestRedeemCodeBlindBoxCannotBeUsedTwice(t *testing.T) {
	svc, db := newCodeTestFixture(t)
	ctx := context.Background()

	pool := []string{"g1", "g2", "g3", "g4", "g5", "g6"}
	if err := svc.Repo.UpsertItem(ctx, &entities.WishItem{
		ItemID:      "wish-blindbox-5",
		Kind:        entities.WishKindBlindbox,
		Name:        "游戏盲盒 · 5 连开",
		Emoji:       "🎉",
		Pool:        entities.JSONStringArray(pool),
		UnlockCount: 5,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	seedCodeFor(t, db, "ABCDEF", "wish-blindbox-5")

	if _, err := svc.RedeemCode(ctx, wishTestUID, &entities.WishCodeRedeemReq{Code: "ABCDEF"}); err != nil {
		t.Fatalf("第一次核销失败: %v", err)
	}
	// 另一个账号来用同一张卡
	const otherUID = uint(9)
	if _, err := svc.RedeemCode(ctx, otherUID, &entities.WishCodeRedeemReq{Code: "ABCDEF"}); err == nil {
		t.Fatalf("同一张码被用了两次")
	} else if !apperrors.Is(err, apperrors.WishCodeUsed) {
		t.Fatalf("错的错误码（应该报「码已用过」）：%v", err)
	}
}
