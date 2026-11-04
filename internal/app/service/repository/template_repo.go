package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"tf-api/internal/app/entities"

	"github.com/google/wire"
	"gorm.io/gorm"
)

// TemplateRepositorySet wire 提供者（见 service/repository/set.go）
var TemplateRepositorySet = wire.NewSet(NewTemplateRepository)

var (
	// ErrInvalidInvite 邀请码格式/校验位不对
	ErrInvalidInvite = errors.New("invalid invite code")
	// ErrInviteMerchantNotApproved 码是对的，但这个 uid 不是已通过的商家
	ErrInviteMerchantNotApproved = errors.New("invite merchant not approved")
)

// TemplateRepository 模板读取（后台的写入在 tf-admin，两边共用同一张表）。
type TemplateRepository struct {
	DB *gorm.DB
}

func NewTemplateRepository(db *gorm.DB) *TemplateRepository {
	return &TemplateRepository{DB: db}
}

// DefaultFor 取某个分组的默认模板。
//
// ownerUID > 0：找该商家的默认模板；ownerUID == 0：找官方默认模板。
// 找不到返回 (nil, nil) —— 这是**正常的回落信号**，不是错误。
func (r *TemplateRepository) DefaultFor(ctx context.Context, kind entities.TemplateKind, ownerUID uint) (*entities.Template, error) {
	var list []entities.Template
	q := r.DB.WithContext(ctx).Where("kind = ? AND is_default = 1", string(kind))
	if ownerUID > 0 {
		q = q.Where("scope = ? AND owner_uid = ?", string(entities.TemplateScopeMerchant), ownerUID)
	} else {
		q = q.Where("scope = ? AND owner_uid = 0", string(entities.TemplateScopeOfficial))
	}
	if err := q.Order("id DESC").Limit(1).Find(&list).Error; err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return &list[0], nil
}

// ItemsOf 取模板下的全部条目（按 sort、id 稳定排序）。
func (r *TemplateRepository) ItemsOf(ctx context.Context, templateID uint) ([]entities.TemplateItem, error) {
	var items []entities.TemplateItem
	err := r.DB.WithContext(ctx).
		Where("template_id = ?", templateID).
		Order("sort ASC, id ASC").Find(&items).Error
	return items, err
}

// SourceMerchantUID 读用户归属的商家（0 = 官方兜底）。
func (r *TemplateRepository) SourceMerchantUID(ctx context.Context, uid uint) (uint, error) {
	var u entities.User
	err := r.DB.WithContext(ctx).
		Select("id, source_merchant_uid").
		Where("id = ?", uid).First(&u).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return u.SourceMerchantUID, nil
}

// ApplyInvite 把邀请码归属写到用户身上。
//
// 三道闸，一道都不能少：
//  1. 码本身要能解析且校验位对（`ParseInviteCode`）；
//  2. uid 必须是**已通过审核的商家**申请单（`merchant_application`）；
//  3. 已经归属过的用户**不覆盖** —— 第一次算数，后来再扫别人的码不改。
//
// ★ 别在「拼 SQL 之后」才判空：这里每一步都在拼之前判。
func (r *TemplateRepository) ApplyInvite(ctx context.Context, uid uint, code string) (uint, error) {
	merchantUID, ok := entities.ParseInviteCode(code)
	if !ok || merchantUID == 0 {
		return 0, ErrInvalidInvite
	}

	var approved int64
	if err := r.DB.WithContext(ctx).Table("merchant_application").
		Where("uid = ? AND role = ? AND status = ? AND deleted_at IS NULL",
			merchantUID,
			string(entities.MerchantRoleMerchant),
			string(entities.MerchantStatusApproved)).
		Count(&approved).Error; err != nil {
		return 0, err
	}
	if approved == 0 {
		return 0, ErrInviteMerchantNotApproved
	}

	var cur entities.User
	if err := r.DB.WithContext(ctx).
		Select("id, source_merchant_uid").
		Where("id = ?", uid).First(&cur).Error; err != nil {
		return 0, err
	}
	if cur.SourceMerchantUID != 0 {
		return cur.SourceMerchantUID, nil
	}

	// 条件里带 `source_merchant_uid = 0`：并发两个请求也不会互相覆盖。
	if err := r.DB.WithContext(ctx).Model(&entities.User{}).
		Where("id = ? AND source_merchant_uid = 0", uid).
		Updates(map[string]interface{}{
			"source_merchant_uid": merchantUID,
			"invite_code":         strings.TrimSpace(code),
			"updated_at":          time.Now().Unix(),
		}).Error; err != nil {
		return 0, err
	}
	return merchantUID, nil
}
