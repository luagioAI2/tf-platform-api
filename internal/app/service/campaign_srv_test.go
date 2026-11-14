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
   主线活动 · 服务端这一半
   ------------------------------------------------------------
   客户端「完成 5 个任务免费激活下落的2048」这件事，在线版**只能服务端做**：
   拥有关系在 `user_game`，客户端自己写本地账本换台设备就没了。

   这段代码有两种坏法，**两种都不报错**：

     · 条件不满足也授 ⇒ 白送游戏（而且没人会发现，因为它「成功」了）；
     · 记录写了、游戏没授 ⇒ 孩子这一关**永远领不到**（客户端看到「已领取」）。

   所以断言的不是「有没有报错」，而是**库里到底有没有那一行**。
   ============================================================ */

// newCampaignTestDB 多连接（服务里 `stats` 走外层句柄，池子 1 条会死锁，
// 和 `wish_blindbox5_srv_test.go` 那个坑同源）。
func newCampaignTestDB(t *testing.T) (*CampaignService, *gorm.DB) {
	t.Helper()

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

	return ProvideCampaignService(repository.NewGameRepository(db), db), db
}

const campUID = uint(11)

// setDoneTasks 造 N 个「已完成」的任务实例（只需要 uid/status）。
func setDoneTasks(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		row := &entities.TaskInstance{
			UID:       campUID,
			TaskID:    uint(1000 + i),
			PeriodKey: "2026-11",
			Date:      "2026-11-12",
			Title:     "测试任务",
			Status:    entities.StatusCompleted,
		}
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("造任务实例: %v", err)
		}
	}
}

// earnCoupons 记一笔兑换券入账（正数），source 由调用方给。
func earnCoupons(t *testing.T, db *gorm.DB, delta int64, source string) {
	t.Helper()
	if err := db.Create(&entities.LedgerEntry{
		UID:          campUID,
		Currency:     entities.CurrencyCoupon,
		Delta:        delta,
		BalanceAfter: delta,
		Source:       source,
		Memo:         "测试",
	}).Error; err != nil {
		t.Fatalf("造券流水: %v", err)
	}
}

func ownedByServer(t *testing.T, db *gorm.DB, gameID string) bool {
	t.Helper()
	var n int64
	if err := db.Model(&entities.UserGame{}).
		Where("uid = ? AND game_id = ?", campUID, gameID).
		Count(&n).Error; err != nil {
		t.Fatalf("查拥有: %v", err)
	}
	return n > 0
}

// TestCampaignClaimRejectsWhenNotReady ★ 条件不够 ⇒ 拒绝，且**一行都不写**。
func TestCampaignClaimRejectsWhenNotReady(t *testing.T) {
	svc, db := newCampaignTestDB(t)
	ctx := context.Background()

	setDoneTasks(t, db, 3) // 差 2 个

	_, err := svc.Claim(ctx, campUID, "first5")
	if err == nil {
		t.Fatalf("只做了 3 个任务却领成功了 —— 白送一款游戏")
	}
	if !apperrors.Is(err, apperrors.CampaignNotReady) {
		t.Fatalf("错的错误码：%v", err)
	}
	if ownedByServer(t, db, "fall2048") {
		t.Fatalf("被拒绝了却把游戏授了")
	}
	var n int64
	db.Model(&entities.CampaignClaim{}).Where("uid = ?", campUID).Count(&n)
	if n != 0 {
		t.Fatalf("被拒绝了却写了 %d 条领取记录", n)
	}
}

// TestCampaignClaimGrantsGame ★ 条件够了 ⇒ 记录 + 游戏都要落库。
func TestCampaignClaimGrantsGame(t *testing.T) {
	svc, db := newCampaignTestDB(t)
	ctx := context.Background()

	setDoneTasks(t, db, 5)

	resp, err := svc.Claim(ctx, campUID, "first5")
	if err != nil {
		t.Fatalf("领取失败: %v", err)
	}
	if resp.GameID != "fall2048" || resp.Already {
		t.Fatalf("响应不对: %+v", resp)
	}
	if !ownedByServer(t, db, "fall2048") {
		// 这一条是最要命的：客户端会显示「已领取」，而游戏库里没有
		t.Fatalf("记录写了但游戏没授 —— 孩子这一关永远领不到")
	}
}

