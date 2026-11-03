package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"tf-api/internal/app/entities"
	"tf-api/internal/app/service/repository"
)

// newSnapshotTestService 构造测试用快照服务
func newSnapshotTestService(t *testing.T) (*GameSnapshotService, *repository.GameKvRepository) {
	t.Helper()
	db := newTestDB(t)
	kvRepo := repository.NewGameKvRepository(db)
	snapRepo := repository.NewGameKvSnapshotRepository(db)
	logRepo := repository.NewGameKvRestoreLogRepository(db)
	svc := &GameSnapshotService{
		SnapRepo: snapRepo,
		LogRepo:  logRepo,
		KvRepo:   kvRepo,
		DB:       db,
		timezone: time.UTC,
	}
	return svc, kvRepo
}

// TestSnapshotCreateAndList 验证快照创建与列表
func TestSnapshotCreateAndList(t *testing.T) {
	svc, _ := newSnapshotTestService(t)
	ctx := context.Background()
	const uid = uint(1)

	snap, err := svc.Create(ctx, uid, "cloudfarm", "save", `{"lv":1}`, 1, entities.SnapshotReasonManual, "手动保存")
	if err != nil {
		t.Fatal(err)
	}
	if snap.ID == 0 {
		t.Fatal("snapshot id should not be zero")
	}

	list, err := svc.List(ctx, uid, "cloudfarm", "save", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(list))
	}
	if list[0].Reason != entities.SnapshotReasonManual {
		t.Fatalf("reason = %s", list[0].Reason)
	}
}

// TestSnapshotShouldSnapshot 验证快照策略（时间/变化触发）
func TestSnapshotShouldSnapshot(t *testing.T) {
	svc, _ := newSnapshotTestService(t)
	ctx := context.Background()
	const uid = uint(2)

	// 第一次：没有历史 → 应该存
	should, _ := svc.ShouldSnapshot(ctx, uid, "cloudfarm", "save", `{"lv":1}`)
	if !should {
		t.Fatal("first snapshot should always be created")
	}

	// 存一个
	_, err := svc.Create(ctx, uid, "cloudfarm", "save", `{"lv":1}`, 1, entities.SnapshotReasonAuto, "")
	if err != nil {
		t.Fatal(err)
	}

	// 立即再问，值没变 → 不应该存
	should, _ = svc.ShouldSnapshot(ctx, uid, "cloudfarm", "save", `{"lv":1}`)
	if should {
		t.Fatal("unchanged value within interval should not snapshot")
	}

	// 值变化很大（长度变化 > 20%）→ 应该存
	bigValue := `{"lv":1,"items":[1,2,3,4,5,6,7,8,9,10],"gold":99999}`
	should, _ = svc.ShouldSnapshot(ctx, uid, "cloudfarm", "save", bigValue)
	if !should {
		t.Fatal("large change should trigger snapshot")
	}
}

