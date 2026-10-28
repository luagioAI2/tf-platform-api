package repository

import (
	"context"
	"errors"

	"tf-api/internal/app/entities"

	"github.com/google/wire"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var GameRepositorySet = wire.NewSet(wire.Struct(new(GameRepository), "*"))

// GameRepository 游戏与购买数据访问
type GameRepository struct {
	DB *gorm.DB
}

// NewGameRepository 直接构造（供 seed / 脚本使用，wire 走 struct provider）
func NewGameRepository(db *gorm.DB) *GameRepository {
	return &GameRepository{DB: db}
}

// ============================ 游戏定义 ============================

// GetByGameID 按业务 ID 查询
func (r *GameRepository) GetByGameID(ctx context.Context, gameID string) (*entities.Game, error) {
	var g entities.Game
	err := r.DB.WithContext(ctx).Where("game_id = ?", gameID).First(&g).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// ListGames 分页查询游戏列表
func (r *GameRepository) ListGames(ctx context.Context, req *entities.GameListReq) ([]*entities.Game, int64, error) {
	tx := r.DB.WithContext(ctx).Model(&entities.Game{}).
		Where("status != ?", entities.GameStatusOffline)

	if req.Kind != "" {
		tx = tx.Where("kind = ?", req.Kind)
	}
	if req.Keyword != "" {
		like := "%" + req.Keyword + "%"
		tx = tx.Where("name LIKE ? OR tagline LIKE ?", like, like)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	req.Normalize()
	list := make([]*entities.Game, 0)
	err := tx.Order("sort DESC, id ASC").
		Offset(req.Offset()).Limit(req.PageSize).
		Find(&list).Error
	return list, total, err
}

// ListByGameIDs 按 ID 列表批量查询
func (r *GameRepository) ListByGameIDs(ctx context.Context, gameIDs []string) ([]*entities.Game, error) {
	if len(gameIDs) == 0 {
		return nil, nil
	}
	var list []*entities.Game
	err := r.DB.WithContext(ctx).Where("game_id IN ?", gameIDs).Find(&list).Error
	return list, err
}

// Create 创建游戏（种子数据用）
func (r *GameRepository) Create(ctx context.Context, g *entities.Game) error {
	return r.DB.WithContext(ctx).Create(g).Error
}

// Upsert 按 game_id 插入或更新（种子脚本用）
func (r *GameRepository) Upsert(ctx context.Context, g *entities.Game) error {
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "game_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"name", "emoji", "tagline", "kind", "tint", "status",
			"price_points", "price_diamond", "default_owned",
			"entry_url", "cover_url", "age_hint", "session_hint",
			"learn", "about", "shots", "sort", "updated_at",
		}),
	}).Create(g).Error
}

// ============================ 用户游戏 ============================

// ListOwnedGameIDs 查询用户拥有的游戏 ID 列表
func (r *GameRepository) ListOwnedGameIDs(ctx context.Context, uid uint) ([]string, error) {
	var ids []string
	err := r.DB.WithContext(ctx).Model(&entities.UserGame{}).
		Where("uid = ?", uid).
		Pluck("game_id", &ids).Error
	return ids, err
}

// HasGame 用户是否拥有某游戏
func (r *GameRepository) HasGame(ctx context.Context, uid uint, gameID string) (bool, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&entities.UserGame{}).
		Where("uid = ? AND game_id = ?", uid, gameID).
		Count(&count).Error
	return count > 0, err
}

// GrantGame 授予游戏（幂等，已拥有则不报错）
func (r *GameRepository) GrantGame(ctx context.Context, ug *entities.UserGame) error {
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "uid"}, {Name: "game_id"}},
		DoNothing: true,
	}).Create(ug).Error
}

// GrantGameWithTx 在事务内授予
func (r *GameRepository) GrantGameWithTx(tx *gorm.DB, ug *entities.UserGame) error {
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "uid"}, {Name: "game_id"}},
		DoNothing: true,
	}).Create(ug).Error
}

// ============================ 订单 ============================

// CreateOrder 创建订单
func (r *GameRepository) CreateOrder(ctx context.Context, o *entities.GameOrder) error {
	return r.DB.WithContext(ctx).Create(o).Error
}

// CreateOrderWithTx 事务内创建订单
func (r *GameRepository) CreateOrderWithTx(tx *gorm.DB, o *entities.GameOrder) error {
	return tx.Create(o).Error
}

// GetOrderByOrderID 按订单号查询
func (r *GameRepository) GetOrderByOrderID(ctx context.Context, orderID string) (*entities.GameOrder, error) {
	var o entities.GameOrder
	err := r.DB.WithContext(ctx).Where("order_id = ?", orderID).First(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// GetOrderByClientRequestID 按客户端幂等键查询
func (r *GameRepository) GetOrderByClientRequestID(ctx context.Context, uid uint, reqID string) (*entities.GameOrder, error) {
	if reqID == "" {
		return nil, nil
	}
	var o entities.GameOrder
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND client_request_id = ?", uid, reqID).
		First(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// UpdateOrderStatus 更新订单状态
func (r *GameRepository) UpdateOrderStatus(ctx context.Context, orderID, status string) error {
	return r.DB.WithContext(ctx).Model(&entities.GameOrder{}).
		Where("order_id = ?", orderID).
		Update("status", status).Error
}

// CountUserGames 用户已拥有游戏数
func (r *GameRepository) CountUserGames(ctx context.Context, uid uint) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&entities.UserGame{}).
		Where("uid = ?", uid).Count(&count).Error
	return count, err
}