// TestCampaignClaimIsIdempotent ★ 点两下不报错、不重复授。
func TestCampaignClaimIsIdempotent(t *testing.T) {
	svc, db := newCampaignTestDB(t)
	ctx := context.Background()

	setDoneTasks(t, db, 5)

	if _, err := svc.Claim(ctx, campUID, "first5"); err != nil {
		t.Fatalf("第一次领取失败: %v", err)
	}
	again, err := svc.Claim(ctx, campUID, "first5")
	if err != nil {
		t.Fatalf("第二次领取不该报错（手抖点两下）: %v", err)
	}
	if !again.Already {
		t.Fatalf("第二次应该标记 already")
	}
	var n int64
	db.Model(&entities.CampaignClaim{}).Where("uid = ? AND step_id = ?", campUID, "first5").Count(&n)
	if n != 1 {
		t.Fatalf("领取记录写了 %d 条（应该 1 条）", n)
	}
}

// TestCampaignCouponMetricIsCumulative ★ 券按「累计赚到」算：花掉不倒退，手工加的不算。
func TestCampaignCouponMetricIsCumulative(t *testing.T) {
	svc, db := newCampaignTestDB(t)
	ctx := context.Background()

	// 赚 120、花 100 ⇒ 余额 20
	earnCoupons(t, db, 120, "farm_harvest")
	if err := db.Create(&entities.LedgerEntry{
		UID:          campUID,
		Currency:     entities.CurrencyCoupon,
		Delta:        -100,
		BalanceAfter: 20,
		Source:       "harvest_redeem",
		Memo:         "换东西",
	}).Error; err != nil {
		t.Fatalf("造支出流水: %v", err)
	}

	state, err := svc.State(ctx, campUID)
	if err != nil {
		t.Fatalf("拉状态失败: %v", err)
	}
	if state.CouponsEarned != 120 {
		t.Fatalf("累计赚到应该是 120（花掉不倒退），实际 %d", state.CouponsEarned)
	}

	if _, err := svc.Claim(ctx, campUID, "coupon100"); err != nil {
		t.Fatalf("累计 120 却领不到 100 那一关: %v", err)
	}
}

// TestCampaignManualAdjustDoesNotCount ★ 家长手工发的券不算「赚到」。
func TestCampaignManualAdjustDoesNotCount(t *testing.T) {
	svc, db := newCampaignTestDB(t)
	ctx := context.Background()

	earnCoupons(t, db, 500, entities.SourceManualAdjust)

	state, err := svc.State(ctx, campUID)
	if err != nil {
		t.Fatalf("拉状态失败: %v", err)
	}
	if state.CouponsEarned != 0 {
		t.Fatalf("手工发的券被算成「赚到」了：%d —— 发一笔就能点亮全部关卡", state.CouponsEarned)
	}
}

// TestCampaignUnknownStep 没有这一关要明确拒绝。
func TestCampaignUnknownStep(t *testing.T) {
	svc, db := newCampaignTestDB(t)
	setDoneTasks(t, db, 999)

	_, err := svc.Claim(context.Background(), campUID, "no-such-step")
	if !apperrors.Is(err, apperrors.CampaignStepNotExist) {
		t.Fatalf("错的错误码：%v", err)
	}
}

/* ------------------------------------------------------------------ *
 * 随机奖励（第 5 关，2026-11-14）
 * ------------------------------------------------------------------ */

