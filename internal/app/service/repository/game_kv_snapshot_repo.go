package repository

import (
	"context"
	"errors"

	"tf-api/internal/app/entities"

	"github.com/google/wire"
	"gorm.io/gorm"
)

var GameKvSnapshotRepositorySet = wire.NewSet(wire.Struct(new(GameKvSnapshotRepository), "*"))

// GameKvSnapshotRepository 存档快照数据访问
type GameKvSnapshotRepository struct {
	DB *gorm.DB
}

// NewGameKvSnapshotRepository 直接构造（供脚本使用）
func NewGameKvSnapshotRepository(db *gorm.DB) *GameKvSnapshotRepository {
	return &GameKvSnapshotRepository{DB: db}
}

// Create 创建快照
func (r *GameKvSnapshotRepository) Create(ctx context.Context, s *entities.GameKvSnapshot) error {
	return r.DB.WithContext(ctx).Create(s).Error
}

// CreateWithTx 事务内创建快照
func (r *GameKvSnapshotRepository) CreateWithTx(tx *gorm.DB, s *entities.GameKvSnapshot) error {
	return tx.Create(s).Error
}

// CreatedAtByIDs 批量取快照的创建时间（id → Unix 秒）。
//
// 给回档日志用：日志里存的是 `snapshot_id`，家长要的是「哪一天那份」。
//
// ⚠️ 用 `Unscoped()`：快照会被保留策略软删，而**回档日志要留着**
// （审计用）。不 Unscoped 的话，被淘汰的那份查不到时间，
// 界面就只能退回「退回了以前的版本」这种没用的话。
func (r *GameKvSnapshotRepository) CreatedAtByIDs(ctx context.Context, uid uint, ids []uint) (map[uint]int64, error) {
	out := make(map[uint]int64, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID        uint
		CreatedAt int64
	}
	err := r.DB.WithContext(ctx).
		Unscoped().
		Model(&entities.GameKvSnapshot{}).
		Select("id, created_at").
		Where("uid = ? AND id IN ?", uid, ids).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row.CreatedAt
	}
	return out, nil
}

