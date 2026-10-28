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
func (r *GameKvSnapshotRepository) GetLatestBefore(ctx context.Context, uid uint, gameID, key string, before int64) (*entities.GameKvSnapshot, error) {
	var s entities.GameKvSnapshot
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND game_id = ? AND kv_key = ? AND created_at <= ?", uid, gameID, key, before).
		Order("id DESC").First(&s).Error
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
	res := tx.Delete(&entities.GameKvSnapshot{})
	return res.RowsAffected, res.Error
}

// DeleteGame 删除某游戏的全部快照（卸载游戏）
func (r *GameKvSnapshotRepository) DeleteGame(ctx context.Context, uid uint, gameID string) (int64, error) {
	res := r.DB.WithContext(ctx).
		Where("uid = ? AND game_id = ?", uid, gameID).
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
