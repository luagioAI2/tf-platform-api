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

var GameCoinServiceSet = wire.NewSet(ProvideGameCoinService)

// 默认兑换率（积分 : 游戏币）
const DefaultPointsPerCoin int64 = 10

// GameCoinService 游戏币服务。
//
// 经济链（与前端一致，不可逆）：
//
//	积分 ──Buy──▶ 游戏币 ──Redeem──▶ 兑换券
//	                 ▲
//	                 └──Mint── 插件铸币（只进不出）
//
// 三条不变量：
//  1. 游戏币不能换回积分
//  2. 兑换券不能换回积分
//  3. 一局期望产出 × 汇率 < 一局成本（由游戏配置保证）
type GameCoinService struct {
	Repo      *repository.GameCoinRepository
	GameRepo  *repository.GameRepository
	WalletSrv *WalletService
	DB        *gorm.DB
}

// ProvideGameCoinService 构造函数
func ProvideGameCoinService(
	repo *repository.GameCoinRepository,
	gameRepo *repository.GameRepository,
	walletSrv *WalletService,
	db *gorm.DB,
) *GameCoinService {
	return &GameCoinService{
		Repo:      repo,
		GameRepo:  gameRepo,
		WalletSrv: walletSrv,
		DB:        db,
	}
}

// Info 查询游戏币余额
func (s *GameCoinService) Info(ctx context.Context, uid uint, gameID string) (*entities.GameCoinInfo, error) {
	w, err := s.Repo.GetWallet(ctx, uid, gameID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if w == nil {
		return &entities.GameCoinInfo{GameID: gameID}, nil
	}
	return &entities.GameCoinInfo{
		GameID:      w.GameID,
		Balance:     w.Balance,
		TotalEarned: w.TotalEarned,
		TotalSpent:  w.TotalSpent,
	}, nil
}

// ListInfo 查询用户所有游戏的币余额
func (s *GameCoinService) ListInfo(ctx context.Context, uid uint) ([]*entities.GameCoinInfo, error) {
	wallets, err := s.Repo.ListWallets(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	out := make([]*entities.GameCoinInfo, 0, len(wallets))
	for _, w := range wallets {
		out = append(out, &entities.GameCoinInfo{
			GameID:      w.GameID,
			Balance:     w.Balance,
			TotalEarned: w.TotalEarned,
			TotalSpent:  w.TotalSpent,
		})
	}
	return out, nil
}

// idemKey 拼一个幂等键。
//
// ★ 客户端**没给** `request_id` 时返回**空串**，让账本落 NULL。
//
// 之前是直接 `"game_coin_spend:" + req.ClientRequestID` —— 客户端不传时
// 拼出来是 `"game_coin_spend:"`（一个**常量**），于是第二次调用撞上账本唯一索引，
// 被翻成「该笔记录已存在」（610）。症状是「第一次能玩，第二次开始一直说
// 已经做过了」—— 而且只发生在**不传 request_id 的客户端**身上，
// 带 id 的客户端永远看不到。2026-11-02 在 `MintVoucher` 上踩过同一类。
func idemKey(prefix, reqID string) string {
	if reqID == "" {
		return ""
	}
	return prefix + reqID
}

// Buy 用积分买游戏币。
//
// 扣积分 + 记游戏币在**同一个数据库事务**里 —— 拆开就会出现
// 「积分扣了、币没到」，而且不报错。
func (s *GameCoinService) Buy(ctx context.Context, uid uint, req *entities.GameCoinBuyReq) (*entities.GameCoinResp, error) {
	// 幂等：同一 request_id 直接返回
	if req.ClientRequestID != "" {
		if bal, ok := s.idempotentHit(ctx, uid, req.GameID, req.ClientRequestID); ok {
			return &entities.GameCoinResp{GameID: req.GameID, Balance: bal}, nil
		}
	}

	rate := req.PointsPerCoin
	if rate <= 0 {
		rate = DefaultPointsPerCoin
	}
	cost := req.Count * rate

	var newBalance int64
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 扣积分（同一事务）
		_, _, err := s.WalletSrv.ChangeWithTx(ctx, tx, repository.BalanceChange{
			UID:            uid,
			Currency:       entities.CurrencyPoints,
			Delta:          -cost,
			Source:         entities.SourceGameSpend,
			RefID:          req.GameID,
			Memo:           fmt.Sprintf("购买 %d 个游戏币（%s）", req.Count, req.GameID),
			IdempotencyKey: idemKey("game_coin_buy_points:", req.ClientRequestID),
		})
		if err != nil {
			return err
		}

		// 2. 记游戏币
		bal, _, err := s.Repo.ApplyChangeWithTx(ctx, tx, repository.GameCoinChange{
			UID:            uid,
			GameID:         req.GameID,
			Delta:          req.Count,
			Source:         entities.GameCoinSourceBuy,
			Memo:           fmt.Sprintf("购买 %d 个币", req.Count),
			IdempotencyKey: idemKey("game_coin_buy:", req.ClientRequestID),
		})
		if err != nil {
			return err
		}
		newBalance = bal
		return nil
	})
	if err != nil {
		return nil, s.mapErr(err)
	}

	return &entities.GameCoinResp{
		GameID:  req.GameID,
		Balance: newBalance,
		Amount:  req.Count,
		Points:  cost,
	}, nil
}

