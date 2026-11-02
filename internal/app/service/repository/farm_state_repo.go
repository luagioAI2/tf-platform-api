package repository

import (
	"context"
	"errors"
	"time"

	"tf-api/internal/app/entities"

	"github.com/google/wire"
	"gorm.io/gorm"
)

var FarmStateRepositorySet = wire.NewSet(wire.Struct(new(FarmStateRepository), "*"))

// FarmStateRepository 农场状态数据访问。
//
// 一个用户只有一行，整块读写。并发靠 `rev` 乐观锁（见 entities.FarmState）。
type FarmStateRepository struct {
	DB *gorm.DB
}

// NewFarmStateRepository 直接构造（供脚本 / 测试使用）
func NewFarmStateRepository(db *gorm.DB) *FarmStateRepository {
	return &FarmStateRepository{DB: db}
}

// Get 读取某用户的农场状态（不存在返回 nil, nil）
func (r *FarmStateRepository) Get(ctx context.Context, uid uint) (*entities.FarmState, error) {
	return r.GetWithTx(ctx, r.DB, uid)
}

// GetWithTx 在给定事务内读取。
//
// ⚠️ 事务内**必须**用这个方法，不能用 Get —— 用外层句柄会去连接池申请第二条连接，
// 池满时死锁（同 game_kv_repo 的教训）。
func (r *FarmStateRepository) GetWithTx(ctx context.Context, tx *gorm.DB, uid uint) (*entities.FarmState, error) {
	var st entities.FarmState
	err := tx.WithContext(ctx).Where("uid = ?", uid).First(&st).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// SaveTx 写入农场状态（乐观锁）。
//
// 返回 (新版本号, 是否冲突, error)。冲突时**什么都没写**，调用方应当
// 让客户端重新拉一次再改。
//
// ★ `updated_at` 必须手写：`BaseModel` 是 `autoUpdateTime:false`，
//   而这里用的是 `Updates(map)`，GORM 的 BeforeUpdate 钩子不保证跑到 ——
//   漏了就会落 0（1970 年），而且**不报错**。
func (r *FarmStateRepository) SaveTx(
	ctx context.Context, tx *gorm.DB, uid uint, payload string, expectedRev int64, force bool,
) (int64, bool, error) {
	cur, err := r.GetWithTx(ctx, tx, uid)
	if err != nil {
		return 0, false, err
	}
	now := time.Now().Unix()

	if cur == nil {
		// 首次保存。客户端如果带着非 0 的 rev 来，说明它以为服务端有数据 ——
		// 那是「换了设备 / 服务端被清过」，让它重拉，别直接覆盖。
		if expectedRev != 0 && !force {
			return 0, true, nil
		}
		st := &entities.FarmState{UID: uid, Rev: 1, Payload: payload, Size: int64(len(payload))}
		st.CreatedAt = now
		st.UpdatedAt = now
		if err := tx.Create(st).Error; err != nil {
			return 0, false, err
		}
		return st.Rev, false, nil
	}

	if !force && cur.Rev != expectedRev {
		return cur.Rev, true, nil
	}

	next := cur.Rev + 1
	err = tx.Model(&entities.FarmState{}).
		Where("uid = ?", uid).
		Updates(map[string]any{
			"rev":        next,
			"payload":    payload,
			"size":       int64(len(payload)),
			"updated_at": now,
		}).Error
	if err != nil {
		return 0, false, err
	}
	return next, false, nil
}
