package repository

import (
	"context"
	"errors"
	"strings"
	"time"

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
	//
	// ⚠️ 插入时版本必须从 **1** 起算（实体上 `default:0`，GORM 会把结构体里的 0
	// 原样写进 version）。0 在这个协议里是**哨兵**，含义是「这一行还不存在 /
	// 我手里没有令牌」，客户端拿 0 当 `expected_version` 就走这条无锁路径。
	// 落 0 的后果：写完这一行，客户端缓存到的还是 0 ⇒ 下一次写**又走无锁路径**
	// ⇒ 一个键的前两次写完全没有冲突检测，两台设备互盖一句话都不报。
	// 实测踩过：探针里第一笔写落 0、第二笔仍走无锁，冲突检测形同虚设。
	// 回档路径（game_snapshot_srv）本来就传 `toVersion = 0 + 1`，这里与它对齐。
	if kv.Version < 1 {
		kv.Version = 1
	}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "uid"}, {Name: "game_id"}, {Name: "kv_key"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"value":   kv.Value,
			"size":    kv.Size,
			"version": gorm.Expr("version + 1"),
			// ⚠️ 必须把 `deleted_at` 清成 NULL，否则**写进去的行谁都读不出来**。
			//
			// `BaseModel` 带 `gorm.DeletedAt`，GORM 的 Delete 是**软删**
			// （只打一个 `deleted_at`），而 Get / List 都会自动追加
			// `deleted_at IS NULL`。upsert 命中一条软删过的行时只改 value/version，
			// `deleted_at` 原样留着 ⇒ 接口返回 200 和一个版本号，客户端以为存上了，
			// 实际上这一行对所有读路径都不可见（包括下一次拉取）。
			//
			// 实测踩过：卸载游戏（软删全部键）→ 重装 → 存档"存成功" →
			// 服务端 get/list 永远返回空，客户端毫不知情，换台设备进度全丢。
			// 症状之所以难查：写成功、无报错、版本号还在涨。
			"deleted_at": nil,
			// 原来这里是 `gorm.Expr("updated_at")` —— 把列赋给它自己，
			// 等于**永远不刷新更新时间**（读出来的 `updated_at` 一直是第一次写的）。
			// upsert 走的是原生 clause，不经过 GORM 的钩子，所以得手写。
			"updated_at": time.Now().Unix(),
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
//
// ⚠️ 用 `Unscoped()` **硬删**。这里不是「标记一下」的场景：
// 存档是整块覆盖的 JSON，软删只会留下一条读不到、却仍占着唯一索引
// `(uid, game_id, kv_key)` 的尸体。更糟的是 upsert 命中它会变成
// 「写成功但谁也读不出来」（见 PutWithTx 里 `deleted_at: nil` 那段）。
func (r *GameKvRepository) Delete(ctx context.Context, uid uint, gameID, key string) (int64, error) {
	res := r.DB.WithContext(ctx).Unscoped().
		Where("uid = ? AND game_id = ? AND kv_key = ?", uid, gameID, key).
		Delete(&entities.GameKv{})
	return res.RowsAffected, res.Error
}

// DeleteGame 卸载游戏：删除该用户该游戏的全部键值（含保留键）
//
// ⚠️ 同样硬删 —— 「卸载」的语义就是数据没了。软删的后果实测过：
// 卸载后重装，插件第一次 `storage.set` 会 upsert 到那条软删的行上，
// 服务端返回 200 和版本号，客户端以为存上了，而 `get` / `list` 永远是空。
func (r *GameKvRepository) DeleteGame(ctx context.Context, uid uint, gameID string) (int64, error) {
	res := r.DB.WithContext(ctx).Unscoped().
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