// Spend 消费游戏币（玩一局）
func (s *GameCoinService) Spend(ctx context.Context, uid uint, req *entities.GameCoinSpendReq) (*entities.GameCoinResp, error) {
	bal, _, err := s.Repo.ApplyChange(ctx, repository.GameCoinChange{
		UID:            uid,
		GameID:         req.GameID,
		Delta:          -req.Amount,
		Source:         entities.GameCoinSourceSpend,
		Memo:           req.Memo,
		IdempotencyKey: idemKey("game_coin_spend:", req.ClientRequestID),
	})
	if err != nil {
		return nil, s.mapErr(err)
	}
	return &entities.GameCoinResp{
		GameID:  req.GameID,
		Balance: bal,
		Amount:  -req.Amount,
	}, nil
}

// Mint 铸币（插件调用，只进不出）
func (s *GameCoinService) Mint(ctx context.Context, uid uint, req *entities.GameCoinMintReq) (*entities.GameCoinResp, error) {
	bal, _, err := s.Repo.ApplyChange(ctx, repository.GameCoinChange{
		UID:    uid,
		GameID: req.GameID,
		Delta:  req.Amount,
		Source: entities.GameCoinSourceMint,
		Memo:   req.Memo,
	})
	if err != nil {
		return nil, s.mapErr(err)
	}
	return &entities.GameCoinResp{
		GameID:  req.GameID,
		Balance: bal,
		Amount:  req.Amount,
	}, nil
}

// MintVoucher 插件抓到奖品 → 直接发兑换券（HOST_API 4 的 `mintVoucher`）。
//
// ## 为什么不走「铸币 + 换券」
//
// 两个事务、两笔账。中间崩一下会留下**真实的币**，而币能直接开局 ——
// 于是抓娃娃本身能续玩，正是要堵的那条回路；而且每卖一只娃娃账本上
// 留一对 `+n / -n`，家长看到的流水没法读。
//
// ## 为什么这条不算印钞机
//
// 兑换券 ⇸ 积分（`entities` 里那两条不可逆约束），这条链的价值上限由
// 插件 manifest 的 `economy.expectedPayout` 钉住，安装时
// `domain/pluginEconomy.ts` 会把关。**服务端不校验 manifest** ——
// 和 `/game/coin/mint` 同一个信任模型（客户端可信，没有反作弊）。
func (s *GameCoinService) MintVoucher(ctx context.Context, uid uint, req *entities.GameCoinVoucherReq) (*entities.GameCoinVoucherResp, error) {
	if req.Amount <= 0 {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}

	/* 幂等键**只在客户端给了才拼**。
	   拼成 `"game_voucher_mint:"`（常量）的话，第二次不带 request_id 的调用
	   会撞账本唯一索引 → 报「该笔记录已存在」，孩子再也拿不到券。
	   2026-11-02 在心愿兑换上踩过一模一样的事（见 wish_srv.go 的注释）。 */
	key := idemKey("game_voucher_mint:", req.ClientRequestID)

	/* 幂等命中要**当成成功**返回，不能让它走到 `ChangeWithTx` ——
	   那里会把 `ErrDuplicateLedger` 翻成业务错误，插件据此判断「没发出来」，
	   于是重试时界面上写着「没抓到」，券其实已经到账了。 */
	if key != "" {
		var existing entities.LedgerEntry
		err := s.DB.WithContext(ctx).Where("idempotency_key = ?", key).First(&existing).Error
		if err == nil {
			return &entities.GameCoinVoucherResp{
				GameID:        req.GameID,
				Vouchers:      req.Amount,
				CouponBalance: existing.BalanceAfter,
			}, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.WithError(err)
		}
	}

	var bal int64
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		bal, _, e = s.WalletSrv.ChangeWithTx(ctx, tx, repository.BalanceChange{
			UID:            uid,
			Currency:       entities.CurrencyCoupon,
			Delta:          req.Amount,
			Source:         entities.SourcePluginMint,
			RefID:          req.GameID,
			Memo:           req.Memo,
			IdempotencyKey: key,
		})
		return e
	})
	if err != nil {
		return nil, err
	}
	return &entities.GameCoinVoucherResp{
		GameID:        req.GameID,
		Vouchers:      req.Amount,
		CouponBalance: bal,
	}, nil
}

