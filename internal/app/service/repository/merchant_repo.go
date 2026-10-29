package repository

import (
	"context"
	"errors"

	"tf-api/internal/app/entities"

	"github.com/google/wire"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var MerchantRepositorySet = wire.NewSet(wire.Struct(new(MerchantRepository), "*"))

// MerchantRepository 招商申请数据访问
type MerchantRepository struct {
	DB *gorm.DB
}

// NewMerchantRepository 直接构造（供脚本使用）
func NewMerchantRepository(db *gorm.DB) *MerchantRepository {
	return &MerchantRepository{DB: db}
}

// Upsert 按 (uid, role) 幂等写入。
//
// ⚠️ 冲突时**只更新「申请人填的那几个字段」**，绝不碰 `status` /
// `admin_url` / `reviewed_*` —— 那是审核结果，改申请不该把审核结果冲掉。
//
// 场景：家长提交后已通过审核（拿到后台地址），过几天回来改了联系电话 ——
// 如果 upsert 把 status 重置成 pending、admin_url 清空，他就**莫名其妙
// 失去了后台入口**，而且不报错。
func (r *MerchantRepository) Upsert(ctx context.Context, app *entities.MerchantApplication) error {
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "uid"}, {Name: "role"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"name":       app.Name,
			"contact":    app.Contact,
			"note":       app.Note,
			"updated_at": app.UpdatedAt,
		}),
	}).Create(app).Error
}

// Get 查某人的某个角色的申请
func (r *MerchantRepository) Get(ctx context.Context, uid uint, role entities.MerchantRole) (*entities.MerchantApplication, error) {
	var app entities.MerchantApplication
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND role = ?", uid, role).
		First(&app).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &app, nil
}

// ListByUID 查某人的全部申请（最多两条：parent + merchant）
func (r *MerchantRepository) ListByUID(ctx context.Context, uid uint) ([]*entities.MerchantApplication, error) {
	var list []*entities.MerchantApplication
	err := r.DB.WithContext(ctx).Where("uid = ?", uid).Order("id ASC").Find(&list).Error
	return list, err
}

// GetByID 按 ID 查（后台审核用）
func (r *MerchantRepository) GetByID(ctx context.Context, id uint) (*entities.MerchantApplication, error) {
	var app entities.MerchantApplication
	err := r.DB.WithContext(ctx).Where("id = ?", id).First(&app).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &app, nil
}

// GetByIDForUpdate 事务内加锁读（防并发重复审核）
func (r *MerchantRepository) GetByIDForUpdate(ctx context.Context, tx *gorm.DB, id uint) (*entities.MerchantApplication, error) {
	var app entities.MerchantApplication
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", id).First(&app).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &app, nil
}

// ReviewCAS 审核（带状态校验）。
//
// 只允许从 `pending` 审核 —— 已通过的单子再点一次「通过」不该改动什么，
// 更不该把 `ReviewedAt` 刷新一遍（审计上看不出谁什么时候审的）。
func (r *MerchantRepository) ReviewCAS(
	ctx context.Context, uid uint, id uint,
	to entities.MerchantStatus, adminURL, note string, operatorID uint, at int64,
) (int64, error) {
	fields := map[string]interface{}{
		"status":      string(to),
		"reviewed_at": at,
		"reviewed_by": operatorID,
		"review_note": note,
		"updated_at":  at,
	}
	if adminURL != "" {
		fields["admin_url"] = adminURL
	}

	res := r.DB.WithContext(ctx).Model(&entities.MerchantApplication{}).
		Where("id = ? AND status = ?", id, string(entities.MerchantStatusPending)).
		Updates(fields)
	return res.RowsAffected, res.Error
}

// List 后台列表
func (r *MerchantRepository) List(ctx context.Context, q MerchantQuery) ([]*entities.MerchantApplication, int64, error) {
	tx := r.DB.WithContext(ctx).Model(&entities.MerchantApplication{})

	if q.Role != "" {
		tx = tx.Where("role = ?", string(q.Role))
	}
	if q.Status != "" {
		tx = tx.Where("status = ?", string(q.Status))
	}
	if q.Keyword != "" {
		like := "%" + q.Keyword + "%"
		tx = tx.Where("name LIKE ? OR contact LIKE ?", like, like)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page, pageSize := q.Page, q.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 30
	}

	var list []*entities.MerchantApplication
	err := tx.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error
	return list, total, err
}

// MerchantQuery 列表查询条件
type MerchantQuery struct {
	Role     entities.MerchantRole
	Status   entities.MerchantStatus
	Keyword  string
	Page     int
	PageSize int
}

// CountByStatus 统计某状态数量（后台红点）
func (r *MerchantRepository) CountByStatus(ctx context.Context, status entities.MerchantStatus) (int64, error) {
	var n int64
	err := r.DB.WithContext(ctx).Model(&entities.MerchantApplication{}).
		Where("status = ?", string(status)).Count(&n).Error
	return n, err
}

// CountInvitedBy 统计某家长**邀请成功**（已通过）的商家数
//
// 用途：家长那一侧的「分成」看板 —— 他邀请了几家、几家通过了。
// 真正的分成金额要等结算规则定下来，这里只给邀请数。
func (r *MerchantRepository) CountInvitedBy(ctx context.Context, uid uint) (total, approved int64, err error) {
	tx := r.DB.WithContext(ctx).Model(&entities.MerchantApplication{}).
		Where("uid = ? AND role = ?", uid, string(entities.MerchantRoleParent))
	if err = tx.Count(&total).Error; err != nil {
		return 0, 0, err
	}
	err = r.DB.WithContext(ctx).Model(&entities.MerchantApplication{}).
		Where("uid = ? AND role = ? AND status = ?",
			uid, string(entities.MerchantRoleParent), string(entities.MerchantStatusApproved)).
		Count(&approved).Error
	return total, approved, err
}
