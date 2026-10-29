package repository

import (
	"context"
	"errors"

	"tf-api/internal/app/entities"

	"github.com/google/wire"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var RedeemRepositorySet = wire.NewSet(wire.Struct(new(RedeemRepository), "*"))

// RedeemRepository 兑换商城数据访问
type RedeemRepository struct {
	DB *gorm.DB
}

// NewRedeemRepository 直接构造（供脚本使用）
func NewRedeemRepository(db *gorm.DB) *RedeemRepository {
	return &RedeemRepository{DB: db}
}

// ============================ 商品 ============================

// CreateItem 创建商品
func (r *RedeemRepository) CreateItem(ctx context.Context, item *entities.RedeemItem) error {
	return r.DB.WithContext(ctx).Create(item).Error
}

// GetItem 按业务 ID 查询商品
func (r *RedeemRepository) GetItem(ctx context.Context, uid uint, itemID string) (*entities.RedeemItem, error) {
	var item entities.RedeemItem
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND item_id = ?", uid, itemID).
		First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// GetItemForUpdate 事务内加锁读商品（兑换时用，防止并发改价/改库存）
func (r *RedeemRepository) GetItemForUpdate(ctx context.Context, tx *gorm.DB, uid uint, itemID string) (*entities.RedeemItem, error) {
	var item entities.RedeemItem
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("uid = ? AND item_id = ?", uid, itemID).
		First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// ListItems 列出商品（可按归档筛选）
//
// 返回商品列表以及「今天已换数量」映射，供 Service 计算可换性。
func (r *RedeemRepository) ListItems(ctx context.Context, uid uint, includeArchived bool) ([]*entities.RedeemItem, error) {
	tx := r.DB.WithContext(ctx).Where("uid = ?", uid)
	if !includeArchived {
		tx = tx.Where("archived = ?", false)
	}
	var list []*entities.RedeemItem
	err := tx.Order("sort DESC, id ASC").Find(&list).Error
	return list, err
}

// UpdateItem 更新商品
func (r *RedeemRepository) UpdateItem(ctx context.Context, uid uint, itemID string, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}
	return r.DB.WithContext(ctx).Model(&entities.RedeemItem{}).
		Where("uid = ? AND item_id = ?", uid, itemID).
		Updates(fields).Error
}

// DeleteItem 删除商品
func (r *RedeemRepository) DeleteItem(ctx context.Context, uid uint, itemID string) error {
	return r.DB.WithContext(ctx).
		Where("uid = ? AND item_id = ?", uid, itemID).
		Delete(&entities.RedeemItem{}).Error
}

// ============================ 记录 ============================

// CreateRecord 创建兑换记录
func (r *RedeemRepository) CreateRecord(ctx context.Context, tx *gorm.DB, rec *entities.RedeemRecord) error {
	db := r.DB
	if tx != nil {
		db = tx
	}
	return db.WithContext(ctx).Create(rec).Error
}

// GetRecord 查询记录
func (r *RedeemRepository) GetRecord(ctx context.Context, uid uint, recordID string) (*entities.RedeemRecord, error) {
	var rec entities.RedeemRecord
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND record_id = ?", uid, recordID).
		First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// GetRecordForUpdate 事务内加锁读记录（取消退款时用，防重复退款）
func (r *RedeemRepository) GetRecordForUpdate(ctx context.Context, tx *gorm.DB, uid uint, recordID string) (*entities.RedeemRecord, error) {
	var rec entities.RedeemRecord
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("uid = ? AND record_id = ?", uid, recordID).
		First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// ListRecords 分页查询兑换记录
func (r *RedeemRepository) ListRecords(ctx context.Context, uid uint, page, pageSize int) ([]*entities.RedeemRecord, int64, error) {
	tx := r.DB.WithContext(ctx).Model(&entities.RedeemRecord{}).Where("uid = ?", uid)

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 30
	}

	var list []*entities.RedeemRecord
	err := tx.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error
	return list, total, err
}

// CountTodayByItem 统计某商品今天已换多少个（限额判断）
//
// ⚠️ 「今天」按**服务端时区**的 0 点算。跨时区部署时要注意。
func (r *RedeemRepository) CountTodayByItem(ctx context.Context, uid uint, itemID string, dayStart int64) (int64, error) {
	var total int64
	err := r.DB.WithContext(ctx).Model(&entities.RedeemRecord{}).
		Where("uid = ? AND item_id = ? AND created_at >= ? AND cancelled = ?",
			uid, itemID, dayStart, false).
		Select("COALESCE(SUM(quantity), 0)").Scan(&total).Error
	return total, err
}

// HasRedeemedOnce 是否换过该商品（`once` 商品判断）
//
// ⚠️ 判据是**兑换记录**，不是库存 —— 库存是家长手填的，记录才是事实。
func (r *RedeemRepository) HasRedeemedOnce(ctx context.Context, uid uint, itemID string) (bool, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&entities.RedeemRecord{}).
		Where("uid = ? AND item_id = ? AND cancelled = ?", uid, itemID, false).
		Count(&count).Error
	return count > 0, err
}

// CountRedeemedByItem 统计某商品累计已换数量（库存判断）
func (r *RedeemRepository) CountRedeemedByItem(ctx context.Context, uid uint, itemID string) (int64, error) {
	var total int64
	err := r.DB.WithContext(ctx).Model(&entities.RedeemRecord{}).
		Where("uid = ? AND item_id = ? AND cancelled = ?", uid, itemID, false).
		Select("COALESCE(SUM(quantity), 0)").Scan(&total).Error
	return total, err
}

// UpdateRecord 更新记录（兑现/取消）
func (r *RedeemRepository) UpdateRecord(ctx context.Context, tx *gorm.DB, uid uint, recordID string, fields map[string]interface{}) (int64, error) {
	db := r.DB
	if tx != nil {
		db = tx
	}
	res := db.WithContext(ctx).Model(&entities.RedeemRecord{}).
		Where("uid = ? AND record_id = ?", uid, recordID).
		Updates(fields)
	return res.RowsAffected, res.Error
}

// UpdateRecordStatusCAS 带状态校验的更新（防重复取消/兑现）
func (r *RedeemRepository) UpdateRecordStatusCAS(
	ctx context.Context, tx *gorm.DB, uid uint, recordID string,
	fromCancelled, fromFulfilled bool, fields map[string]interface{},
) (int64, error) {
	db := r.DB
	if tx != nil {
		db = tx
	}
	res := db.WithContext(ctx).Model(&entities.RedeemRecord{}).
		Where("uid = ? AND record_id = ? AND cancelled = ? AND fulfilled = ?",
			uid, recordID, fromCancelled, fromFulfilled).
		Updates(fields)
	return res.RowsAffected, res.Error
}

// SumPointsRedeemed 统计某时间段用掉的积分（家长看板）
func (r *RedeemRepository) SumPointsRedeemed(ctx context.Context, uid uint, from, to int64) (int64, int64, error) {
	var points, coupon int64
	err := r.DB.WithContext(ctx).Model(&entities.RedeemRecord{}).
		Where("uid = ? AND created_at >= ? AND created_at <= ? AND cancelled = ?", uid, from, to, false).
		Select("COALESCE(SUM(points_paid), 0)").Scan(&points).Error
	if err != nil {
		return 0, 0, err
	}
	err = r.DB.WithContext(ctx).Model(&entities.RedeemRecord{}).
		Where("uid = ? AND created_at >= ? AND created_at <= ? AND cancelled = ?", uid, from, to, false).
		Select("COALESCE(SUM(coupon_paid), 0)").Scan(&coupon).Error
	return points, coupon, err
}