// TestRestore 验证回档
func TestRestore(t *testing.T) {
	svc, kvRepo := newSnapshotTestService(t)
	ctx := context.Background()
	const uid = uint(3)

	// 1. 写入初始存档
	kv := &entities.GameKv{UID: uid, GameID: "cloudfarm", Key: "save", Value: `{"lv":1}`, Size: 9}
	if err := kvRepo.Put(ctx, kv, 0); err != nil {
		t.Fatal(err)
	}

	// 2. 存快照（记录 lv:1 的状态）
	snap, err := svc.Create(ctx, uid, "cloudfarm", "save", `{"lv":1}`, 1, entities.SnapshotReasonManual, "初始")
	if err != nil {
		t.Fatal(err)
	}

	// 3. 写入新值（模拟误操作）
	cur, _ := kvRepo.Get(ctx, uid, "cloudfarm", "save")
	newKV := &entities.GameKv{
		UID: uid, GameID: "cloudfarm", Key: "save",
		Value: `{"lv":99,"wiped":true}`, Size: 22,
	}
	if err := kvRepo.Put(ctx, newKV, cur.Version); err != nil {
		t.Fatal(err)
	}

	// 确认已改
	after, _ := kvRepo.Get(ctx, uid, "cloudfarm", "save")
	if after.Value == `{"lv":1}` {
		t.Fatal("value should have changed")
	}

	// 4. 回档
	restored, err := svc.Restore(ctx, uid, snap.ID, 0, "误操作回档")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Value != `{"lv":1}` {
		t.Fatalf("restore failed: got %s", restored.Value)
	}

	// 5. 确认当前值已恢复
	final, _ := kvRepo.Get(ctx, uid, "cloudfarm", "save")
	if final.Value != `{"lv":1}` {
		t.Fatalf("value not restored: %s", final.Value)
	}

	// 6. 确认有回档日志
	logs, err := svc.RestoreLogs(ctx, uid, "cloudfarm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 restore log, got %d", len(logs))
	}
	if logs[0].SnapshotID != snap.ID {
		t.Fatalf("log snapshot_id = %d, want %d", logs[0].SnapshotID, snap.ID)
	}

	// 7. 日志要带上「退回到哪一份」的时间，家长才看得懂
	if logs[0].TargetAt != snap.CreatedAt {
		t.Fatalf("log target_at = %d, want %d（快照创建时间）", logs[0].TargetAt, snap.CreatedAt)
	}
}

// TestRestoreLogTargetAtSurvivesPrune 验证回档日志里的时间**不依赖那份快照还在**。
//
// 这是这一列存在的唯一理由：快照会被保留策略淘汰（每个 key 只留 20 份），
// 如果 target_at 是列表时现查的，那么过几天所有历史回档记录都会退回
// 「退回了以前的版本」—— 修好的东西自己烂掉，而且**不报错**。
func TestRestoreLogTargetAtSurvivesPrune(t *testing.T) {
	svc, kvRepo := newSnapshotTestService(t)
	ctx := context.Background()
	const uid = uint(5)

	kv := &entities.GameKv{UID: uid, GameID: "cloudfarm", Key: "save", Value: `{"v":"A"}`, Size: 11}
	_ = kvRepo.Put(ctx, kv, 0)
	snap, err := svc.Create(ctx, uid, "cloudfarm", "save", `{"v":"A"}`, 1, entities.SnapshotReasonManual, "A")
	if err != nil {
		t.Fatal(err)
	}
	want := snap.CreatedAt

	if _, err := svc.Restore(ctx, uid, snap.ID, 0, "回到 A"); err != nil {
		t.Fatal(err)
	}

	// 把那份快照**物理删掉**（模拟保留策略淘汰 / 卸载清理）
	if err := svc.SnapRepo.DB.WithContext(ctx).Unscoped().
		Delete(&entities.GameKvSnapshot{}, snap.ID).Error; err != nil {
		t.Fatal(err)
	}

	logs, err := svc.RestoreLogs(ctx, uid, "cloudfarm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 restore log, got %d", len(logs))
	}
	if logs[0].TargetAt != want {
		t.Fatalf("快照已删，target_at 应该还在 = %d，实际 %d", want, logs[0].TargetAt)
	}
}

