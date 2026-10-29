package repository

import (
	"context"
	"errors"

	"tf-api/internal/app/entities"

	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var WalletRepositorySet = wire.NewSet(wire.Struct(new(WalletRepository), "*"))

// WalletRepository 钱包与账本数据访问。
//
// ⚠️ 所有余额变动必须走 `ApplyChange`，它在一个事务内：
//  1. 用行锁（SELECT ... FOR UPDATE）锁住钱包行
//  2. 校验余额充足
//  3. 写账本
//  4. 更新余额 + version
//
// 不要绕过它直接 Update("points", ...)，否则并发下会丢钱。
type WalletRepository struct {
	DB  *gorm.DB
	RDS *redis.Client
}

// NewWalletRepository 直接构造（供 seed / 脚本使用，wire 走 struct provider）
func NewWalletRepository(db *gorm.DB) *WalletRepository {
	return &WalletRepository{DB: db}
}

// CreateWallet 创建钱包
func (r *WalletRepository) CreateWallet(ctx context.Context, w *entities.Wallet) error {
	return r.DB.WithContext(ctx).Create(w).Error
}

// GetWallet 查询钱包
func (r *WalletRepository) GetWallet(ctx context.Context, uid uint) (*entities.Wallet, error) {
	var w entities.Wallet
	err := r.DB.WithContext(ctx).Where("uid = ?", uid).First(&w).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// GetOrCreateWallet 查询钱包，不存在则创建（幂等）
func (r *WalletRepository) GetOrCreateWallet(ctx context.Context, uid uint) (*entities.Wallet, error) {
	w, err := r.GetWallet(ctx, uid)
	if err != nil || w != nil {
		return w, err
	}
	w = &entities.Wallet{UID: uid}
	if err := r.DB.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(w).Error; err != nil {
		return nil, err
	}
	// 并发下可能被别人先创建，重新读一次
	return r.GetWallet(ctx, uid)
}

// GetOrCreateWalletWithTx 事务内查询钱包（不存在则创建）。
//
// ⚠️ 事务内必须用这个，不能用 GetOrCreateWallet：后者走外层 DB 句柄，
// 会向连接池申请第二条连接（第一条已被事务占用），连接池打满时死锁。
func (r *WalletRepository) GetOrCreateWalletWithTx(ctx context.Context, tx *gorm.DB, uid uint) (*entities.Wallet, error) {
	var w entities.Wallet
	err := tx.WithContext(ctx).Where("uid = ?", uid).First(&w).Error
	if err == nil {
		return &w, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	w = entities.Wallet{UID: uid}
	if err := tx.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&w).Error; err != nil {
		return nil, err
	}
	// 并发下可能被别人先创建，同一事务内重新读一次
	if err := tx.WithContext(ctx).Where("uid = ?", uid).First(&w).Error; err != nil {
		return nil, err
	}
	return &w, nil
}

// SumIncomeSince 统计某币种某时间之后的**收入**合计（delta > 0）。
//
// 用途：「大约要攒几天」——只算收入，不算支出。
// 把花掉的也算进来会得出一个永远攒不到的假数。
func (r *WalletRepository) SumIncomeSince(ctx context.Context, uid uint, currency entities.Currency, since int64) (int64, error) {
	var sum int64
	err := r.DB.WithContext(ctx).Model(&entities.LedgerEntry{}).
		Where("uid = ? AND currency = ? AND delta > 0 AND created_at >= ?", uid, currency, since).
		Select("COALESCE(SUM(delta), 0)").Scan(&sum).Error
	return sum, err
}

// BalanceChange 余额变动参数
type BalanceChange struct {
	UID      uint
	Currency entities.Currency
	// Delta 变动量：正数收入，负数支出
	Delta int64
	// Source 来源
	Source string
	// RefID 关联对象
	RefID string
	// Memo 备注
	Memo string
	// IdempotencyKey 幂等键（可选，非空时重复调用只生效一次）
	IdempotencyKey string
	// AllowNegative 是否允许负余额（默认不允许）
	AllowNegative bool
}

// ErrInsufficientBalance 余额不足
var ErrInsufficientBalance = errors.New("insufficient balance")

// ErrDuplicateLedger 重复记账（幂等命中）
var ErrDuplicateLedger = errors.New("duplicate ledger entry")

// ApplyChange 原子地变更余额并记账。
//
// 返回 (变动后余额, 账本条目, error)。
// 如果 IdempotencyKey 命中已有记录，返回 (当前余额, 已有条目, ErrDuplicateLedger)。
func (r *WalletRepository) ApplyChange(ctx context.Context, c BalanceChange) (int64, *entities.LedgerEntry, error) {
	if !c.Currency.IsValid() {
		return 0, nil, errors.New("invalid currency")
	}
	if c.Delta == 0 {
		return 0, nil, errors.New("delta must not be zero")
	}

	var (
		newBalance int64
		entry      *entities.LedgerEntry
	)

	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 幂等检查：先看幂等键是否已存在
		if c.IdempotencyKey != "" {
			var existing entities.LedgerEntry
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

		// 行锁：SELECT ... FOR UPDATE，保证并发安全
		var w entities.Wallet
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("uid = ?", c.UID).
			First(&w).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// 钱包不存在则创建（新用户首次变动）
				w = entities.Wallet{UID: c.UID}
				if err := tx.Create(&w).Error; err != nil {
					return err
				}
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
					Where("uid = ?", c.UID).First(&w).Error; err != nil {
					return err
				}
			} else {
				return err
			}
		}

		// 计算新余额
		cur := w.Points
		switch c.Currency {
		case entities.CurrencyCoupon:
			cur = w.Coupon
		case entities.CurrencyDiamond:
			cur = w.Diamond
		}

		newBalance = cur + c.Delta
		if newBalance < 0 && !c.AllowNegative {
			return ErrInsufficientBalance
		}

		// 更新余额（带 version 乐观锁兜底）
		col := "points"
		switch c.Currency {
		case entities.CurrencyCoupon:
			col = "coupon"
		case entities.CurrencyDiamond:
			col = "diamond"
		}
		res := tx.Model(&entities.Wallet{}).
			Where("uid = ? AND version = ?", c.UID, w.Version).
			Updates(map[string]interface{}{
				col:       newBalance,
				"version": w.Version + 1,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errors.New("wallet version conflict, please retry")
		}

		// 写账本
		entry = &entities.LedgerEntry{
			UID:          c.UID,
			Currency:     c.Currency,
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

// ApplyChangeWithTx 在给定事务内变更余额并记账（供跨表事务使用）。
//
// 与 ApplyChange 的区别：不自己开事务，由调用方控制事务边界。
// 用于「扣款 + 授予 + 订单」必须原子完成的场景（如购买游戏）。
func (r *WalletRepository) ApplyChangeWithTx(ctx context.Context, tx *gorm.DB, c BalanceChange) (int64, *entities.LedgerEntry, error) {
	if !c.Currency.IsValid() {
		return 0, nil, errors.New("invalid currency")
	}
	if c.Delta == 0 {
		return 0, nil, errors.New("delta must not be zero")
	}

	// 幂等检查
	if c.IdempotencyKey != "" {
		var existing entities.LedgerEntry
		err := tx.WithContext(ctx).Where("idempotency_key = ?", c.IdempotencyKey).First(&existing).Error
		if err == nil {
			return existing.BalanceAfter, &existing, ErrDuplicateLedger
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil, err
		}
	}

	// 行锁
	var w entities.Wallet
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("uid = ?", c.UID).First(&w).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil, err
		}
		w = entities.Wallet{UID: c.UID}
		if err := tx.WithContext(ctx).Create(&w).Error; err != nil {
			return 0, nil, err
		}
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("uid = ?", c.UID).First(&w).Error; err != nil {
			return 0, nil, err
		}
	}

	cur := w.Points
	col := "points"
	switch c.Currency {
	case entities.CurrencyCoupon:
		cur, col = w.Coupon, "coupon"
	case entities.CurrencyDiamond:
		cur, col = w.Diamond, "diamond"
	}

	newBalance := cur + c.Delta
	if newBalance < 0 && !c.AllowNegative {
		return 0, nil, ErrInsufficientBalance
	}

	res := tx.WithContext(ctx).Model(&entities.Wallet{}).
		Where("uid = ? AND version = ?", c.UID, w.Version).
		Updates(map[string]interface{}{
			col:       newBalance,
			"version": w.Version + 1,
		})
	if res.Error != nil {
		return 0, nil, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, nil, errors.New("wallet version conflict, please retry")
	}

	entry := &entities.LedgerEntry{
		UID:          c.UID,
		Currency:     c.Currency,
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

// SumByCurrency 按币种汇总账本（对账用，不要用于读余额）
func (r *WalletRepository) SumByCurrency(ctx context.Context, uid uint, currency entities.Currency) (int64, error) {
	var sum int64
	err := r.DB.WithContext(ctx).Model(&entities.LedgerEntry{}).
		Where("uid = ? AND currency = ?", uid, currency).
		Select("COALESCE(SUM(delta), 0)").
		Scan(&sum).Error
	return sum, err
}

// ListLedger 分页查询账本
func (r *WalletRepository) ListLedger(ctx context.Context, req *entities.LedgerListReq, uid uint) error {
	tx := r.DB.WithContext(ctx).Model(&entities.LedgerEntry{}).Where("uid = ?", uid)

	if req.Currency != "" {
		tx = tx.Where("currency = ?", req.Currency)
	}
	if req.Source != "" {
		tx = tx.Where("source = ?", req.Source)
	}

	items := make([]*entities.LedgerItem, 0)
	req.List = &items
	tx = tx.Order("id DESC")

	// 先算总数
	if err := tx.Count(&req.Count).Error; err != nil {
		return err
	}

	req.Normalize()
	return tx.Select(
		"id, currency, delta, balance_after, source, ref_id, memo, created_at",
	).Offset(req.Offset()).Limit(req.PageSize).Scan(&items).Error
}

// GetLedgerByRefID 按关联 ID 查询账本（对账/排查用）
func (r *WalletRepository) GetLedgerByRefID(ctx context.Context, uid uint, refID string) ([]*entities.LedgerEntry, error) {
	var list []*entities.LedgerEntry
	err := r.DB.WithContext(ctx).
		Where("uid = ? AND ref_id = ?", uid, refID).
		Order("id ASC").Find(&list).Error
	return list, err
}
