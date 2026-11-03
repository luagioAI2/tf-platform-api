package service

import (
	"context"
	"errors"
	"fmt"

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

// SpendPoints 扣积分（插件游戏开局这类「游戏内消费」）。
//
// ## 为什么在线版必须走服务端
//
// 在线版的积分以服务端为准（余额 = 服务端流水之和）。插件游戏扣分原本
// 只写本地账本，那会**静默漂移**：本地多一条 `lg_` 行、服务端没有，
// 而拉取只清 `srv_` 行 —— 那条本地行永远留着，余额就此对不上，
// 界面上什么都看不出来，只会某天发现「妈妈的手机比孩子多 30 分」。
//
// ## 只认 `game_spend`
//
// 放开通用的 `manual_adjust` 等于给了客户端「任意改写流水来源」的能力，
// 而来源是家长看流水时的唯一线索。多一种来源就多一处要维护的口径，
// 所以这里**白名单只有一个值**。
//
// 幂等键只在客户端给了才拼 —— 拼成常量会让第二次调用撞唯一索引，
// 表现成「玩不了第二局」。
func (s *WalletService) SpendPoints(ctx context.Context, uid uint, req *entities.WalletSpendReq) (*entities.WalletInfo, error) {
	source := req.Source
	if source == "" {
		source = entities.SourceGameSpend
	}
	if source != entities.SourceGameSpend {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}

	key := ""
	if req.ClientRequestID != "" {
		key = "wallet_spend:" + req.ClientRequestID
	}

	_, _, err := s.Change(ctx, repository.BalanceChange{
		UID:            uid,
		Currency:       entities.CurrencyPoints,
		Delta:          -req.Amount,
		Source:         source,
		RefID:          req.RefID,
		Memo:           req.Memo,
		IdempotencyKey: key,
	})
	// 重试（同一个 request_id）会撞幂等键 —— 那笔其实已经成功了，
	// 不该让调用方以为「没扣成」而再扣一次。所以按成功处理。
	if err != nil && !apperrors.Is(err, apperrors.DuplicateLedgerEntry) {
		return nil, err
	}

	w, err := s.GetWallet(ctx, uid)
	if err != nil {
		return nil, err
	}
	return w, nil
}

// ImportLegacy 把「本地版留下的余额」一次性结转进这个账号。
//
// ## 三种币一个事务
//
// 分开提交的话，中途失败会留下「积分搬了、兑换券没搬」，而客户端那边一旦删掉
// 本机旧行，兑换券就真的没了。一个事务要么全成、要么全不成。
//
// ## 幂等按币种算
//
// 幂等键里带币种 ⇒ 重试时已经写过的那个币种命中、没写过的照常写。这比
// 「整单一个键」宽容，也比「整单不加键」安全：同一次重试不会写两遍。
//
// ## 为什么不校验「账号里已经有余额了」
//
// 那会把「家长先在这台设备上注册过、孩子又在别的设备上玩过」这种正常情况
// 挡掉。这里只回答一个问题：「这份本机旧账搬上去了没有」—— 搬过就跳过，
// 没搬过就写。客户端负责只在一台设备上算一次载荷（见 `online/legacy.ts`）。
func (s *WalletService) ImportLegacy(ctx context.Context, uid uint, req *entities.WalletImportReq) (*entities.WalletImportResp, error) {
	if req.Points+req.Coupons+req.Diamonds <= 0 {
		return nil, apperrors.WithCode(apperrors.ImportPayloadInvalid)
	}

	type item struct {
		cur   entities.Currency
		delta int64
		count int64
		name  string
	}
	items := []item{
		{entities.CurrencyPoints, req.Points, req.Counts["points"], "积分"},
		{entities.CurrencyCoupon, req.Coupons, req.Counts["coupons"], "兑换券"},
		{entities.CurrencyDiamond, req.Diamonds, req.Counts["diamonds"], "钻石"},
	}

	resp := &entities.WalletImportResp{}
	err := s.Repo.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, it := range items {
			if it.delta <= 0 {
				continue
			}
			memo := fmt.Sprintf("从本地版结转 %s %d", it.name, it.delta)
			if it.count > 0 {
				memo = fmt.Sprintf("从本地版结转 %s %d（%d 笔）", it.name, it.delta, it.count)
			}
			_, _, err := s.Repo.ApplyChangeWithTx(ctx, tx, repository.BalanceChange{
				UID:            uid,
				Currency:       it.cur,
				Delta:          it.delta,
				Source:         entities.SourceLegacyImport,
				Memo:           memo,
				IdempotencyKey: fmt.Sprintf("legacy_import:%d:%s:%s", uid, req.Key, it.cur),
			})
			if errors.Is(err, repository.ErrDuplicateLedger) {
				// 这个币种之前已经搬过了 —— 正常重试，不是错误
				continue
			}
			if err != nil {
				return err
			}
			resp.Imported = true
			switch it.cur {
			case entities.CurrencyPoints:
				resp.Points = it.delta
			case entities.CurrencyCoupon:
				resp.Coupons = it.delta
			case entities.CurrencyDiamond:
				resp.Diamonds = it.delta
			}
		}
		return nil
	})
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	w, err := s.GetWallet(ctx, uid)
	if err != nil {
		return nil, err
	}
	resp.Wallet = w
	return resp, nil
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
