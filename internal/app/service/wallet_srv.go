package service

import (
	"context"
	"errors"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"
	"tf-api/pkg/logger"

	"github.com/google/wire"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var WalletServiceSet = wire.NewSet(ProvideWalletService)

// 注册赠送积分
const RegisterGiftPoints int64 = 100

// WalletService 钱包服务：余额查询、记账、对账
type WalletService struct {
	Repo *repository.WalletRepository
}

// ProvideWalletService 构造函数
func ProvideWalletService(repo *repository.WalletRepository) *WalletService {
	return &WalletService{Repo: repo}
}

// InitWallet 初始化钱包（注册时调用），含注册赠送
func (s *WalletService) InitWallet(ctx context.Context, uid uint) error {
	w, err := s.Repo.GetOrCreateWallet(ctx, uid)
	if err != nil {
		return err
	}
	if w == nil {
		return apperrors.WithCode(apperrors.WalletNotExist)
	}

	// 注册赠送（幂等：用 idempotency key 保证只发一次）
	if RegisterGiftPoints > 0 {
		_, _, err := s.Repo.ApplyChange(ctx, repository.BalanceChange{
			UID:            uid,
			Currency:       entities.CurrencyPoints,
			Delta:          RegisterGiftPoints,
			Source:         entities.SourceRegisterGift,
			Memo:           "注册赠送",
			IdempotencyKey: "register_gift:" + itoa(uid),
		})
		if err != nil && !errors.Is(err, repository.ErrDuplicateLedger) {
			logger.ZWarn("register gift failed", zap.Uint("uid", uid), zap.Error(err))
			// 赠送失败不影响注册
		}
	}
	return nil
}

// GetWallet 查询钱包（不存在则创建）
func (s *WalletService) GetWallet(ctx context.Context, uid uint) (*entities.WalletInfo, error) {
	w, err := s.Repo.GetOrCreateWallet(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if w == nil {
		return nil, apperrors.WithCode(apperrors.WalletNotExist)
	}
	return &entities.WalletInfo{
		UID:     w.UID,
		Points:  w.Points,
		Coupon:  w.Coupon,
		Diamond: w.Diamond,
	}, nil
}

// GetWalletWithTx 事务内查询钱包（不存在则创建）。
//
// ⚠️ 事务内**必须**用这个方法：GetWallet 走的是外层 DB 句柄，
// 会向连接池申请第二条连接（第一条已被事务占用），连接池打满时死锁。
// 这个坑本项目在回档逻辑上踩过一次（测试挂死 600 秒），别重复。
func (s *WalletService) GetWalletWithTx(ctx context.Context, tx *gorm.DB, uid uint) (*entities.Wallet, error) {
	w, err := s.Repo.GetOrCreateWalletWithTx(ctx, tx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	return w, nil
}

// Balance 查询某币种余额
func (s *WalletService) Balance(ctx context.Context, uid uint, currency entities.Currency) (int64, error) {
	w, err := s.Repo.GetOrCreateWallet(ctx, uid)
	if err != nil || w == nil {
		return 0, apperrors.WithError(err)
	}
	switch currency {
	case entities.CurrencyPoints:
		return w.Points, nil
	case entities.CurrencyCoupon:
		return w.Coupon, nil
	case entities.CurrencyDiamond:
		return w.Diamond, nil
	}
	return 0, apperrors.WithCode(apperrors.CurrencyNotSupported)
}

// Change 变更余额（统一入口）。
//
// 所有发币/扣币都必须走这里，不要直接操作 Repository。
func (s *WalletService) Change(ctx context.Context, c repository.BalanceChange) (int64, *entities.LedgerEntry, error) {
	balance, entry, err := s.Repo.ApplyChange(ctx, c)
	switch {
	case errors.Is(err, repository.ErrInsufficientBalance):
		return balance, entry, apperrors.WithCode(apperrors.InsufficientBalance)
	case errors.Is(err, repository.ErrDuplicateLedger):
		return balance, entry, apperrors.WithCode(apperrors.DuplicateLedgerEntry)
	case err != nil:
		return balance, entry, apperrors.WithError(err)
	}
	return balance, entry, nil
}

// ChangeWithTx 在给定事务内变更余额（供跨表事务使用）。
//
// 用于「扣积分 + 记游戏币」这类必须原子完成的场景。
func (s *WalletService) ChangeWithTx(ctx context.Context, tx *gorm.DB, c repository.BalanceChange) (int64, *entities.LedgerEntry, error) {
	balance, entry, err := s.Repo.ApplyChangeWithTx(ctx, tx, c)
	switch {
	case errors.Is(err, repository.ErrInsufficientBalance):
		return balance, entry, apperrors.WithCode(apperrors.InsufficientBalance)
	case errors.Is(err, repository.ErrDuplicateLedger):
		return balance, entry, apperrors.WithCode(apperrors.DuplicateLedgerEntry)
	case err != nil:
		return balance, entry, apperrors.WithError(err)
	}
	return balance, entry, nil
}

// ListLedger 查询账本
func (s *WalletService) ListLedger(ctx context.Context, uid uint, req *entities.LedgerListReq) error {
	if err := s.Repo.ListLedger(ctx, req, uid); err != nil {
		return apperrors.WithError(err)
	}
	return nil
}

// Reconcile 对账：校验钱包余额是否等于账本 delta 之和。
//
// 返回 (钱包余额, 账本汇总, 是否一致)。
// 用于定时巡检与排查「钱对不上」的问题。
func (s *WalletService) Reconcile(ctx context.Context, uid uint) (map[entities.Currency][2]int64, bool, error) {
	w, err := s.Repo.GetWallet(ctx, uid)
	if err != nil {
		return nil, false, apperrors.WithError(err)
	}
	if w == nil {
		return nil, false, apperrors.WithCode(apperrors.WalletNotExist)
	}

	result := make(map[entities.Currency][2]int64, 3)
	ok := true

	for _, cur := range []entities.Currency{
		entities.CurrencyPoints,
		entities.CurrencyCoupon,
		entities.CurrencyDiamond,
	} {
		sum, err := s.Repo.SumByCurrency(ctx, uid, cur)
		if err != nil {
			return nil, false, apperrors.WithError(err)
		}

		var walletBalance int64
		switch cur {
		case entities.CurrencyPoints:
			walletBalance = w.Points
		case entities.CurrencyCoupon:
			walletBalance = w.Coupon
		case entities.CurrencyDiamond:
			walletBalance = w.Diamond
		}

		result[cur] = [2]int64{walletBalance, sum}
		if walletBalance != sum {
			ok = false
			logger.ZError("wallet reconcile mismatch",
				zap.Uint("uid", uid),
				zap.String("currency", string(cur)),
				zap.Int64("wallet", walletBalance),
				zap.Int64("ledger", sum))
		}
	}
	return result, ok, nil
}

// itoa 无依赖的 uint 转字符串
func itoa(n uint) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
