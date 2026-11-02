package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"tf-api/internal/app/entities"
	"tf-api/internal/app/service/repository"
)

// TestKvVersionStartsAtOne 第一笔写必须落 **1**，不能落 0。
//
// 为什么这是一条硬约束：客户端把 `expected_version = 0` 当作
// 「我手里没有令牌」的哨兵，服务端收到 0 就走**无锁 upsert**。
// 如果新行落 0，客户端缓存到的也是 0 ⇒ 下一次写又走无锁路径
// ⇒ 一个键的前两次写完全没有冲突检测，两台设备互盖一句话都不报。
//
// 实测踩过：探针里断言「第一次写 version = 1」失败，读到 0，
// 追下去才发现是 `default:0` + 无锁插入把 0 原样写进了库。
func TestKvVersionStartsAtOne(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewGameKvRepository(db)
	ctx := context.Background()
	const uid = uint(1)

	kv := &entities.GameKv{UID: uid, GameID: "cloudfarm", Key: "save", Value: `{"lv":1}`, Size: 8}
	if err := repo.Put(ctx, kv, 0); err != nil {
		t.Fatalf("put: %v", err)
	}
	if kv.Version != 1 {
		t.Fatalf("第一笔写应落 version 1，实际 %d", kv.Version)
	}

	cur, err := repo.Get(ctx, uid, "cloudfarm", "save")
	if err != nil || cur == nil {
		t.Fatalf("get: %v %v", cur, err)
	}
	if cur.Version != 1 {
		t.Fatalf("库里应是 1，实际 %d", cur.Version)
	}
}

// TestKvVersionLockPath 拿到版本号之后，乐观锁必须真的能拦住旧版本。
func TestKvVersionLockPath(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewGameKvRepository(db)
	ctx := context.Background()
	const uid = uint(2)

	first := &entities.GameKv{UID: uid, GameID: "cloudfarm", Key: "save", Value: `{"lv":1}`}
	if err := repo.Put(ctx, first, 0); err != nil {
		t.Fatalf("first put: %v", err)
	}

	second := &entities.GameKv{UID: uid, GameID: "cloudfarm", Key: "save", Value: `{"lv":2}`}
	if err := repo.Put(ctx, second, first.Version); err != nil {
		t.Fatalf("second put: %v", err)
	}
	if second.Version != 2 {
		t.Fatalf("第二笔写应是 2，实际 %d", second.Version)
	}

	// 拿着过期版本再写 ⇒ 必须冲突，而不是静默覆盖
	stale := &entities.GameKv{UID: uid, GameID: "cloudfarm", Key: "save", Value: `{"lv":3}`}
	err := repo.Put(ctx, stale, first.Version)
	if !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("过期版本应报冲突，实际 %v", err)
	}

	cur, _ := repo.Get(ctx, uid, "cloudfarm", "save")
	if cur.Value != `{"lv":2}` {
		t.Fatalf("冲突的写不许落库，实际 %s", cur.Value)
	}
}

// TestKvNoLockUpsertStillBumps 没带令牌（0）的写仍然成功，但版本要 +1。
//
// 这条是给「新设备第一次写」用的：它没有令牌，只能无锁覆盖。
// 但覆盖之后版本必须往前走，否则冲突检测永远追不上。
func TestKvNoLockUpsertStillBumps(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewGameKvRepository(db)
	ctx := context.Background()
	const uid = uint(3)

	for i, want := range []int64{1, 2, 3} {
		kv := &entities.GameKv{UID: uid, GameID: "cloudfarm", Key: "save", Value: `{"i":` + itoaInt(i) + `}`}
		if err := repo.Put(ctx, kv, 0); err != nil {
			t.Fatalf("put #%d: %v", i, err)
		}
		if kv.Version != want {
			t.Fatalf("第 %d 笔无锁写应是 %d，实际 %d", i+1, want, kv.Version)
		}
	}
}

// TestKvReviveAfterUninstall 卸载（硬删）后重装，再存一次必须**读得回来**。
//
// 这是那个「写成功但谁也读不出来」的回归用例：
// 卸载走软删时，重装后的第一笔 `storage.set` 会 upsert 到那条软删的行上，
// 接口返回 200 和版本号，而 `get` / `list` 永远是空 —— 客户端毫不知情。
func TestKvReviveAfterUninstall(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewGameKvRepository(db)
	ctx := context.Background()
	const uid = uint(4)

	first := &entities.GameKv{UID: uid, GameID: "cloudfarm", Key: "save", Value: `{"lv":1}`}
	if err := repo.Put(ctx, first, 0); err != nil {
		t.Fatalf("put: %v", err)
	}
	n, err := repo.DeleteGame(ctx, uid, "cloudfarm")
	if err != nil {
		t.Fatalf("delete game: %v", err)
	}
	if n != 1 {
		t.Fatalf("应删掉 1 行，实际 %d", n)
	}

	// 重装后第一次写：客户端没有令牌 ⇒ expected_version = 0
	again := &entities.GameKv{UID: uid, GameID: "cloudfarm", Key: "save", Value: `{"lv":2}`}
	if err := repo.Put(ctx, again, 0); err != nil {
		t.Fatalf("reinstall put: %v", err)
	}

	got, err := repo.Get(ctx, uid, "cloudfarm", "save")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("重装后写进去的存档读不到（软删的尸体把 upsert 吞了）")
	}
	if got.Value != `{"lv":2}` {
		t.Fatalf("值应是新的，实际 %s", got.Value)
	}

	list, err := repo.List(ctx, uid, "cloudfarm")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list 应是 1 个键，实际 %d", len(list))
	}
}

// TestKvUpsertRevivesSoftDeletedRow upsert 必须把**软删过的行**救回来。
//
// DeleteGame 现在硬删了，但历史数据 / 后台标记仍然可能留下软删的行。
// 只要 upsert 不清 `deleted_at`，那一行就永远写不进去 —— 而且不报错。
func TestKvUpsertRevivesSoftDeletedRow(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewGameKvRepository(db)
	ctx := context.Background()
	const uid = uint(5)

	if err := repo.Put(ctx, &entities.GameKv{UID: uid, GameID: "cloudfarm", Key: "save", Value: `{"lv":1}`}, 0); err != nil {
		t.Fatalf("put: %v", err)
	}
	// 绕过仓储直接打软删标记，模拟「历史数据里已经躺着一条软删的行」
	if err := db.Exec("UPDATE game_kv SET deleted_at = ? WHERE uid = ?", time.Now().Unix(), uid).Error; err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if cur, _ := repo.Get(ctx, uid, "cloudfarm", "save"); cur != nil {
		t.Fatal("软删之后不该还能读到")
	}

	if err := repo.Put(ctx, &entities.GameKv{UID: uid, GameID: "cloudfarm", Key: "save", Value: `{"lv":2}`}, 0); err != nil {
		t.Fatalf("put after soft delete: %v", err)
	}
	cur, err := repo.Get(ctx, uid, "cloudfarm", "save")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cur == nil {
		t.Fatal("upsert 没有把 deleted_at 清掉，这一行永久读不到")
	}
	if cur.Value != `{"lv":2}` {
		t.Fatalf("值应是新的，实际 %s", cur.Value)
	}
}

// itoaInt 只为拼 JSON，避免引入 strconv 让测试更吵
func itoaInt(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}
