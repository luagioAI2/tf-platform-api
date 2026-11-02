package service

import (
	"context"
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
	restored, err := svc.RestoreByTime(ctx, uid, "cloudfarm", "save", cutoff, 0, "按时间回档")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Value != `{"v":"A"}` {
		t.Fatalf("expected A, got %s", restored.Value)
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