// Redeem 游戏币换兑换券。
//
// 三件事在同一个事务里：
//  1. 读币账本并判余额够不够
//  2. 烧掉 count 个币（写负数）
//  3. 发 count × rate 张兑换券
//
// 汇率由**服务端配置**决定，不由客户端传（否则一个写错的客户端就能印券）。
func (s *GameCoinService) Redeem(ctx context.Context, uid uint, req *entities.GameCoinRedeemReq) (*entities.GameCoinResp, error) {
	// 汇率：从游戏配置读（voucherPerCoin），这里用默认 1:1
	rate := int64(1)
	g, err := s.GameRepo.GetByGameID(ctx, req.GameID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if g == nil {
		return nil, apperrors.WithCode(apperrors.GameNotExist)
	}

	vouchers := req.Count * rate
	var newBalance int64

	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 烧币
		bal, _, err := s.Repo.ApplyChangeWithTx(ctx, tx, repository.GameCoinChange{
			UID:            uid,
			GameID:         req.GameID,
			Delta:          -req.Count,
			Source:         entities.GameCoinSourceRedeem,
			Memo:           fmt.Sprintf("兑换 %d 张兑换券", vouchers),
			IdempotencyKey: idemKey("game_coin_redeem:", req.ClientRequestID),
		})
		if err != nil {
			return err
		}
		newBalance = bal

		// 2. 发兑换券
		_, _, err = s.WalletSrv.ChangeWithTx(ctx, tx, repository.BalanceChange{
			UID:            uid,
			Currency:       entities.CurrencyCoupon,
			Delta:          vouchers,
			Source:         entities.SourcePluginMint,
			RefID:          req.GameID,
			Memo:           fmt.Sprintf("游戏币兑换（%s）", req.GameID),
			IdempotencyKey: idemKey("game_coin_redeem_coupon:", req.ClientRequestID),
		})
		return err
	})
	if err != nil {
		return nil, s.mapErr(err)
	}

	return &entities.GameCoinResp{
		GameID:   req.GameID,
		Balance:  newBalance,
		Amount:   -req.Count,
		Vouchers: vouchers,
	}, nil
}

// Ledger 查询游戏币账本
func (s *GameCoinService) Ledger(ctx context.Context, uid uint, gameID string, page, pageSize int) ([]*entities.GameCoinLedger, int64, error) {
	list, total, err := s.Repo.ListLedger(ctx, uid, gameID, page, pageSize)
	if err != nil {
		return nil, 0, apperrors.WithError(err)
	}
	return list, total, nil
}

// Reconcile 对账：钱包余额 vs 账本汇总
func (s *GameCoinService) Reconcile(ctx context.Context, uid uint, gameID string) (int64, int64, bool, error) {
	w, err := s.Repo.GetWallet(ctx, uid, gameID)
	if err != nil {
		return 0, 0, false, apperrors.WithError(err)
	}
	var walletBalance int64
	if w != nil {
		walletBalance = w.Balance
	}
	sum, err := s.Repo.SumByGame(ctx, uid, gameID)
	if err != nil {
		return 0, 0, false, apperrors.WithError(err)
	}
	ok := walletBalance == sum
	if !ok {
		logger.ZError("game coin reconcile mismatch",
			zap.Uint("uid", uid),
			zap.String("game_id", gameID),
			zap.Int64("wallet", walletBalance),
			zap.Int64("ledger", sum))
	}
	return walletBalance, sum, ok, nil
}

// idempotentHit 幂等命中检查
func (s *GameCoinService) idempotentHit(ctx context.Context, uid uint, gameID, reqID string) (int64, bool) {
	if reqID == "" {
		return 0, false
	}
	w, err := s.Repo.GetWallet(ctx, uid, gameID)
	if err != nil || w == nil {
		return 0, false
	}
	// 用账本查幂等键
	var count int64
	err = s.DB.WithContext(ctx).Model(&entities.GameCoinLedger{}).
		Where("idempotency_key LIKE ?", "game_coin_buy:"+reqID+"%").
		Count(&count).Error
	if err == nil && count > 0 {
		return w.Balance, true
	}
	return 0, false
}

// mapErr 把 Repository 错误映射为业务错误
func (s *GameCoinService) mapErr(err error) error {
	switch {
	case errors.Is(err, repository.ErrGameCoinInsufficient):
		return apperrors.WithCode(apperrors.GameCoinInsufficient)
	case errors.Is(err, repository.ErrInsufficientBalance):
		return apperrors.WithCode(apperrors.InsufficientBalance)
	case errors.Is(err, repository.ErrDuplicateLedger):
		return apperrors.WithCode(apperrors.DuplicateOperation)
	default:
		return apperrors.WithError(err)
	}
}