// TestCampaignFirst50GrantsUnowned ★ 第 5 关送的是**一款用户还没有的游戏**，
// 而且不能是前面几关明码送过的（否则孩子会觉得「又是这个」）。
func TestCampaignFirst50GrantsUnowned(t *testing.T) {
	svc, db := newCampaignTestDB(t)
	ctx := context.Background()

	setDoneTasks(t, db, 50)

	/* ⚠️ 测试库是空库：先塞几款候选，否则 pickRandomGift 一个都挑不出来
	   （它只认 `status=available` 的行）。这是**夹具**，不是被测逻辑。 */
	seedTestGames(t, db, "anthill", "corgi", "melonjar", "minergirl")

	// 先假装已经拥有几款，验证它不会挑这几个
	for _, gid := range []string{"anthill", "corgi"} {
		if err := svc.GameRepo.GrantGame(ctx, &entities.UserGame{
			UID: campUID, GameID: gid, Source: entities.GameOwnedByGift,
		}); err != nil {
			t.Fatalf("预置拥有失败: %v", err)
		}
	}

	resp, err := svc.Claim(ctx, campUID, "first50")
	if err != nil {
		t.Fatalf("领取失败: %v", err)
	}
	if resp.GameID == "" {
		// 这一条最要命：game_id 是空的，客户端不知道装哪款
		t.Fatalf("随机关没返回 game_id")
	}
	if resp.GameID == "anthill" || resp.GameID == "corgi" {
		t.Fatalf("挑了已经拥有的 %s", resp.GameID)
	}
	// 不能是前面几关明码送过的
	fixed := map[string]bool{"fall2048": true, "cloudfarm": true, "screamchick": true, "hoopsneak": true,
		"blockfit": true, "prizepark": true, "claw3d": true}
	if fixed[resp.GameID] {
		t.Fatalf("挑了主线明码送过的 %s —— 孩子会觉得「又是这个」", resp.GameID)
	}
	if !ownedByServer(t, db, resp.GameID) {
		t.Fatalf("返回了 %s 但没有真的授下去", resp.GameID)
	}
}

// TestCampaignFirst50NoGiftLeft ★ 没有可送的时必须**报错**，不能假装成功。
func TestCampaignFirst50NoGiftLeft(t *testing.T) {
	svc, db := newCampaignTestDB(t)
	ctx := context.Background()

	setDoneTasks(t, db, 50)

	seedTestGames(t, db, "anthill", "corgi", "melonjar")

	// 把全部 available 游戏都塞给用户（含随包那批）
	var games []entities.Game
	if err := db.Where("status = ?", entities.GameStatusAvailable).Find(&games).Error; err != nil {
		t.Fatalf("读游戏失败: %v", err)
	}
	for _, g := range games {
		if err := svc.GameRepo.GrantGame(ctx, &entities.UserGame{
			UID: campUID, GameID: g.GameID, Source: entities.GameOwnedByGift,
		}); err != nil {
			t.Fatalf("预置拥有失败: %v", err)
		}
	}

	if _, err := svc.Claim(ctx, campUID, "first50"); err == nil {
		/* 不报错的话界面会显示「领取成功」而游戏柜没有变化 ——
		   这套功能里最坏的一种失败。 */
		t.Fatalf("全都拥有了还返回成功 —— 会造成「领了但没东西」")
	}
}

// TestCampaignCoupon100GrantsCloudfarm ★ 第 2 关按用户要求送云边小农场。
func TestCampaignCoupon100GrantsCloudfarm(t *testing.T) {
	svc, db := newCampaignTestDB(t)
	ctx := context.Background()

	earnCoupons(t, db, 100, "farm_harvest")

	resp, err := svc.Claim(ctx, campUID, "coupon100")
	if err != nil {
		t.Fatalf("领取失败: %v", err)
	}
	if resp.GameID != "cloudfarm" {
		t.Fatalf("第 2 关应该送 cloudfarm，实际 %s", resp.GameID)
	}
	if !ownedByServer(t, db, "cloudfarm") {
		t.Fatalf("没授下去")
	}
}

// TestCampaignStepTableMatchesClient ★ 8 关，且 id 与客户端那份一字不差。
func TestCampaignStepTableMatchesClient(t *testing.T) {
	want := []string{"first5", "coupon100", "first15", "coupon300", "first50", "coupon600", "first100", "coupon1000"}
	got := entities.CampaignSteps()
	if len(got) != len(want) {
		t.Fatalf("关卡数 %d，期望 %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].StepID != id {
			t.Fatalf("第 %d 关 id 是 %s，期望 %s（改 id 等于作废老玩家的领取记录）", i+1, got[i].StepID, id)
		}
	}
}

/* ------------------------------------------------------------------ *
 * 测试夹具
 * ------------------------------------------------------------------ */

// seedTestGames 往空测试库里塞几款 available 游戏（无 package，只用来挑）。
func seedTestGames(t *testing.T, db *gorm.DB, ids ...string) {
	t.Helper()
	for _, id := range ids {
		row := &entities.Game{
			GameID:      id,
			Name:        id,
			Status:      entities.GameStatusAvailable,
			Sort:        0,
			EntryURL:    "",
			CoverURL:    "",
			AgeHint:     "",
			SessionHint: "",
		}
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("造游戏 %s: %v", id, err)
		}
	}
}
