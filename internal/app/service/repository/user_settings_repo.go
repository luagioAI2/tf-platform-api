package repository

import (
	"context"
	"errors"

	"tf-api/internal/app/entities"

	"github.com/google/wire"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var UserSettingsRepositorySet = wire.NewSet(wire.Struct(new(UserSettingsRepository), "*"))

// UserSettingsRepository 家长设置数据访问
type UserSettingsRepository struct {
	DB *gorm.DB
}

// NewUserSettingsRepository 直接构造（供脚本 / 测试使用）
func NewUserSettingsRepository(db *gorm.DB) *UserSettingsRepository {
	return &UserSettingsRepository{DB: db}
}

// GetByUID 读一个账号的设置。**没有就返回 nil, nil** —— 不是错误：
// 「这个账号还没设过」是正常状态，调用方拿到 nil 就用客户端的默认值。
func (r *UserSettingsRepository) GetByUID(ctx context.Context, uid uint) (*entities.UserSettings, error) {
	var s entities.UserSettings
	err := r.DB.WithContext(ctx).Where("uid = ?", uid).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Upsert 整份覆盖。
//
// ★ 用 `uid` 唯一索引做冲突目标，`DoUpdates` 里**显式列出**要覆盖的列：
//   写 `UpdateAll: true` 会把 `created_at` 也刷成现在 —— 之后没人分得清
//   「这份设置是什么时候第一次建的」。
func (r *UserSettingsRepository) Upsert(ctx context.Context, s *entities.UserSettings) error {
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "uid"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"payload", "size", "version", "updated_at",
		}),
	}).Create(s).Error
}