// Get 按 ID 查询快照
func (r *GameKvSnapshotRepository) Get(ctx context.Context, uid uint, snapshotID uint) (*entities.GameKvSnapshot, error) {
	var s entities.GameKvSnapshot
	err := r.DB.WithContext(ctx).
		Where("id = ? AND uid = ?", snapshotID, uid).
		First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetByID 不校验 uid（管理员/系统回档用）
func (r *GameKvSnapshotRepository) GetByID(ctx context.Context, snapshotID uint) (*entities.GameKvSnapshot, error) {
	var s entities.GameKvSnapshot
	err := r.DB.WithContext(ctx).Where("id = ?", snapshotID).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// List 列出某 key 的快照（按时间倒序）
func (r *GameKvSnapshotRepository) List(ctx context.Context, uid uint, gameID, key string, limit int) ([]*entities.GameKvSnapshot, error) {
	tx := r.DB.WithContext(ctx).Where("uid = ? AND game_id = ?", uid, gameID)
	if key != "" {
		tx = tx.Where("kv_key = ?", key)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	var list []*entities.GameKvSnapshot
	err := tx.Order("id DESC").Limit(limit).Find(&list).Error
	return list, err
}

// ListAll 列出某游戏全部 key 的快照（回档到某时间点用）
func (r *GameKvSnapshotRepository) ListAll(ctx context.Context, uid uint, gameID string, limit int) ([]*entities.GameKvSnapshot, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var list []*entities.GameKvSnapshot
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND game_id = ?", uid, gameID).
		Order("id DESC").Limit(limit).Find(&list).Error
	return list, err
}

// GetLatest 获取某 key 最新的快照
func (r *GameKvSnapshotRepository) GetLatest(ctx context.Context, uid uint, gameID, key string) (*entities.GameKvSnapshot, error) {
	var s entities.GameKvSnapshot
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND game_id = ? AND kv_key = ?", uid, gameID, key).
		Order("id DESC").First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetLatestBefore 获取某时间点之前最近的快照（按时间回档用）
//
// `maxID > 0` 时只在 `id <= maxID` 里挑。`before` 是**秒**，同一秒可能有多份，
// 只按时间挑会挑到「那一秒里最新的一份」；带上 maxID 才能钉住
// 「界面当时看到的那批」（详见 `entities.GameSnapshotRestoreByTimeReq.MaxID`）。
func (r *GameKvSnapshotRepository) GetLatestBefore(ctx context.Context, uid uint, gameID, key string, before int64, maxID uint) (*entities.GameKvSnapshot, error) {
	q := r.DB.WithContext(ctx).
		Where("uid = ? AND game_id = ? AND kv_key = ? AND created_at <= ?", uid, gameID, key, before)
	if maxID > 0 {
		q = q.Where("id <= ?", maxID)
	}
	var s entities.GameKvSnapshot
	err := q.Order("id DESC").First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// HasDailyBaseline 今天是否已有日切基线
func (r *GameKvSnapshotRepository) HasDailyBaseline(ctx context.Context, uid uint, gameID, key string, dayStart int64) (bool, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&entities.GameKvSnapshot{}).
		Where("uid = ? AND game_id = ? AND kv_key = ? AND is_daily_baseline = ? AND created_at >= ?",
			uid, gameID, key, true, dayStart).
		Count(&count).Error
	return count > 0, err
}

// CountByKey 统计某 key 的快照数（不含日切基线）
func (r *GameKvSnapshotRepository) CountByKey(ctx context.Context, uid uint, gameID, key string) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&entities.GameKvSnapshot{}).
		Where("uid = ? AND game_id = ? AND kv_key = ? AND is_daily_baseline = ?",
			uid, gameID, key, false).
		Count(&count).Error
	return count, err
}

// CountByUser 统计用户快照总数（配额）
func (r *GameKvSnapshotRepository) CountByUser(ctx context.Context, uid uint) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&entities.GameKvSnapshot{}).
		Where("uid = ?", uid).Count(&count).Error
	return count, err
}

// DeleteOldestByUser 删掉该用户最旧的 n 个快照（总量配额兜底，日切基线也算在内）。
//
// 为什么「最旧的」基本就是日切基线：基线是每天的第一条，天然排在最前面，
// 所以这里不需要再按 `is_daily_baseline` 排一次 —— 按 id 升序取，先走的就是它们。
//
// ⚠️ 同样必须 `Unscoped()`，理由见下面 `DeleteOldest` 那段（软删一个字节都不省）。
func (r *GameKvSnapshotRepository) DeleteOldestByUser(ctx context.Context, uid uint, n int) (int64, error) {
	if n <= 0 {
		return 0, nil
	}

	// 先取 id 再按 id 删：SQLite 默认编译不开 `DELETE ... LIMIT`
	var ids []uint
	err := r.DB.WithContext(ctx).Model(&entities.GameKvSnapshot{}).
		Where("uid = ?", uid).
		Order("id ASC").Limit(n).
		Pluck("id", &ids).Error
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}

	res := r.DB.WithContext(ctx).
		Where("uid = ? AND id IN ?", uid, ids).
		Unscoped().
		Delete(&entities.GameKvSnapshot{})
	return res.RowsAffected, res.Error
}