// TestRestoreIsReversible 验证「回档本身也可回档」
//
// 这是关键安全属性：如果回档到错误的时间点，还能从 before_restore 快照恢复。
func TestRestoreIsReversible(t *testing.T) {
	svc, kvRepo := newSnapshotTestService(t)
	ctx := context.Background()
	const uid = uint(4)

	// 初始值 A
	kv := &entities.GameKv{UID: uid, GameID: "cloudfarm", Key: "save", Value: `{"v":"A"}`, Size: 11}
	_ = kvRepo.Put(ctx, kv, 0)
	snapA, _ := svc.Create(ctx, uid, "cloudfarm", "save", `{"v":"A"}`, 1, entities.SnapshotReasonManual, "A")

	// 改成 B
	cur, _ := kvRepo.Get(ctx, uid, "cloudfarm", "save")
	_ = kvRepo.Put(ctx, &entities.GameKv{
		UID: uid, GameID: "cloudfarm", Key: "save", Value: `{"v":"B"}`, Size: 11,
	}, cur.Version)

	// 回档到 A（此时会先存 B 为 before_restore）
	if _, err := svc.Restore(ctx, uid, snapA.ID, 0, "回到 A"); err != nil {
		t.Fatal(err)
	}

	// 确认是 A
	v, _ := kvRepo.Get(ctx, uid, "cloudfarm", "save")
	if v.Value != `{"v":"A"}` {
		t.Fatalf("expected A, got %s", v.Value)
	}

	// 找到 before_restore 快照（记录的是 B）
	list, _ := svc.List(ctx, uid, "cloudfarm", "save", 20)
	var beforeID uint
	for _, item := range list {
		if item.Reason == entities.SnapshotReasonBeforeRestore {
			beforeID = item.ID
			break
		}
	}
	if beforeID == 0 {
		t.Fatal("before_restore snapshot not found")
	}

	// 从 before_restore 恢复 → 应该回到 B
	if _, err := svc.Restore(ctx, uid, beforeID, 0, "撤销回档"); err != nil {
		t.Fatal(err)
	}
	v2, _ := kvRepo.Get(ctx, uid, "cloudfarm", "save")
	if v2.Value != `{"v":"B"}` {
		t.Fatalf("undo restore failed: expected B, got %s", v2.Value)
	}
}

// TestSnapshotRetention 验证保留策略（不会无限增长）
func TestSnapshotRetention(t *testing.T) {
	svc, _ := newSnapshotTestService(t)
	ctx := context.Background()
	const uid = uint(5)

	// 手动创建超过保留上限的快照
	for i := 0; i < entities.SnapshotKeepPerKey+10; i++ {
		_, err := svc.Create(ctx, uid, "cloudfarm", "save",
			`{"i":`+itoaUint(uint(i))+`}`, int64(i+1), entities.SnapshotReasonAuto, "")
		if err != nil {
			t.Fatal(err)
		}
		// 等异步清理完成
		time.Sleep(5 * time.Millisecond)
	}

	// 等待最后一次清理
	time.Sleep(200 * time.Millisecond)

	count, err := svc.SnapRepo.CountByKey(ctx, uid, "cloudfarm", "save")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("snapshot count after retention: %d (limit %d)", count, entities.SnapshotKeepPerKey)

	if count > int64(entities.SnapshotKeepPerKey)+2 {
		t.Fatalf("retention failed: %d snapshots, expected <= %d", count, entities.SnapshotKeepPerKey)
	}
}

// TestSnapshotIsolation 验证快照按用户隔离
func TestSnapshotIsolation(t *testing.T) {
	svc, _ := newSnapshotTestService(t)
	ctx := context.Background()

	// 用户 1 的快照
	snap1, _ := svc.Create(ctx, 1, "cloudfarm", "save", `{"u":1}`, 1, entities.SnapshotReasonManual, "")

	// 用户 2 不应该能访问用户 1 的快照
	_, err := svc.Get(ctx, 2, snap1.ID)
	if err == nil {
		t.Fatal("user 2 should not access user 1's snapshot")
	}
}

// TestRestoreByTime 验证按时间点回档
func TestRestoreByTime(t *testing.T) {
	svc, kvRepo := newSnapshotTestService(t)
	ctx := context.Background()
	const uid = uint(6)

	// 初始 A + 快照
	kv := &entities.GameKv{UID: uid, GameID: "cloudfarm", Key: "save", Value: `{"v":"A"}`, Size: 11}
	_ = kvRepo.Put(ctx, kv, 0)
	_, _ = svc.Create(ctx, uid, "cloudfarm", "save", `{"v":"A"}`, 1, entities.SnapshotReasonManual, "")

	time.Sleep(1100 * time.Millisecond) // 确保时间戳不同
	cutoff := time.Now().Unix()
	time.Sleep(1100 * time.Millisecond)

	// 改成 B
	cur, _ := kvRepo.Get(ctx, uid, "cloudfarm", "save")
	_ = kvRepo.Put(ctx, &entities.GameKv{
		UID: uid, GameID: "cloudfarm", Key: "save", Value: `{"v":"B"}`, Size: 11,
	}, cur.Version)
	_, _ = svc.Create(ctx, uid, "cloudfarm", "save", `{"v":"B"}`, 2, entities.SnapshotReasonManual, "")

	// 按时间回档到 cutoff 之前 → 应该回到 A
	restored, err := svc.RestoreByTime(ctx, uid, "cloudfarm", "save", cutoff, 0, 0, "按时间回档")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Value != `{"v":"A"}` {
		t.Fatalf("expected A, got %s", restored.Value)
	}
}

