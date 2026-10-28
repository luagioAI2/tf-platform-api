package service

import (
	"context"
	"encoding/json"
	"strings"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"

	"github.com/google/wire"
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
type GameStorageService struct {
	KvRepo   *repository.GameKvRepository
	GameRepo *repository.GameRepository
}

// ProvideGameStorageService 构造函数
func ProvideGameStorageService(kvRepo *repository.GameKvRepository, gameRepo *repository.GameRepository) *GameStorageService {
	return &GameStorageService{KvRepo: kvRepo, GameRepo: gameRepo}
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

// Get 读取单个键
func (s *GameStorageService) Get(ctx context.Context, uid uint, gameID, key string) (interface{}, error) {
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
		return nil, nil // 未存过，返回 null（与前端 undefined 语义对齐）
	}

	// 值是任意 JSON，解析后返回
	var out interface{}
	if err := json.Unmarshal([]byte(kv.Value), &out); err != nil {
		// 存档损坏：返回原始字符串，不报错（避免一个坏存档卡死整个游戏）
		return kv.Value, nil
	}
	return out, nil
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
	if len(value) > entities.GameKvMaxValueBytes {
		return 0, apperrors.WithCode(apperrors.FileTooLarge)
	}
	if len(value) == 0 {
		return 0, apperrors.WithCode(apperrors.InvalidParam)
	}
	// 必须是合法 JSON
	if !json.Valid(value) {
		return 0, apperrors.WithCode(apperrors.InvalidParam)
	}

	// 配额：单游戏 key 数量上限
	if expectedVersion <= 0 {
		count, err := s.KvRepo.CountKeys(ctx, uid, gameID)
		if err != nil {
			return 0, apperrors.WithError(err)
		}
		// 已存在则不算新增
		existing, _ := s.KvRepo.Get(ctx, uid, gameID, key)
		if existing == nil && count >= entities.GameKvMaxKeysPerGame {
			return 0, apperrors.WithCode(apperrors.InvalidParam)
		}
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
			return 0, apperrors.WithCode(apperrors.BalanceConflict)
		}
		return 0, apperrors.WithError(err)
	}
	return kv.Version, nil
}

// List 列出某游戏的全部键值（过滤保留键）
func (s *GameStorageService) List(ctx context.Context, uid uint, gameID string) (map[string]interface{}, error) {
	if err := s.checkGameExists(ctx, uid, gameID); err != nil {
		return nil, err
	}

	rows, err := s.KvRepo.List(ctx, uid, gameID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	out := make(map[string]interface{}, len(rows))
	for _, r := range rows {
		var v interface{}
		if err := json.Unmarshal([]byte(r.Value), &v); err != nil {
			out[r.Key] = r.Value // 坏存档原样返回
			continue
		}
		out[r.Key] = v
	}
	return out, nil
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
		"max_bytes":   int64(64) << 20, // 单用户 64MB 总配额
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
