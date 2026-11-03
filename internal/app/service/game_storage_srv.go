package service

import (
	"context"
	"encoding/json"
	"strings"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"
	"tf-api/pkg/logger"

	"github.com/google/wire"
	"go.uber.org/zap"
)

var GameStorageServiceSet = wire.NewSet(ProvideGameStorageService)

// GameStorageService 游戏通用存储服务。
//
// 对齐 kid-quest-farm 的 `host.storage` 接口：
//
//	get(key)  → 读单个键
//	set(key, value) → 整块写入
//	list()    → 列出全部键值（过滤保留键）
//
// 额外提供 delete（前端没有，但服务端需要支持「卸载游戏」）。
//
// ## 快照
//
// 每次写入都会按策略决定是否存快照（见 GameSnapshotService）。
// 这样即使存档被写坏或误操作，也能回档到之前的某个时间点。
type GameStorageService struct {
	KvRepo   *repository.GameKvRepository
	GameRepo *repository.GameRepository
	SnapSrv  *GameSnapshotService
}

// ProvideGameStorageService 构造函数
func ProvideGameStorageService(
	kvRepo *repository.GameKvRepository,
	gameRepo *repository.GameRepository,
	snapSrv *GameSnapshotService,
) *GameStorageService {
	return &GameStorageService{KvRepo: kvRepo, GameRepo: gameRepo, SnapSrv: snapSrv}
}

// validateKey 校验 key 合法性
//
// 规则（与前端一致）：
//   - 不能为空、不超过 128 字符
//   - 不能以 `__` 开头（宿主保留）
//   - 不能包含路径分隔符等危险字符
func validateKey(key string) error {
	if key == "" {
		return apperrors.WithCode(apperrors.InvalidParam)
	}
	if len(key) > entities.GameKvMaxKeyLen {
		return apperrors.WithCode(apperrors.InvalidParam)
	}
	if strings.HasPrefix(key, entities.GameKvReservedPrefix) {
		// 保留键：客户端不能读写
		return apperrors.WithCode(apperrors.PermissionsForbidden)
	}
	if strings.ContainsAny(key, "/\\ \t\n\r") {
		return apperrors.WithCode(apperrors.InvalidParam)
	}
	return nil
}

// Get 读取单个键。
//
// 返回值包含 `version` —— 客户端下次乐观锁写入必须回传它，
// 否则多设备冲突检测形同虚设。
func (s *GameStorageService) Get(ctx context.Context, uid uint, gameID, key string) (*entities.GameKvReadResp, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if err := s.checkGameExists(ctx, uid, gameID); err != nil {
		return nil, err
	}

	kv, err := s.KvRepo.Get(ctx, uid, gameID, key)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if kv == nil {
		// 未存过：value 为 null（与前端 undefined 语义对齐），version 为 0
		return &entities.GameKvReadResp{Key: key, Value: nil, Version: 0, Exists: false}, nil
	}

	var out interface{}
	if err := json.Unmarshal([]byte(kv.Value), &out); err != nil {
		// 存档损坏：返回原始字符串，不报错（避免一个坏存档卡死整个游戏）
		out = kv.Value
	}
	return &entities.GameKvReadResp{
		Key:       key,
		Value:     out,
		Version:   kv.Version,
		Exists:    true,
		Size:      kv.Size,
		UpdatedAt: kv.UpdatedAt,
	}, nil
}

// Set 整块写入
//
// expectedVersion > 0 时启用乐观锁（多设备冲突检测）。
func (s *GameStorageService) Set(ctx context.Context, uid uint, gameID, key string, value json.RawMessage, expectedVersion int64) (int64, error) {
	if err := validateKey(key); err != nil {
		return 0, err
	}
	if err := s.checkGameExists(ctx, uid, gameID); err != nil {
		return 0, err
	}

	// 体积校验
	//
	// ⚠️ 这里曾经返 `FileTooLarge`（10 03 文件域，那是**上传**用的码）。
	// 文档 §6 一直写着 `10040601 存档过大`，客户端也按 10040601 做提示 ——
	// 两边说的不是同一件事，而且没人看得出来（都是「太大」）。
	if len(value) > entities.GameKvMaxValueBytes {
		return 0, apperrors.WithCode(apperrors.GameKvTooLarge)
	}
	if len(value) == 0 {
		return 0, apperrors.WithCode(apperrors.InvalidParam)
	}
	// 必须是合法 JSON
	if !json.Valid(value) {
		return 0, apperrors.WithCode(apperrors.InvalidParam)
	}

	// 当前那一行（下面两条配额都要用它的 size）
	existing, err := s.KvRepo.Get(ctx, uid, gameID, key)
	if err != nil {
		return 0, apperrors.WithError(err)
	}

	// 配额 ①：单游戏 key 数量上限
	//
	// 只在「无锁路径 + 这是个新 key」时查：带 `expected_version` 的写必须命中
	// 已存在的行（否则是版本冲突），不可能新增 key。
	if expectedVersion <= 0 && existing == nil {
		count, err := s.KvRepo.CountKeys(ctx, uid, gameID)
		if err != nil {
			return 0, apperrors.WithError(err)
		}
		if count >= entities.GameKvMaxKeysPerGame {
			return 0, apperrors.WithCode(apperrors.GameKvKeyLimitExceeded)
		}
	}

	// 配额 ②：单用户总量上限
	var oldSize int64
	if existing != nil {
		oldSize = existing.Size
	}
	if err := s.checkTotalQuota(ctx, uid, oldSize, int64(len(value))); err != nil {
		return 0, err
	}

	kv := &entities.GameKv{
		UID:     uid,
		GameID:  gameID,
		Key:     key,
		Value:   string(value),
		Size:    int64(len(value)),
		Version: expectedVersion,
	}
	if err := s.KvRepo.Put(ctx, kv, expectedVersion); err != nil {
		if err == repository.ErrVersionConflict {
			// 用存档专属错误码，不要复用钱包的 BalanceConflict
			// （否则客户端看到「余额更新冲突」一头雾水）
			return 0, apperrors.WithCode(apperrors.GameKvVersionConflict)
		}
		return 0, apperrors.WithError(err)
	}

	// 按策略存快照（时间/变化幅度触发，不是每次都存）
	// 失败不影响写入 —— 快照是「有更好」的能力，不能因为它挂掉就写不进存档。
	if s.SnapSrv != nil {
		s.SnapSrv.MaybeSnapshot(ctx, uid, gameID, key, string(value), kv.Version)
	}

	return kv.Version, nil
}

