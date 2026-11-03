package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"
)

// newStorageTestService 构造测试用存储服务。
//
// 游戏用 `DefaultOwned: true` 播种 —— 这样 `checkGameExists` 不会去查
// `user_game`（少一张表的 fixture，也不容易漏）。
func newStorageTestService(t *testing.T) *GameStorageService {
	t.Helper()
	db := newTestDB(t)
	gameRepo := repository.NewGameRepository(db)
	if err := gameRepo.Create(context.Background(), &entities.Game{
		GameID: "cloudfarm", Name: "云上农场",
		Status: entities.GameStatusAvailable, DefaultOwned: true,
	}); err != nil {
		t.Fatalf("seed game: %v", err)
	}
	return &GameStorageService{
		KvRepo:   repository.NewGameKvRepository(db),
		GameRepo: gameRepo,
	}
}

// codeOf 取业务错误码（**不是** HTTP 状态码：业务错误一律 HTTP 200）
func codeOf(t *testing.T, err error) int {
	t.Helper()
	var appErr *apperrors.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("期望 *apperrors.Error，实际 %T: %v", err, err)
	}
	return appErr.ErrCode()
}

// TestStorageValueTooLarge 存档超 1MB 要返**游戏域**的 10040601。
//
// 曾经返的是 `FileTooLarge`（10 03 文件域，那是上传用的码），而
// `GAME_STORAGE_API.md` §6 一直写着 `10040601 存档过大`、客户端也按
// 10040601 做提示 —— 三处说的不是同一件事，且都是「太大」，看不出来。
func TestStorageValueTooLarge(t *testing.T) {
	svc := newStorageTestService(t)
	ctx := context.Background()
	const uid = uint(1)

	// 超一个字节就够。**两边都必须是合法 JSON** —— 否则先被 JSON 校验挡掉，
	// 拿到的会是 `invalid-parameters`，这条用例就白跑了（踩过）。
	big := jsonStringOfSize(entities.GameKvMaxValueBytes + 1)
	_, err := svc.Set(ctx, uid, "cloudfarm", "save", big, 0)
	if err == nil {
		t.Fatal("超 1MB 竟然写进去了")
	}
	if code := codeOf(t, err); code != apperrors.GameKvTooLarge {
		t.Fatalf("错误码 = %d，期望 %d（存档过大）", code, apperrors.GameKvTooLarge)
	}

	// 边界：刚好 1MB 要能过（差一个字节就拒绝，说明判据是 `>` 而不是 `>=`）
	ok := jsonStringOfSize(entities.GameKvMaxValueBytes)
	if _, err := svc.Set(ctx, uid, "cloudfarm", "save", ok, 0); err != nil {
		t.Fatalf("刚好 1MB 被拒了: %v", err)
	}
}

// TestStorageKeyLimit key 数量到上限后要返**专用码**，不能只说「参数错误」。
//
// 原来返 `InvalidParam` —— 参数一个字都没错，是配额满了；
// 插件作者照着「参数错误」查，查不到原因。
func TestStorageKeyLimit(t *testing.T) {
	svc := newStorageTestService(t)
	ctx := context.Background()
	const uid = uint(2)
	val := json.RawMessage(`{"n":1}`)

	for i := 0; i < entities.GameKvMaxKeysPerGame; i++ {
		if _, err := svc.Set(ctx, uid, "cloudfarm", fmt.Sprintf("k%d", i), val, 0); err != nil {
			t.Fatalf("第 %d 个 key 就写不进去了: %v", i+1, err)
		}
	}

	// 第 65 个：拒绝，且是专用码
	_, err := svc.Set(ctx, uid, "cloudfarm", "one-more", val, 0)
	if err == nil {
		t.Fatal("超过 64 个 key 竟然还能写")
	}
	if code := codeOf(t, err); code != apperrors.GameKvKeyLimitExceeded {
		t.Fatalf("错误码 = %d，期望 %d（key 数量超上限）", code, apperrors.GameKvKeyLimitExceeded)
	}

	// 覆盖**已存在**的 key 不受配额影响（否则存档写满之后就再也存不了进度）
	if _, err := svc.Set(ctx, uid, "cloudfarm", "k0", json.RawMessage(`{"n":2}`), 0); err != nil {
		t.Fatalf("覆盖已有 key 被配额挡住了: %v", err)
	}
}

