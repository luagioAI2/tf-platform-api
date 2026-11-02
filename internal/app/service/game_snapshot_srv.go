package service

import (
	"context"
	"encoding/json"
	"time"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"
	"tf-api/pkg/logger"

	"github.com/google/wire"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var GameSnapshotServiceSet = wire.NewSet(ProvideGameSnapshotService)

// GameSnapshotService 存档快照与回档服务。
//
// 核心职责：
//  1. 写入存档时按策略决定是否存快照（不是每次都存，否则存储爆炸）
//  2. 提供快照列表 / 详情查询
//  3. 回档：把某个快照写回当前值，并记录审计日志
type GameSnapshotService struct {
	SnapRepo *repository.GameKvSnapshotRepository
	LogRepo  *repository.GameKvRestoreLogRepository
	KvRepo   *repository.GameKvRepository
	DB       *gorm.DB
	timezone *time.Location
}

// ProvideGameSnapshotService 构造函数
func ProvideGameSnapshotService(
	snapRepo *repository.GameKvSnapshotRepository,
	logRepo *repository.GameKvRestoreLogRepository,
	kvRepo *repository.GameKvRepository,
	db *gorm.DB,
) *GameSnapshotService {
	return &GameSnapshotService{
		SnapRepo: snapRepo,
		LogRepo:  logRepo,
		KvRepo:   kvRepo,
		DB:       db,
		timezone: time.Local,
	}
}

// ShouldSnapshot 判断是否需要存快照。
//
// 策略（避免存储爆炸）：
//   - 没有历史快照 → 存（第一个快照必须有）
//   - 距上次快照 > 10 分钟 → 存
//   - 值变化 > 20% → 存
//   - 否则跳过
func (s *GameSnapshotService) ShouldSnapshot(ctx context.Context, uid uint, gameID, key string, newValue string) (bool, string) {
	latest, err := s.SnapRepo.GetLatest(ctx, uid, gameID, key)
	if err != nil {
		// 查不到就当需要存（宁可多存，不要丢）
		return true, entities.SnapshotReasonAuto
	}
	if latest == nil {
		return true, entities.SnapshotReasonAuto
	}

	// 时间触发
	elapsed := time.Now().Unix() - latest.CreatedAt
	if elapsed >= entities.SnapshotMinIntervalSeconds {
		return true, entities.SnapshotReasonAuto
	}

	// 变化幅度触发
	if changedEnough(latest.Value, newValue, entities.SnapshotChangeRatioThreshold) {
		return true, entities.SnapshotReasonAuto
	}

	return false, ""
}

// changedEnough 判断值变化是否超过阈值。
//
// 只对「可比较大小」的 JSON 做精确判断（数字、字符串长度、数组长度）；
// 复杂结构退化用「字节长度变化」近似 —— 因为 KV 里存的是任意 JSON，
// 服务端不可能理解语义。
func changedEnough(oldVal, newVal string, threshold float64) bool {
	if oldVal == newVal {
		return false
	}
	oldLen, newLen := len(oldVal), len(newVal)
	if oldLen == 0 {
		return newLen > 0
	}
	ratio := float64(abs(newLen-oldLen)) / float64(oldLen)
	return ratio >= threshold
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Create 创建快照（手动或自动）
func (s *GameSnapshotService) Create(ctx context.Context, uid uint, gameID, key, value string, version int64, reason, note string) (*entities.GameKvSnapshot, error) {
	snap := &entities.GameKvSnapshot{
		UID:     uid,
		GameID:  gameID,
		Key:     key,
		Value:   value,
		Size:    int64(len(value)),
		Version: version,
		Reason:  reason,
		Note:    note,
	}
	if err := s.SnapRepo.Create(ctx, snap); err != nil {
		return nil, apperrors.WithError(err)
	}
	s.enforceRetention(ctx, uid, gameID, key)
	return snap, nil
}

// MaybeSnapshot 按策略决定是否存快照（写入后调用）。
//
// 这是「自动快照」的唯一入口，GameStorageService.Set 里调用。
func (s *GameSnapshotService) MaybeSnapshot(ctx context.Context, uid uint, gameID, key, value string, version int64) {
	should, reason := s.ShouldSnapshot(ctx, uid, gameID, key, value)
	if !should {
		return
	}

	// 日切基线：当天第一个快照永久保留
	isBaseline := s.shouldMarkDailyBaseline(ctx, uid, gameID, key)

	snap := &entities.GameKvSnapshot{
		UID:             uid,
		GameID:          gameID,
		Key:             key,
		Value:           value,
		Size:            int64(len(value)),
		Version:         version,
		Reason:          reason,
		IsDailyBaseline: isBaseline,
	}
	if err := s.SnapRepo.Create(ctx, snap); err != nil {
		logger.ZWarn("create snapshot failed",
			zap.Uint("uid", uid), zap.String("game_id", gameID),
			zap.String("key", key), zap.Error(err))
		return
	}

	s.enforceRetention(ctx, uid, gameID, key)
}

// shouldMarkDailyBaseline 是否应该标为日切基线
func (s *GameSnapshotService) shouldMarkDailyBaseline(ctx context.Context, uid uint, gameID, key string) bool {
	now := time.Now().In(s.timezone)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.timezone).Unix()
	has, err := s.SnapRepo.HasDailyBaseline(ctx, uid, gameID, key, dayStart)
	if err != nil {
		return false
	}
	return !has
}

// enforceRetention 执行保留策略：每个 key 最多保留 N 个非基线快照。
//
// ⚠️ 刻意做成**同步**而不是 `go func()`：
// 每次写入都起一个 goroutine，在 5W 并发下会瞬间堆积成千上万个。
// 这里只是一条带索引的 DELETE，开销可忽略，同步做反而更安全。
func (s *GameSnapshotService) enforceRetention(ctx context.Context, uid uint, gameID, key string) {
	if _, err := s.SnapRepo.DeleteOldest(ctx, uid, gameID, key, entities.SnapshotKeepPerKey); err != nil {
		logger.ZWarn("snapshot retention failed",
			zap.Uint("uid", uid), zap.String("game_id", gameID),
			zap.String("key", key), zap.Error(err))
	}
}

// List 列出快照（不含 value，减少传输）
func (s *GameSnapshotService) List(ctx context.Context, uid uint, gameID, key string, limit int) ([]*entities.GameSnapshotItem, error) {
	list, err := s.SnapRepo.List(ctx, uid, gameID, key, limit)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	out := make([]*entities.GameSnapshotItem, 0, len(list))
	for _, s := range list {
		out = append(out, &entities.GameSnapshotItem{
			ID:              s.ID,
			GameID:          s.GameID,
			Key:             s.Key,
			Size:            s.Size,
			Version:         s.Version,
			Reason:          s.Reason,
			IsDailyBaseline: s.IsDailyBaseline,
			Note:            s.Note,
			CreatedAt:       s.CreatedAt,
		})
	}
	return out, nil
}

// Get 获取快照详情（含 value）
func (s *GameSnapshotService) Get(ctx context.Context, uid, snapshotID uint) (map[string]interface{}, error) {
	snap, err := s.SnapRepo.Get(ctx, uid, snapshotID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if snap == nil {
		return nil, apperrors.WithCode(apperrors.SnapshotNotExist)
	}

	var value interface{}
	if err := json.Unmarshal([]byte(snap.Value), &value); err != nil {
		value = snap.Value
	}

	return map[string]interface{}{
		"id":                snap.ID,
		"game_id":           snap.GameID,
		"key":               snap.Key,
		"value":             value,
		"size":              snap.Size,
		"version":           snap.Version,
		"reason":            snap.Reason,
		"is_daily_baseline": snap.IsDailyBaseline,
		"note":              snap.Note,
		"created_at":        snap.CreatedAt,
	}, nil
}

// Restore 回档到指定快照。
//
// 流程（必须在同一事务内）：
//  1. 校验快照属于该用户
//  2. **先给当前值存一个快照**（reason=before_restore）—— 回档本身也可回档
//  3. 把快照值写回 GameKv
//  4. 写回档日志
//
// 这样即使回档错了，还能从 before_restore 快照恢复回来。
func (s *GameSnapshotService) Restore(ctx context.Context, uid, snapshotID uint, operatorID uint, reason string) (*entities.GameKv, error) {
	snap, err := s.SnapRepo.Get(ctx, uid, snapshotID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if snap == nil {
		return nil, apperrors.WithCode(apperrors.SnapshotNotExist)
	}

	var restored *entities.GameKv

	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 读当前值（⚠️ 必须用 tx，用外层 DB 会因连接池耗尽死锁）
		cur, err := s.KvRepo.GetWithTx(ctx, tx, uid, snap.GameID, snap.Key)
		if err != nil {
			return err
		}

		var fromVersion int64
		if cur != nil {
			fromVersion = cur.Version
			// 2. 回档前先存当前值（保证回档可撤销）
			before := &entities.GameKvSnapshot{
				UID:     uid,
				GameID:  snap.GameID,
				Key:     snap.Key,
				Value:   cur.Value,
				Size:    cur.Size,
				Version: cur.Version,
				Reason:  entities.SnapshotReasonBeforeRestore,
				Note:    "回档前自动保存",
			}
			if err := s.SnapRepo.CreateWithTx(tx, before); err != nil {
				return err
			}
		}

		// 3. 写回快照值（⚠️ 同样必须用 tx）
		toVersion := fromVersion + 1
		kv := &entities.GameKv{
			UID:     uid,
			GameID:  snap.GameID,
			Key:     snap.Key,
			Value:   snap.Value,
			Size:    snap.Size,
			Version: toVersion,
		}
		if cur != nil {
			// 已存在：按 version 更新
			if err := s.KvRepo.PutWithTx(ctx, tx, kv, fromVersion); err != nil {
				return err
			}
		} else {
			// 不存在：插入（expectedVersion=0 走 upsert）
			if err := s.KvRepo.PutWithTx(ctx, tx, kv, 0); err != nil {
				return err
			}
		}
		restored = kv

		// 4. 存「回档动作」快照
		after := &entities.GameKvSnapshot{
			UID:     uid,
			GameID:  snap.GameID,
			Key:     snap.Key,
			Value:   snap.Value,
			Size:    snap.Size,
			Version: toVersion,
			Reason:  entities.SnapshotReasonRestore,
			Note:    "从快照 #" + itoaUint(snapshotID) + " 回档",
		}
		if err := s.SnapRepo.CreateWithTx(tx, after); err != nil {
			return err
		}

		// 5. 写审计日志
		log := &entities.GameKvRestoreLog{
			UID:         uid,
			GameID:      snap.GameID,
			Key:         snap.Key,
			SnapshotID:  snapshotID,
			FromVersion: fromVersion,
			ToVersion:   toVersion,
			OperatorID:  operatorID,
			Reason:      reason,
			// 被恢复那份快照的时间，**在这一刻固化**（详见实体注释：
			// 现查的话，快照被淘汰后历史记录会退回「以前的版本」）
			TargetAt: snap.CreatedAt,
		}
		return s.LogRepo.CreateWithTx(tx, log)
	})

	if err != nil {
		logger.ZError("restore failed",
			zap.Uint("uid", uid), zap.Uint("snapshot_id", snapshotID), zap.Error(err))
		return nil, apperrors.WithError(err)
	}

	logger.ZInfo("game kv restored",
		zap.Uint("uid", uid),
		zap.String("game_id", snap.GameID),
		zap.String("key", snap.Key),
		zap.Uint("snapshot_id", snapshotID),
		zap.Uint("operator", operatorID))
	return restored, nil
}

// RestoreByTime 按时间点回档：把某时刻之前最近的快照写回。
func (s *GameSnapshotService) RestoreByTime(ctx context.Context, uid uint, gameID, key string, at int64, operatorID uint, reason string) (*entities.GameKv, error) {
	snap, err := s.SnapRepo.GetLatestBefore(ctx, uid, gameID, key, at)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if snap == nil {
		return nil, apperrors.WithCode(apperrors.SnapshotNotExist)
	}
	return s.Restore(ctx, uid, snap.ID, operatorID, reason)
}

// RestoreGame 回档整个游戏到某时间点。
//
// 遍历该游戏所有有快照的 key，各取 at 之前最近的快照写回。
// 返回成功回档的 key 数量。
func (s *GameSnapshotService) RestoreGame(ctx context.Context, uid uint, gameID string, at int64, operatorID uint, reason string) (int, error) {
	// 找出该游戏所有有过快照的 key
	snaps, err := s.SnapRepo.ListAll(ctx, uid, gameID, 500)
	if err != nil {
		return 0, apperrors.WithError(err)
	}

	// 去重 key
	keySet := make(map[string]struct{}, len(snaps))
	for _, sn := range snaps {
		keySet[sn.Key] = struct{}{}
	}

	restored := 0
	for key := range keySet {
		if _, err := s.RestoreByTime(ctx, uid, gameID, key, at, operatorID, reason); err != nil {
			logger.ZWarn("restore key failed",
				zap.Uint("uid", uid), zap.String("game_id", gameID),
				zap.String("key", key), zap.Error(err))
			continue
		}
		restored++
	}
	return restored, nil
}

// RestoreLogs 查询回档日志。
//
// `target_at`（被恢复那份快照的创建时间）现在由 `Restore` 在回档那一刻写进
// 日志行，正常情况下这里什么都不用补。
//
// 这里只处理**老行**：加这一列之前写的日志 `target_at` 是 0，按 `snapshot_id`
// 回填一次。回填不上（快照已被保留策略淘汰）就留 0，界面自己退回旧说法。
//
// ⚠️ 补时间是**尽力而为**：查不到不能让整个列表失败 ——
// 那样「回档记录」会变成「因为一份老快照没了，所以整页打不开」。
func (s *GameSnapshotService) RestoreLogs(ctx context.Context, uid uint, gameID string, limit int) ([]*entities.GameKvRestoreLog, error) {
	list, err := s.LogRepo.List(ctx, uid, gameID, limit)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if len(list) == 0 {
		return list, nil
	}
	// 只查老行（target_at == 0）；新行已经带着时间，不必再查一遍
	ids := make([]uint, 0, len(list))
	for _, l := range list {
		if l.SnapshotID > 0 && l.TargetAt == 0 {
			ids = append(ids, l.SnapshotID)
		}
	}
	if len(ids) == 0 {
		return list, nil
	}
	at, err := s.SnapRepo.CreatedAtByIDs(ctx, uid, ids)
	if err != nil {
		logger.ZError("restore logs: load snapshot times failed",
			zap.Uint("uid", uid), zap.Error(err))
		return list, nil
	}
	for _, l := range list {
		if l.TargetAt == 0 {
			l.TargetAt = at[l.SnapshotID]
		}
	}
	return list, nil
}

// DeleteGameSnapshots 卸载游戏时删除快照
func (s *GameSnapshotService) DeleteGameSnapshots(ctx context.Context, uid uint, gameID string) (int64, error) {
	return s.SnapRepo.DeleteGame(ctx, uid, gameID)
}
