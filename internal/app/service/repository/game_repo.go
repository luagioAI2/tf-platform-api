package repository

import (
	"context"
	"errors"
	"time"

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
			"price_points", "price_diamond", "default_owned", "voucher_per_coin",
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

// stampGrant 补时间戳。
//
// 平台的 `BaseModel` 是 `autoCreateTime:false` / `autoUpdateTime:false` ——
// GORM **不会**自己填，漏了就是 `created_at = 0`（1970）。
// 授予游戏有四个调用点（默认拥有 / 购买 / 心愿 / 兑换码），
// 各写一遍迟早漏一个，所以收在这里统一补。
func stampGrant(ug *entities.UserGame) {
	if ug.CreatedAt == 0 {
		ug.CreatedAt = time.Now().Unix()
	}
	ug.UpdatedAt = ug.CreatedAt
}

// GrantGame 授予游戏（幂等，已拥有则不报错）
func (r *GameRepository) GrantGame(ctx context.Context, ug *entities.UserGame) error {
	stampGrant(ug)
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "uid"}, {Name: "game_id"}},
		DoNothing: true,
	}).Create(ug).Error
}

// GrantGameWithTx 在事务内授予
func (r *GameRepository) GrantGameWithTx(tx *gorm.DB, ug *entities.UserGame) error {
	stampGrant(ug)
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "uid"}, {Name: "game_id"}},
		DoNothing: true,
	}).Create(ug).Error
}

// RevokeGameWithTx 事务内收回游戏。
//
// ⚠️ 只删 `user_game` 那一行，**不删存档**（`game_kv`）。
//
// 为什么保留存档：取消兑换往往是家长「这次先不换」，
// 过几天可能又给买了。存档里有孩子辛苦攒的进度（农场等级、金币），
// 删掉等于惩罚孩子 —— 而这不是取消兑换想做的事。
// 而且重新授予时（`GrantGame`）存档还在，孩子能接着玩。
//
// ★★ 必须 `Unscoped()` **硬删**，不能软删。
//
//	`user_game` 上有 `uk_uid_game (uid, game_id)` 唯一索引，而软删只是把
//	`deleted_at` 写上一个时间戳 —— **那一行仍然占着唯一索引**。于是：
//
//	  兑换 → 取消（软删）→ 再兑换同一款
//	    ⇒ `GrantGameWithTx` 的 `ON CONFLICT DO NOTHING` 静默不写
//	    ⇒ 孩子付了钱、界面说「兑换成功」，库里却一条拥有关系都没有。
//
//	实测（`TestWishRedeemAgainAfterCancelGetsGamesBack`）：5 连开取消后
//	再兑换，5 款只回来 1 款 —— 回来的那款是「上一单恰好没开出来」的那个。
//	单开盲盒同样中招，只是概率低、不容易被发现。
//
//	这条不是新问题，是「软删 + upsert = 写成功却谁也读不出来」的老形状
//	（见 `.workbuddy-ai/memory/notes/backend-go.md`）。
func (r *GameRepository) RevokeGameWithTx(tx *gorm.DB, uid uint, gameID string) error {
	return tx.Unscoped().
		Where("uid = ? AND game_id = ?", uid, gameID).
		Delete(&entities.UserGame{}).Error
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