// TestStorageReservedKey 保留键一律 604（**没有**单独的业务码）。
//
// `GameKvKeyReserved = 10040801` 曾经声明过但零引用 —— 代码返 604、
// 文档 §3.2 也写 604，那个码纯属多余，已删。这条用例同时钉住
// 「删码没有把行为改掉」。
func TestStorageReservedKey(t *testing.T) {
	svc := newStorageTestService(t)
	ctx := context.Background()
	const uid = uint(3)

	for _, key := range []string{"__coin", "__bundle", "__assets"} {
		_, err := svc.Set(ctx, uid, "cloudfarm", key, json.RawMessage(`[]`), 0)
		if err == nil {
			t.Fatalf("保留键 %s 竟然能写", key)
		}
		if code := codeOf(t, err); code != apperrors.PermissionsForbidden {
			t.Fatalf("%s 的错误码 = %d，期望 %d（权限不足）", key, code, apperrors.PermissionsForbidden)
		}
	}
}

// jsonStringOfSize 造一个**总长恰好 n 字节**的合法 JSON 字符串值。
//
// 为什么要这么麻烦：`bytes.Repeat([]byte("a"), n)` 不是合法 JSON，
// 而 `Set` 的校验顺序是「先体积、后 JSON」—— 超限那侧看不出来，
// 但「刚好 1MB」那侧会先被 JSON 校验拒掉（错误码变成 invalid-parameters）。
func jsonStringOfSize(n int) json.RawMessage {
	b := make([]byte, n)
	b[0], b[n-1] = '"', '"'
	for i := 1; i < n-1; i++ {
		b[i] = 'a'
	}
	return json.RawMessage(b)
}

// TestStorageTotalQuota 单用户 64MB 总量上限必须真的拦。
//
// 这条以前只写在 `/game/kv/usage` 的 `max_bytes` 里，`Set` 从不检查 ——
// 界面告诉家长「最多 64MB」，实际 1MB × 每游戏 64 个 key × 21 款，
// 单个用户能存到 1GB 以上。
func TestStorageTotalQuota(t *testing.T) {
	svc := newStorageTestService(t)
	ctx := context.Background()
	const uid = uint(4)

	// 直接把用量「撑满」：只把 `size` 列写成 64MB，值本身很小。
	// 这里要测的是配额判据，不是 `size` 怎么算出来的 —— 真塞 64MB 数据
	// 只会让这条用例慢几十秒，而 `TotalSize` 的求和另有用例覆盖。
	if err := svc.KvRepo.DB.Create(&entities.GameKv{
		UID: uid, GameID: "cloudfarm", Key: "huge",
		Value: `{"n":1}`, Size: entities.GameKvMaxTotalBytes, Version: 1,
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 再写一个新 key（哪怕只有十几个字节）⇒ 超了，要拒绝
	_, err := svc.Set(ctx, uid, "cloudfarm", "save", json.RawMessage(`{"lv":1}`), 0)
	if err == nil {
		t.Fatal("总量已经满了，新 key 竟然写进去了")
	}
	if code := codeOf(t, err); code != apperrors.GameKvTooLarge {
		t.Fatalf("错误码 = %d，期望 %d（存档过大 / 账号满了）", code, apperrors.GameKvTooLarge)
	}

	// 越写越小的写不受配额影响（否则存档满了之后连「清数据」都写不进去）
	if _, err := svc.Set(ctx, uid, "cloudfarm", "huge", json.RawMessage(`{"n":2}`), 1); err != nil {
		t.Fatalf("变小的写被配额挡住了: %v", err)
	}
}