/*
 * ============================ 同一秒里的两份快照 ============================
 *
 * `restore-by-time` 的 `at` 只到**秒**。同一秒里落了两份快照时（界面刚把列表
 * 画出来，孩子又玩了一下，服务端自己补了一份更新的），只按 `created_at <= at`
 * 挑，挑到的是**那一秒里最新的一份** —— 界面没显示、家长没点的那份。
 *
 * 于是「预览说退回到 21:30 那份，实际退回到另一个状态」，两边都不报错。
 * `max_id` 就是钉住「界面当时看到的那批」：快照 id 自增，所以
 * `id <= max_id` 正好等于「界面读列表时已经存在的行」。
 *
 * 去掉仓储里那个 `if maxID > 0 { q = q.Where("id <= ?", maxID) }`，这条会红。
 */
func TestRestoreByTimePinsMaxID(t *testing.T) {
	svc, _ := newSnapshotTestService(t)
	ctx := context.Background()
	const uid = uint(61)
	const game, key = "cloudfarm", "save"

	old, err := svc.Create(ctx, uid, game, key, `{"v":"old"}`, 1, entities.SnapshotReasonManual, "")
	if err != nil {
		t.Fatal(err)
	}
	newer, err := svc.Create(ctx, uid, game, key, `{"v":"newer"}`, 2, entities.SnapshotReasonManual, "")
	if err != nil {
		t.Fatal(err)
	}

	// 把两份压进**同一秒** —— 这就是线上真正发生的事
	at := time.Now().Add(-10 * time.Second).Unix()
	if err := svc.DB.Model(&entities.GameKvSnapshot{}).
		Where("id IN ?", []uint{old.ID, newer.ID}).
		Update("created_at", at).Error; err != nil {
		t.Fatal(err)
	}

	// 不带 max_id：挑到那一秒里最新的一份（旧行为，也就是这次的 bug）
	got, err := svc.RestoreByTime(ctx, uid, game, key, at, 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != `{"v":"newer"}` {
		t.Fatalf("不带 max_id 应该挑同一秒里最新那份，got %s", got.Value)
	}

	// 带上界面看到的最大 id ⇒ 必须退回 old
	got2, err := svc.RestoreByTime(ctx, uid, game, key, at, old.ID, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if got2.Value != `{"v":"old"}` {
		t.Fatalf("带 max_id=%d 应该退回 old，got %s", old.ID, got2.Value)
	}
}

/*
 * ============================ 保留策略真的省空间吗 ============================
 *
 * `TestSnapshotRetention` 断的是 `CountByKey` —— 而 GORM 给带
 * `gorm.DeletedAt` 的模型自动加 `deleted_at IS NULL`，所以**软删也算「删了」**。
 * 结果是：保留策略把行「删」了 20 次，表里的 `value` 一个字节都没少，
 * 而那条断言照样绿。
 *
 * 下面这两条断言的是**物理行数**（`Unscoped()`），也就是「空间有没有真的还回来」。
 * 去掉仓储里那两个 `Unscoped()` 就会红。
 */

func TestSnapshotRetentionFreesDisk(t *testing.T) {
	svc, _ := newSnapshotTestService(t)
	ctx := context.Background()
	const uid = uint(7)

	for i := 0; i < entities.SnapshotKeepPerKey+10; i++ {
		_, err := svc.Create(ctx, uid, "cloudfarm", "save",
			`{"i":`+itoaUint(uint(i))+`}`, int64(i+1), entities.SnapshotReasonAuto, "")
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // 等异步清理

	var physical int64
	if err := svc.DB.Unscoped().Model(&entities.GameKvSnapshot{}).
		Where("uid = ? AND game_id = ? AND kv_key = ?", uid, "cloudfarm", "save").
		Count(&physical).Error; err != nil {
		t.Fatal(err)
	}
	if physical > int64(entities.SnapshotKeepPerKey)+2 {
		t.Fatalf("保留策略没真的删掉：表里还剩 %d 行（软删的尸体，value 还占着空间），期望 <= %d",
			physical, entities.SnapshotKeepPerKey+2)
	}
}

// TestUninstallDropsSnapshotRows 卸载之后快照行必须**物理消失**，
// 否则「卸载会连存档一起删掉」这句话是骗人的（软删的行只是读不出来）。
func TestUninstallDropsSnapshotRows(t *testing.T) {
	svc, _ := newSnapshotTestService(t)
	ctx := context.Background()
	const uid = uint(8)

	for _, k := range []string{"save", "bag"} {
		if _, err := svc.Create(ctx, uid, "cloudfarm", k, `{"lv":1}`, 1,
			entities.SnapshotReasonManual, ""); err != nil {
			t.Fatal(err)
		}
	}

	n, err := svc.DeleteGameSnapshots(ctx, uid, "cloudfarm")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("删除条数 = %d，期望 2", n)
	}

	var physical int64
	if err := svc.DB.Unscoped().Model(&entities.GameKvSnapshot{}).
		Where("uid = ? AND game_id = ?", uid, "cloudfarm").
		Count(&physical).Error; err != nil {
		t.Fatal(err)
	}
	if physical != 0 {
		t.Fatalf("卸载后表里还有 %d 行（软删的尸体）", physical)
	}
}

// TestSnapshotUserQuota 单用户总量配额：日切基线不参与 per-key 淘汰，
// 所以必须由第二级配额兜底，否则 `game_kv_snapshot` 只增不减。
//
// 断三件事：
//  1. 超配额后总数回到上限（不多不少）；
//  2. 删的是**最旧的基线**，刚存的那份还在（别把用户刚存的东西清掉）；
//  3. 配额是按 uid 的，不串到别人。
func TestSnapshotUserQuota(t *testing.T) {
	svc, _ := newSnapshotTestService(t)
	ctx := context.Background()
	const uid = uint(7)

	// 灌满：**全是日切基线** —— `DeleteOldest` 明确不碰它们，
	// 所以只有 enforceUserQuota 能把这 2000 行收回去。
	rows := make([]*entities.GameKvSnapshot, 0, entities.SnapshotMaxPerUser)
	for i := 0; i < entities.SnapshotMaxPerUser; i++ {
		rows = append(rows, &entities.GameKvSnapshot{
			UID:             uid,
			GameID:          "cloudfarm",
			Key:             fmt.Sprintf("k%d", i%40),
			Value:           `{"lv":1}`,
			Size:            8,
			Version:         1,
			Reason:          entities.SnapshotReasonDaily,
			IsDailyBaseline: true,
		})
	}
	if err := svc.DB.CreateInBatches(rows, 200).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 再存一份 ⇒ 触发配额清理
	if _, err := svc.Create(ctx, uid, "cloudfarm", "save", `{"lv":2}`, 2,
		entities.SnapshotReasonManual, ""); err != nil {
		t.Fatalf("create: %v", err)
	}

	total, err := svc.SnapRepo.CountByUser(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if total > int64(entities.SnapshotMaxPerUser) {
		t.Fatalf("配额没生效：%d 条（上限 %d）", total, entities.SnapshotMaxPerUser)
	}
	if total != int64(entities.SnapshotMaxPerUser) {
		t.Fatalf("清理过量：%d 条（期望刚好回到 %d）", total, entities.SnapshotMaxPerUser)
	}

	// 删的是最旧的基线，不是刚建的这份
	latest, err := svc.SnapRepo.GetLatest(ctx, uid, "cloudfarm", "save")
	if err != nil || latest == nil {
		t.Fatalf("刚存的快照被清掉了: %v", err)
	}
	if latest.Reason != entities.SnapshotReasonManual {
		t.Fatalf("最新那份的 reason = %s，期望 manual", latest.Reason)
	}

	// 基线确实少了 1 条（证明删的就是它，而不是普通快照）
	var baselines int64
	if err := svc.DB.Model(&entities.GameKvSnapshot{}).
		Where("uid = ? AND is_daily_baseline = ?", uid, true).
		Count(&baselines).Error; err != nil {
		t.Fatal(err)
	}
	if baselines != int64(entities.SnapshotMaxPerUser-1) {
		t.Fatalf("基线剩 %d 条（期望 %d）", baselines, entities.SnapshotMaxPerUser-1)
	}

	// 配额按 uid：别人的一行都不该被碰
	other, err := svc.SnapRepo.CountByUser(ctx, uid+1)
	if err != nil {
		t.Fatal(err)
	}
	if other != 0 {
		t.Fatalf("uid %d 应该是空的，实际 %d 条", uid+1, other)
	}
}

// TestSnapshotQuotaAfterRestore 回档一次要写两条快照（before_restore + restore），
// 走的是事务里的 CreateWithTx、**不经过** enforceRetention ⇒ 不补一次用户配额，
// 反复回档（RestoreGame 是「每个键回一次」）就能把总量顶到配额之上。
func TestSnapshotQuotaAfterRestore(t *testing.T) {
	svc, _ := newSnapshotTestService(t)
	ctx := context.Background()
	const uid = uint(8)

	// 当前存档（回档要写回它，所以它必须存在且 version 对得上）
	if err := svc.DB.Create(&entities.GameKv{
		UID: uid, GameID: "cloudfarm", Key: "save",
		Value: `{"lv":9}`, Size: 9, Version: 1,
	}).Error; err != nil {
		t.Fatalf("seed kv: %v", err)
	}

	// 灌到「刚好满」：1999 条基线 + 1 条待回档的快照 = 2000
	rows := make([]*entities.GameKvSnapshot, 0, entities.SnapshotMaxPerUser-1)
	for i := 0; i < entities.SnapshotMaxPerUser-1; i++ {
		rows = append(rows, &entities.GameKvSnapshot{
			UID: uid, GameID: "cloudfarm", Key: "bag",
			Value: `{"n":0}`, Size: 7, Version: 1,
			Reason: entities.SnapshotReasonDaily, IsDailyBaseline: true,
		})
	}
	if err := svc.DB.CreateInBatches(rows, 200).Error; err != nil {
		t.Fatalf("seed snapshots: %v", err)
	}
	target := &entities.GameKvSnapshot{
		UID: uid, GameID: "cloudfarm", Key: "save",
		Value: `{"lv":1}`, Size: 9, Version: 1,
		Reason: entities.SnapshotReasonManual,
	}
	if err := svc.SnapRepo.Create(ctx, target); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	before, err := svc.SnapRepo.CountByUser(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if before != int64(entities.SnapshotMaxPerUser) {
		t.Fatalf("前置条件不成立：%d 条", before)
	}

	// 回档 ⇒ +2 条（before_restore + restore）
	if _, err := svc.Restore(ctx, uid, target.ID, 0, "test"); err != nil {
		t.Fatalf("restore: %v", err)
	}

	after, err := svc.SnapRepo.CountByUser(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if after > int64(entities.SnapshotMaxPerUser) {
		t.Fatalf("回档后超配额：%d 条（上限 %d）", after, entities.SnapshotMaxPerUser)
	}

	// 「回档本身也可回档」不能被这条清理破坏：刚写的 before_restore 还在
	var beforeRestore int64
	if err := svc.DB.Model(&entities.GameKvSnapshot{}).
		Where("uid = ? AND reason = ?", uid, entities.SnapshotReasonBeforeRestore).
		Count(&beforeRestore).Error; err != nil {
		t.Fatal(err)
	}
	if beforeRestore != 1 {
		t.Fatalf("before_restore 快照被清掉了（%d 条）", beforeRestore)
	}
}
