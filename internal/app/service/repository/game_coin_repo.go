package repository

import (
	"context"
	"errors"

	"tf-api/internal/app/entities"

	"github.com/google/wire"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var GameCoinRepositorySet = wire.NewSet(wire.Struct(new(GameCoinRepository), "*"))

// GameCoinRepository 游戏币账本与钱包快照。
//
// 与宿主三套币（WalletRepository）同一套模式：
// 账本是真相，钱包是快照，所有变动走 ApplyChange（行锁 + 版本号）。
type GameCoinRepository struct {
	DB *gorm.DB
}

// NewGameCoinRepository 直接构造（供脚本使用）
func NewGameCoinRepository(db *gorm.DB) *GameCoinRepository {
	return &GameCoinRepository{DB: db}
}

// GameCoinChange 游戏币变动参数
type GameCoinChange struct {
	UID    uint
	GameID string
	Delta  int64
	Source string
	RefID  string
	Memo   string
	// IdempotencyKey 幂等键
	IdempotencyKey string
	// AllowNegative 是否允许负余额
	AllowNegative bool
}

// ErrGameCoinInsufficient 游戏币余额不足
var ErrGameCoinInsufficient = errors.New("insufficient game coin")

// ApplyChange 原子地变更游戏币余额并记账。
func (r *GameCoinRepository) ApplyChange(ctx context.Context, c GameCoinChange) (int64, *entities.GameCoinLedger, error) {
	if c.Delta == 0 {
		return 0, nil, errors.New("delta must not be zero")
	}

	var (
		newBalance int64
		entry      *entities.GameCoinLedger
	)

	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 幂等检查
		if c.IdempotencyKey != "" {
			var existing entities.GameCoinLedger
			err := tx.Where("idempotency_key = ?", c.IdempotencyKey).First(&existing).Error
			if err == nil {
				entry = &existing
				newBalance = existing.BalanceAfter
				return ErrDuplicateLedger
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}

		// 行锁
		var w entities.GameWallet
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("uid = ? AND game_id = ?", c.UID, c.GameID).
			First(&w).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			w = entities.GameWallet{UID: c.UID, GameID: c.GameID}
			if err := tx.Create(&w).Error; err != nil {
				return err
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("uid = ? AND game_id = ?", c.UID, c.GameID).
				First(&w).Error; err != nil {
				return err
			}
		}

		newBalance = w.Balance + c.Delta
		if newBalance < 0 && !c.AllowNegative {
			return ErrGameCoinInsufficient
		}

		// 更新余额 + 累计统计
		updates := map[string]interface{}{
			"balance": newBalance,
			"version": w.Version + 1,
		}
		if c.Delta > 0 {
			updates["total_earned"] = w.TotalEarned + c.Delta
		} else {
			updates["total_spent"] = w.TotalSpent - c.Delta
		}

		res := tx.Model(&entities.GameWallet{}).
			Where("uid = ? AND game_id = ? AND version = ?", c.UID, c.GameID, w.Version).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrVersionConflict
		}

		// 写账本
		entry = &entities.GameCoinLedger{
			UID:          c.UID,
			GameID:       c.GameID,
			Delta:        c.Delta,
			BalanceAfter: newBalance,
			Source:       c.Source,
			RefID:        c.RefID,
			Memo:         c.Memo,
		}
		entry.SetIdempotencyKey(c.IdempotencyKey)
		return tx.Create(entry).Error
	})

	if errors.Is(err, ErrDuplicateLedger) {
		return newBalance, entry, ErrDuplicateLedger
	}
	if err != nil {
		return 0, nil, err
	}
	return newBalance, entry, nil
}

// ApplyChangeWithTx 在给定事务内变更游戏币（供跨表事务使用）。
func (r *GameCoinRepository) ApplyChangeWithTx(ctx context.Context, tx *gorm.DB, c GameCoinChange) (int64, *entities.GameCoinLedger, error) {
	if c.Delta == 0 {
		return 0, nil, errors.New("delta must not be zero")
	}

	// 幂等检查
	if c.IdempotencyKey != "" {
		var existing entities.GameCoinLedger
		err := tx.WithContext(ctx).Where("idempotency_key = ?", c.IdempotencyKey).First(&existing).Error
		if err == nil {
			return existing.BalanceAfter, &existing, ErrDuplicateLedger
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil, err
		}
	}

	// 行锁
	var w entities.GameWallet
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("uid = ? AND game_id = ?", c.UID, c.GameID).
		First(&w).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil, err
		}
		w = entities.GameWallet{UID: c.UID, GameID: c.GameID}
		if err := tx.WithContext(ctx).Create(&w).Error; err != nil {
			return 0, nil, err
		}
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("uid = ? AND game_id = ?", c.UID, c.GameID).
			First(&w).Error; err != nil {
			return 0, nil, err
		}
	}

	newBalance := w.Balance + c.Delta
	if newBalance < 0 && !c.AllowNegative {
		return 0, nil, ErrGameCoinInsufficient
	}

	updates := map[string]interface{}{
		"balance": newBalance,
		"version": w.Version + 1,
	}
	if c.Delta > 0 {
		updates["total_earned"] = w.TotalEarned + c.Delta
	} else {
		updates["total_spent"] = w.TotalSpent - c.Delta
	}

	res := tx.WithContext(ctx).Model(&entities.GameWallet{}).
		Where("uid = ? AND game_id = ? AND version = ?", c.UID, c.GameID, w.Version).
		Updates(updates)
	if res.Error != nil {
		return 0, nil, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, nil, ErrVersionConflict
	}

	entry := &entities.GameCoinLedger{
		UID:          c.UID,
		GameID:       c.GameID,
		Delta:        c.Delta,
		BalanceAfter: newBalance,
		Source:       c.Source,
		RefID:        c.RefID,
		Memo:         c.Memo,
	}
	entry.SetIdempotencyKey(c.IdempotencyKey)
	if err := tx.WithContext(ctx).Create(entry).Error; err != nil {
		return 0, nil, err
	}
	return newBalance, entry, nil
}

// GetWallet 查询游戏币钱包
func (r *GameCoinRepository) GetWallet(ctx context.Context, uid uint, gameID string) (*entities.GameWallet, error) {
	var w entities.GameWallet
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND game_id = ?", uid, gameID).
		First(&w).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// ListWallets 列出用户所有游戏的币钱包
func (r *GameCoinRepository) ListWallets(ctx context.Context, uid uint) ([]*entities.GameWallet, error) {
	var list []*entities.GameWallet
	err := r.DB.WithContext(ctx).Where("uid = ?", uid).Find(&list).Error
	return list, err
}

// SumByGame 按游戏汇总账本（对账用）
func (r *GameCoinRepository) SumByGame(ctx context.Context, uid uint, gameID string) (int64, error) {
	var sum int64
	err := r.DB.WithContext(ctx).Model(&entities.GameCoinLedger{}).
		Where("uid = ? AND game_id = ?", uid, gameID).
		Select("COALESCE(SUM(delta), 0)").Scan(&sum).Error
	return sum, err
}

// ListLedger 分页查询游戏币账本
func (r *GameCoinRepository) ListLedger(ctx context.Context, uid uint, gameID string, page, pageSize int) ([]*entities.GameCoinLedger, int64, error) {
	tx := r.DB.WithContext(ctx).Model(&entities.GameCoinLedger{}).Where("uid = ?", uid)
	if gameID != "" {
		tx = tx.Where("game_id = ?", gameID)
	}

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

	var list []*entities.GameCoinLedger
	err := tx.Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&list).Error
	return list, total, err
}
