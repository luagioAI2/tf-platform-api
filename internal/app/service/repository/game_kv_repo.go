package repository

import (
	"context"
	"errors"
	"strings"

	"tf-api/internal/app/entities"

	"github.com/google/wire"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var GameKvRepositorySet = wire.NewSet(wire.Struct(new(GameKvRepository), "*"))

// GameKvRepository 游戏存档数据访问（通用 KV）。
//
// 设计对齐 kid-quest-farm 的 `gameKv` 表语义：
//   - `(uid, game_id, key)` 唯一
//   - 整块读写，无局部更新
//   - `__` 前缀是宿主保留键，业务层校验
type GameKvRepository struct {
	DB *gorm.DB
}

// NewGameKvRepository 直接构造（供脚本使用）
func NewGameKvRepository(db *gorm.DB) *GameKvRepository {
	return &GameKvRepository{DB: db}
}

// Get 读取单个键
func (r *GameKvRepository) Get(ctx context.Context, uid uint, gameID, key string) (*entities.GameKv, error) {
	return r.GetWithTx(ctx, r.DB, uid, gameID, key)
}

// GetWithTx 在给定事务内读取。
//
// ⚠️ 事务内**必须**用这个方法，不能用 Get。
// 原因：`r.DB` 是外层句柄，在事务里用它会让 database/sql 去连接池
// 申请**第二条连接**（第一条已被事务占用）。连接池打满时直接死锁 ——
// 实测：回档逻辑里误用外层句柄，测试挂死 600 秒。
func (r *GameKvRepository) GetWithTx(ctx context.Context, tx *gorm.DB, uid uint, gameID, key string) (*entities.GameKv, error) {
	var kv entities.GameKv
	err := tx.WithContext(ctx).
		Where("uid = ? AND game_id = ? AND kv_key = ?", uid, gameID, key).
		First(&kv).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &kv, nil
}

// List 列出某游戏的全部键值（不含保留键）
//
// ⚠️ LIKE 里的 `_` 是**单字符通配符**，不是字面下划线。
// 直接写 `NOT LIKE '__%'` 会匹配「任意 2 个字符 + 任意内容」，
// 从而把 `save` 这类正常 key 也排除掉（实测踩过：写入成功但 list 返回空）。
// 必须用 ESCAPE 子句把下划线转义成字面量。
func (r *GameKvRepository) List(ctx context.Context, uid uint, gameID string) ([]*entities.GameKv, error) {
	var list []*entities.GameKv
	err := r.DB.WithContext(ctx).
		Where(`uid = ? AND game_id = ? AND kv_key NOT LIKE ? ESCAPE '\'`,
			uid, gameID, `\_\_%`).
		Order("kv_key ASC").
		Find(&list).Error
	return list, err
}

// ListAll 列出某游戏的全部键值（含保留键，仅供内部/管理使用）
func (r *GameKvRepository) ListAll(ctx context.Context, uid uint, gameID string) ([]*entities.GameKv, error) {
	var list []*entities.GameKv
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND game_id = ?", uid, gameID).
		Order("kv_key ASC").Find(&list).Error
	return list, err
}

// Put 整块写入（upsert）。
//
// expectedVersion > 0 时做乐观锁校验：
//   - 记录不存在 → 插入
//   - 记录存在且 version 匹配 → 更新
//   - 记录存在但 version 不匹配 → 返回 ErrVersionConflict
func (r *GameKvRepository) Put(ctx context.Context, kv *entities.GameKv, expectedVersion int64) error {
	return r.PutWithTx(ctx, r.DB, kv, expectedVersion)
}

// PutWithTx 在给定事务内写入。
//
// ⚠️ 事务内**必须**用这个方法，理由同 GetWithTx。
func (r *GameKvRepository) PutWithTx(ctx context.Context, tx *gorm.DB, kv *entities.GameKv, expectedVersion int64) error {
	if expectedVersion > 0 {
		// 乐观锁路径：先查当前版本（同一事务内）
		cur, err := r.GetWithTx(ctx, tx, kv.UID, kv.GameID, kv.Key)
		if err != nil {
			return err
		}
		if cur == nil {
			return ErrVersionConflict
		}
		if cur.Version != expectedVersion {
			return ErrVersionConflict
		}
		res := tx.WithContext(ctx).Model(&entities.GameKv{}).
			Where("uid = ? AND game_id = ? AND kv_key = ? AND version = ?",
				kv.UID, kv.GameID, kv.Key, expectedVersion).
			Updates(map[string]interface{}{
				"value":   kv.Value,
				"size":    kv.Size,
				"version": expectedVersion + 1,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrVersionConflict
		}
		kv.Version = expectedVersion + 1
		return nil
	}

	// 无锁路径：直接 upsert，version 自增
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "uid"}, {Name: "game_id"}, {Name: "kv_key"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"value":      kv.Value,
			"size":       kv.Size,
			"version":    gorm.Expr("version + 1"),
			"updated_at": gorm.Expr("updated_at"),
		}),
	}).Create(kv).Error; err != nil {
		return err
	}

	// ⚠️ upsert 走的是 SQL 表达式 `version + 1`，GORM 不会把结果回写到结构体，
	// 所以这里必须重新读一次真实版本号。
	// 不回读的后果：客户端拿到的 version 永远是 0，乐观锁（expected_version）
	// 永远匹配不上 → 多设备冲突检测形同虚设；快照里记录的 version 也是错的。
	// 实测踩过：写入成功但 version 一直是 0。
	row, err := r.GetWithTx(ctx, tx, kv.UID, kv.GameID, kv.Key)
	if err != nil {
		return err
	}
	if row != nil {
		kv.Version = row.Version
	}
	return nil
}

// Delete 删除单个键
func (r *GameKvRepository) Delete(ctx context.Context, uid uint, gameID, key string) (int64, error) {
	res := r.DB.WithContext(ctx).
		Where("uid = ? AND game_id = ? AND kv_key = ?", uid, gameID, key).
		Delete(&entities.GameKv{})
	return res.RowsAffected, res.Error
}

// DeleteGame 卸载游戏：删除该用户该游戏的全部键值（含保留键）
func (r *GameKvRepository) DeleteGame(ctx context.Context, uid uint, gameID string) (int64, error) {
	res := r.DB.WithContext(ctx).
		Where("uid = ? AND game_id = ?", uid, gameID).
		Delete(&entities.GameKv{})
	return res.RowsAffected, res.Error
}

// CountKeys 统计某游戏已用的 key 数（配额检查）
func (r *GameKvRepository) CountKeys(ctx context.Context, uid uint, gameID string) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&entities.GameKv{}).
		Where("uid = ? AND game_id = ?", uid, gameID).
		Count(&count).Error
	return count, err
}

// TotalSize 统计某用户所有游戏存档的总字节数（配额检查）
func (r *GameKvRepository) TotalSize(ctx context.Context, uid uint) (int64, error) {
	var total int64
	err := r.DB.WithContext(ctx).Model(&entities.GameKv{}).
		Where("uid = ?", uid).
		Select("COALESCE(SUM(size), 0)").Scan(&total).Error
	return total, err
}

// ErrVersionConflict 版本冲突（多设备同时写）
var ErrVersionConflict = errors.New("game kv version conflict")

// IsReservedKey 是否为宿主保留键
func IsReservedKey(key string) bool {
	return strings.HasPrefix(key, entities.GameKvReservedPrefix)
}