// checkTotalQuota 单用户总大小配额（文档 §3.3 的 64MB）。
//
// 判据是「**写完之后**的总量」，所以用 `total - 旧值 + 新值` 去比。
// 代价是一次带 uid 索引的 SUM（`uk_uid_game_key` 的首列），行数被
// 「单游戏 64 个 key」硬限住（21 款 × 64 ≈ 1300 行封顶）⇒ 可以放在写路径上。
// 越写越小的写（`delta <= 0`）直接跳过，省掉这次 SUM。
//
// 超了复用 `GameKvTooLarge`：对客户端来说「这个值太大」和「账号满了」要做的
// 事完全一样（都是「存不下，先清点东西」），多一个码只是多一处要对齐。
func (s *GameStorageService) checkTotalQuota(ctx context.Context, uid uint, oldSize, newSize int64) error {
	delta := newSize - oldSize
	if delta <= 0 {
		return nil
	}
	total, err := s.KvRepo.TotalSize(ctx, uid)
	if err != nil {
		return apperrors.WithError(err)
	}
	if total+delta > entities.GameKvMaxTotalBytes {
		return apperrors.WithCode(apperrors.GameKvTooLarge)
	}
	return nil
}

// List 列出某游戏的全部键值（过滤保留键）。
//
// 返回两样东西：
//   - Data：`key → value`，**形状与前端 `storage.list()` 完全一致**，
//     直接替换即可，不破坏插件契约；
//   - Versions：`key → version`，供客户端做乐观锁写入时回传。
func (s *GameStorageService) List(ctx context.Context, uid uint, gameID string) (*entities.GameKvListResp, error) {
	if err := s.checkGameExists(ctx, uid, gameID); err != nil {
		return nil, err
	}

	rows, err := s.KvRepo.List(ctx, uid, gameID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	data := make(map[string]interface{}, len(rows))
	versions := make(map[string]int64, len(rows))
	for _, r := range rows {
		var v interface{}
		if err := json.Unmarshal([]byte(r.Value), &v); err != nil {
			data[r.Key] = r.Value // 坏存档原样返回
		} else {
			data[r.Key] = v
		}
		versions[r.Key] = r.Version
	}

	return &entities.GameKvListResp{
		GameID:   gameID,
		Data:     data,
		Versions: versions,
		Count:    len(data),
	}, nil
}

// Delete 删除单个键
func (s *GameStorageService) Delete(ctx context.Context, uid uint, gameID, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if _, err := s.KvRepo.Delete(ctx, uid, gameID, key); err != nil {
		return apperrors.WithError(err)
	}
	return nil
}

// Uninstall 卸载游戏：删除该用户该游戏的全部存档
func (s *GameStorageService) Uninstall(ctx context.Context, uid uint, gameID string) (int64, error) {
	n, err := s.KvRepo.DeleteGame(ctx, uid, gameID)
	if err != nil {
		return 0, apperrors.WithError(err)
	}
	// 同时清理快照（否则卸载后快照会一直占存储）
	if s.SnapSrv != nil {
		if _, err := s.SnapSrv.DeleteGameSnapshots(ctx, uid, gameID); err != nil {
			logger.ZWarn("delete snapshots failed",
				zap.Uint("uid", uid), zap.String("game_id", gameID), zap.Error(err))
		}
	}
	return n, nil
}

// Usage 查询存储用量（配额展示）
func (s *GameStorageService) Usage(ctx context.Context, uid uint) (map[string]interface{}, error) {
	total, err := s.KvRepo.TotalSize(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	return map[string]interface{}{
		"total_bytes": total,
		"max_bytes":   entities.GameKvMaxTotalBytes, // 单用户 64MB 总配额
	}, nil
}

// checkGameExists 校验游戏存在且用户有权访问
func (s *GameStorageService) checkGameExists(ctx context.Context, uid uint, gameID string) error {
	g, err := s.GameRepo.GetByGameID(ctx, gameID)
	if err != nil {
		return apperrors.WithError(err)
	}
	if g == nil {
		return apperrors.WithCode(apperrors.GameNotExist)
	}
	if g.Status == entities.GameStatusOffline {
		return apperrors.WithCode(apperrors.GameOffline)
	}
	// 需要已拥有（默认拥有的游戏除外）
	if !g.DefaultOwned && uid > 0 {
		has, err := s.GameRepo.HasGame(ctx, uid, gameID)
		if err != nil {
			return apperrors.WithError(err)
		}
		if !has {
			return apperrors.WithCode(apperrors.GameNotOwned)
		}
	}
	return nil
}