// DeleteOldest 删除某 key 最旧的 N 个非日切快照（保留策略）
func (r *GameKvSnapshotRepository) DeleteOldest(ctx context.Context, uid uint, gameID, key string, keep int) (int64, error) {
	if keep < 0 {
		keep = 0
	}

	// 先查出要保留的 id（最新的 keep 个）
	var keepIDs []uint
	err := r.DB.WithContext(ctx).Model(&entities.GameKvSnapshot{}).
		Where("uid = ? AND game_id = ? AND kv_key = ? AND is_daily_baseline = ?",
			uid, gameID, key, false).
		Order("id DESC").Limit(keep).
		Pluck("id", &keepIDs).Error
	if err != nil {
		return 0, err
	}

	tx := r.DB.WithContext(ctx).
		Where("uid = ? AND game_id = ? AND kv_key = ? AND is_daily_baseline = ?",
			uid, gameID, key, false)
	if len(keepIDs) > 0 {
		tx = tx.Where("id NOT IN ?", keepIDs)
	}
	/*
	 * ⚠️ `Unscoped()` 不能少。
	 *
	 * `GameKvSnapshot` 内嵌了 `BaseModel`（有 `gorm.DeletedAt`），所以裸的
	 * `Delete` 是**软删**：只写一个 `deleted_at`，`value` 那一列原封不动留在
	 * 表里。读取侧（`List` / `GetLatestBefore` / `CountByKey`）都被 GORM
	 * 自动加了 `deleted_at IS NULL`，所以**看起来一切正常** ——
	 * 「保留最近 20 个」也确实是 20 个，只是**一个字节都没省下来**。
	 *
	 * 快照的 `value` 是整份存档（KB 级），按「十分钟一个 + 日切基线」攒一年，
	 * 一个用户一款游戏就是几十 MB 的僵尸行。而 `/game/kv/usage` 只统计
	 * `game_kv` 表，所以配额面板上一点都看不出来。
	 *
	 * ⇒ 快照是**可再生**的历史数据（不是账本），保留策略的目的就是省空间，
	 *   所以这里必须硬删。同 §7.13 对 `game_kv` 的结论。
	 */
	res := tx.Unscoped().Delete(&entities.GameKvSnapshot{})
	return res.RowsAffected, res.Error
}

// DeleteGame 删除某游戏的全部快照（卸载游戏）
func (r *GameKvSnapshotRepository) DeleteGame(ctx context.Context, uid uint, gameID string) (int64, error) {
	// ⚠️ 同样必须 `Unscoped()`：卸载的语义是「连存档一起删掉，重装不捡旧的」。
	//    软删的话快照行还在，`restore-by-time` 一退就能把删掉的存档**复活**，
	//    而界面上「卸载」那句话是骗人的。（读取侧看不出区别，所以不报错。）
	res := r.DB.WithContext(ctx).
		Where("uid = ? AND game_id = ?", uid, gameID).
		Unscoped().
		Delete(&entities.GameKvSnapshot{})
	return res.RowsAffected, res.Error
}

// ============================ 回档日志 ============================

var GameKvRestoreLogRepositorySet = wire.NewSet(wire.Struct(new(GameKvRestoreLogRepository), "*"))

// GameKvRestoreLogRepository 回档日志数据访问
type GameKvRestoreLogRepository struct {
	DB *gorm.DB
}

// NewGameKvRestoreLogRepository 直接构造
func NewGameKvRestoreLogRepository(db *gorm.DB) *GameKvRestoreLogRepository {
	return &GameKvRestoreLogRepository{DB: db}
}

// Create 创建日志
func (r *GameKvRestoreLogRepository) Create(ctx context.Context, l *entities.GameKvRestoreLog) error {
	return r.DB.WithContext(ctx).Create(l).Error
}

// CreateWithTx 事务内创建日志
func (r *GameKvRestoreLogRepository) CreateWithTx(tx *gorm.DB, l *entities.GameKvRestoreLog) error {
	return tx.Create(l).Error
}

// List 查询回档日志
func (r *GameKvRestoreLogRepository) List(ctx context.Context, uid uint, gameID string, limit int) ([]*entities.GameKvRestoreLog, error) {
	tx := r.DB.WithContext(ctx).Where("uid = ?", uid)
	if gameID != "" {
		tx = tx.Where("game_id = ?", gameID)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var list []*entities.GameKvRestoreLog
	err := tx.Order("id DESC").Limit(limit).Find(&list).Error
	return list, err
}
