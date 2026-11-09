package repository

import (
	"context"
	"errors"

	"tf-api/internal/app/entities"

	"github.com/google/wire"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var GameReleaseRepositorySet = wire.NewSet(wire.Struct(new(GameReleaseRepository), "*"))

// GameReleaseRepository 游戏版本发布数据访问
type GameReleaseRepository struct {
	DB *gorm.DB
}

// NewGameReleaseRepository 直接构造（供脚本使用）
func NewGameReleaseRepository(db *gorm.DB) *GameReleaseRepository {
	return &GameReleaseRepository{DB: db}
}

// Publish 发布一个版本（把同 game+channel 的其他版本置为非生效）。
//
// 事务边界：**「把旧的置 false」和「插入新的」必须原子** ——
// 拆开会出现「两个 active」或者「一个 active 都没有」的瞬间，
// 前者会让客户端随机拿到两个版本之一，后者会让所有下载 404。
func (r *GameReleaseRepository) Publish(ctx context.Context, rel *entities.GameRelease) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 旧的取消生效
		if err := tx.Model(&entities.GameRelease{}).
			Where("game_id = ? AND channel = ? AND active = ?", rel.GameID, rel.Channel, true).
			Update("active", false).Error; err != nil {
			return err
		}

		// 2. 插入新版本（先尝试插入，冲突说明版本号重复）
		rel.Active = true
		if err := tx.Create(rel).Error; err != nil {
			return err
		}
		return nil
	})
}

// GetActive 查某游戏当前生效版本
func (r *GameReleaseRepository) GetActive(ctx context.Context, gameID, channel string) (*entities.GameRelease, error) {
	var rel entities.GameRelease
	err := r.DB.WithContext(ctx).
		Where("game_id = ? AND channel = ? AND active = ?", gameID, channel, true).
		First(&rel).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rel, nil
}

// GetByVersion 查指定版本
func (r *GameReleaseRepository) GetByVersion(ctx context.Context, gameID, channel, version string) (*entities.GameRelease, error) {
	var rel entities.GameRelease
	err := r.DB.WithContext(ctx).
		Where("game_id = ? AND channel = ? AND version = ?", gameID, channel, version).
		First(&rel).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rel, nil
}

// ListActive 列出所有游戏的当前生效版本（构建 catalog 用）
func (r *GameReleaseRepository) ListActive(ctx context.Context, channel string) ([]*entities.GameRelease, error) {
	var list []*entities.GameRelease
	err := r.DB.WithContext(ctx).
		Where("channel = ? AND active = ?", channel, true).
		Order("game_id ASC").Find(&list).Error
	return list, err
}

// List 列出发布记录（管理端）
func (r *GameReleaseRepository) List(ctx context.Context, q ReleaseQuery) ([]*entities.GameRelease, int64, error) {
	tx := r.DB.WithContext(ctx).Model(&entities.GameRelease{})
	if q.GameID != "" {
		tx = tx.Where("game_id = ?", q.GameID)
	}
	if q.Channel != "" {
		tx = tx.Where("channel = ?", q.Channel)
	}
	if q.OnlyActive {
		tx = tx.Where("active = ?", true)
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
		pageSize = 50
	}

	var list []*entities.GameRelease
	err := tx.Order("published_at DESC, id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&list).Error
	return list, total, err
}

// ReleaseQuery 版本查询条件
type ReleaseQuery struct {
	GameID     string
	Channel    string
	OnlyActive bool
	Page       int
	PageSize   int
}

// IncrDownloadCount 累加下载计数
func (r *GameReleaseRepository) IncrDownloadCount(ctx context.Context, releaseID uint) error {
	return r.DB.WithContext(ctx).Model(&entities.GameRelease{}).
		Where("id = ?", releaseID).
		UpdateColumn("download_count", gorm.Expr("download_count + 1")).Error
}

// Delete 删除发布记录
func (r *GameReleaseRepository) Delete(ctx context.Context, releaseID uint) error {
	return r.DB.WithContext(ctx).Where("id = ?", releaseID).
		Delete(&entities.GameRelease{}).Error
}

// Deactivate 下架某版本（active = false）
func (r *GameReleaseRepository) Deactivate(ctx context.Context, releaseID uint) error {
	return r.SetActive(ctx, releaseID, false)
}

// SetActive 上架 / 下架某版本。
//
// 之所以要能「再上架」：下架是运营手滑的高频动作，而重新发布同一版本号
// 会被唯一约束挡住（`GameReleaseDuplicate`）—— 没有这个反向开关，
// 误点一次就再也回不来了，只能换个版本号重发。
func (r *GameReleaseRepository) SetActive(ctx context.Context, releaseID uint, active bool) error {
	return r.DB.WithContext(ctx).Model(&entities.GameRelease{}).
		Where("id = ?", releaseID).Update("active", active).Error
}

// Upsert 幂等写入（发布脚本用）
func (r *GameReleaseRepository) Upsert(ctx context.Context, rel *entities.GameRelease) error {
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "game_id"}, {Name: "channel"}, {Name: "version"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"url", "sha256", "size", "asset_base", "min_host_api",
			"forced", "release_notes", "published_at", "updated_at",
		}),
	}).Create(rel).Error
}
