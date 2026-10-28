package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"
	"tf-api/pkg/logger"

	"github.com/google/wire"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var GameServiceSet = wire.NewSet(ProvideGameService)

// GameService 游戏商城服务：列表、详情、购买
type GameService struct {
	GameRepo   *repository.GameRepository
	WalletSrv  *WalletService
	WalletRepo *repository.WalletRepository
	DB         *gorm.DB
}

// ProvideGameService 构造函数
func ProvideGameService(
	gameRepo *repository.GameRepository,
	walletSrv *WalletService,
	walletRepo *repository.WalletRepository,
	db *gorm.DB,
) *GameService {
	return &GameService{
		GameRepo:   gameRepo,
		WalletSrv:  walletSrv,
		WalletRepo: walletRepo,
		DB:         db,
	}
}

// List 游戏列表（含拥有状态与可购买性）
func (s *GameService) List(ctx context.Context, uid uint, req *entities.GameListReq) ([]*entities.GameItem, int64, error) {
	games, total, err := s.GameRepo.ListGames(ctx, req)
	if err != nil {
		return nil, 0, apperrors.WithError(err)
	}

	// 一次性取出用户已拥有的游戏（避免 N+1）
	ownedSet := make(map[string]struct{})
	if uid > 0 {
		ids, err := s.GameRepo.ListOwnedGameIDs(ctx, uid)
		if err != nil {
			return nil, 0, apperrors.WithError(err)
		}
		for _, id := range ids {
			ownedSet[id] = struct{}{}
		}
	}

	// 余额（用于判断 can_afford）
	var points, diamond int64
	if uid > 0 {
		if w, err := s.WalletSrv.GetWallet(ctx, uid); err == nil && w != nil {
			points, diamond = w.Points, w.Diamond
		}
	}

	items := make([]*entities.GameItem, 0, len(games))
	for _, g := range games {
		_, owned := ownedSet[g.GameID]
		// 默认拥有的游戏也算已拥有
		if g.DefaultOwned {
			owned = true
		}
		if req.OnlyOwned && !owned {
			continue
		}
		items = append(items, s.toItem(g, owned, points, diamond))
	}

	// OnlyOwned 时 total 需要修正
	if req.OnlyOwned {
		total = int64(len(items))
	}
	return items, total, nil
}

// Detail 游戏详情
func (s *GameService) Detail(ctx context.Context, uid uint, gameID string) (*entities.GameDetail, error) {
	g, err := s.GameRepo.GetByGameID(ctx, gameID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if g == nil {
		return nil, apperrors.WithCode(apperrors.GameNotExist)
	}
	if g.Status == entities.GameStatusOffline {
		return nil, apperrors.WithCode(apperrors.GameOffline)
	}

	owned := g.DefaultOwned
	if uid > 0 {
		if has, err := s.GameRepo.HasGame(ctx, uid, gameID); err == nil {
			owned = owned || has
		}
	}

	var points, diamond int64
	if uid > 0 {
		if w, err := s.WalletSrv.GetWallet(ctx, uid); err == nil && w != nil {
			points, diamond = w.Points, w.Diamond
		}
	}

	item := s.toItem(g, owned, points, diamond)
	return &entities.GameDetail{
		GameItem: *item,
		Learn:    []string(g.Learn),
		About:    []string(g.About),
		Shots:    []entities.Shot(g.Shots),
		EntryURL: g.EntryURL,
	}, nil
}

// toItem 转换为列表项
func (s *GameService) toItem(g *entities.Game, owned bool, points, diamond int64) *entities.GameItem {
	canAfford := owned
	if !owned {
		switch {
		case g.PricePoints > 0:
			canAfford = points >= g.PricePoints
		case g.PriceDiamond > 0:
			canAfford = diamond >= g.PriceDiamond
		default:
			canAfford = true // 免费
		}
	}
	return &entities.GameItem{
		GameID:       g.GameID,
		Name:         g.Name,
		Emoji:        g.Emoji,
		Tagline:      g.Tagline,
		Kind:         g.Kind,
		Tint:         g.Tint,
		Status:       g.Status,
		CoverURL:     g.CoverURL,
		AgeHint:      g.AgeHint,
		SessionHint:  g.SessionHint,
		PricePoints:  g.PricePoints,
		PriceDiamond: g.PriceDiamond,
		Owned:        owned,
		CanAfford:    canAfford,
	}
}

// Purchase 购买游戏。
//
// 事务边界（关键）：
//  1. 幂等检查（客户端 request_id / 已拥有）
//  2. 校验游戏存在且可购买
//  3. 扣款（走 WalletService.Change，内部有行锁）
//  4. 授予游戏
//  5. 创建订单
//
// ⚠️ 扣款和授予必须在同一个事务里，否则会出现「扣了钱没给游戏」。
// 这里用「先扣款（独立事务）→ 授予（失败则退款）」的补偿模式，
// 因为 WalletService 内部已经开了事务，嵌套事务在 GORM 里需要 SavePoint，
// 补偿模式更简单可靠，且失败时账本有完整记录可追溯。
func (s *GameService) Purchase(ctx context.Context, uid uint, req *entities.PurchaseGameReq) (*entities.PurchaseGameResp, error) {
	// 1. 客户端幂等：同一 request_id 直接返回上次结果
	if req.ClientRequestID != "" {
		existing, err := s.GameRepo.GetOrderByClientRequestID(ctx, uid, req.ClientRequestID)
		if err != nil {
			return nil, apperrors.WithError(err)
		}
		if existing != nil {
			wallet, _ := s.WalletSrv.GetWallet(ctx, uid)
			return &entities.PurchaseGameResp{
				OrderID:      existing.OrderID,
				GameID:       existing.GameID,
				Currency:     existing.Currency,
				Amount:       existing.Amount,
				Wallet:       derefWallet(wallet),
				AlreadyOwned: true,
			}, nil
		}
	}

	// 2. 校验游戏
	g, err := s.GameRepo.GetByGameID(ctx, req.GameID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if g == nil {
		return nil, apperrors.WithCode(apperrors.GameNotExist)
	}
	if g.Status != entities.GameStatusAvailable {
		return nil, apperrors.WithCode(apperrors.GameNotPurchasable)
	}

	// 3. 已拥有则幂等返回
	has, err := s.GameRepo.HasGame(ctx, uid, req.GameID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if has || g.DefaultOwned {
		wallet, _ := s.WalletSrv.GetWallet(ctx, uid)
		return &entities.PurchaseGameResp{
			GameID:       g.GameID,
			Wallet:       derefWallet(wallet),
			AlreadyOwned: true,
		}, nil
	}

	// 4. 确定支付币种与金额
	currency, amount, err := resolvePayment(g, req.Currency)
	if err != nil {
		return nil, err
	}

	orderID := generateOrderID(uid)

	// 5. 事务：扣款 + 授予 + 订单，三者必须原子完成
	//
	// 为什么不能像最初那样「先扣款再授予，失败补偿退款」：
	// 补偿会在账本里留下「扣款 + 退款」两条记录，虽然资金没丢，
	// 但用户看到流水会困惑，对账也复杂。真正的原子事务更干净。
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 5.1 扣款（同一事务内）
		if amount > 0 {
			_, _, err := s.WalletRepo.ApplyChangeWithTx(ctx, tx, repository.BalanceChange{
				UID:            uid,
				Currency:       currency,
				Delta:          -amount,
				Source:         entities.SourceGamePurchase,
				RefID:          g.GameID,
				Memo:           "购买游戏：" + g.Name,
				IdempotencyKey: "game_purchase:" + orderID,
			})
			if err != nil {
				return err
			}
		}

		// 5.2 授予游戏
		grant := &entities.UserGame{
			UID:       uid,
			GameID:    g.GameID,
			Source:    entities.GameOwnedByPurchase,
			PricePaid: amount,
			Currency:  currency,
			OrderID:   orderID,
		}
		if err := s.GameRepo.GrantGameWithTx(tx, grant); err != nil {
			return err
		}

		// 5.3 创建订单
		order := &entities.GameOrder{
			OrderID:         orderID,
			UID:             uid,
			GameID:          g.GameID,
			Currency:        currency,
			Amount:          amount,
			Status:          entities.OrderStatusPaid,
			ClientRequestID: req.ClientRequestID,
		}
		return s.GameRepo.CreateOrderWithTx(tx, order)
	})

	if err != nil {
		switch {
		case errors.Is(err, repository.ErrInsufficientBalance):
			return nil, apperrors.WithCode(apperrors.InsufficientBalance)
		case errors.Is(err, repository.ErrDuplicateLedger):
			return nil, apperrors.WithCode(apperrors.DuplicateOperation)
		}
		logger.ZError("purchase transaction failed",
			zap.Uint("uid", uid),
			zap.String("game_id", g.GameID),
			zap.Error(err))
		return nil, apperrors.WithError(err)
	}

	wallet, _ := s.WalletSrv.GetWallet(ctx, uid)
	return &entities.PurchaseGameResp{
		OrderID:  orderID,
		GameID:   g.GameID,
		Currency: currency,
		Amount:   amount,
		Wallet:   derefWallet(wallet),
	}, nil
}

// resolvePayment 决定用哪种币支付、付多少
func resolvePayment(g *entities.Game, preferred entities.Currency) (entities.Currency, int64, error) {
	// 指定了币种：校验该币种可用
	if preferred != "" {
		switch preferred {
		case entities.CurrencyPoints:
			if g.PricePoints <= 0 {
				return "", 0, apperrors.WithCode(apperrors.GameNotPurchasable)
			}
			return entities.CurrencyPoints, g.PricePoints, nil
		case entities.CurrencyDiamond:
			if g.PriceDiamond <= 0 {
				return "", 0, apperrors.WithCode(apperrors.GameNotPurchasable)
			}
			return entities.CurrencyDiamond, g.PriceDiamond, nil
		default:
			return "", 0, apperrors.WithCode(apperrors.CurrencyNotSupported)
		}
	}

	// 未指定：优先钻石（更"贵"的币），再积分，最后免费
	if g.PriceDiamond > 0 {
		return entities.CurrencyDiamond, g.PriceDiamond, nil
	}
	if g.PricePoints > 0 {
		return entities.CurrencyPoints, g.PricePoints, nil
	}
	return entities.CurrencyPoints, 0, nil // 免费
}

// MyGames 我拥有的游戏列表
func (s *GameService) MyGames(ctx context.Context, uid uint) ([]*entities.GameItem, error) {
	ids, err := s.GameRepo.ListOwnedGameIDs(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	games, err := s.GameRepo.ListByGameIDs(ctx, ids)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	// 加上默认拥有的游戏
	all, _, err := s.GameRepo.ListGames(ctx, &entities.GameListReq{})
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	seen := make(map[string]struct{}, len(games))
	items := make([]*entities.GameItem, 0, len(games)+1)
	for _, g := range games {
		seen[g.GameID] = struct{}{}
		items = append(items, s.toItem(g, true, 0, 0))
	}
	for _, g := range all {
		if !g.DefaultOwned {
			continue
		}
		if _, ok := seen[g.GameID]; ok {
			continue
		}
		items = append(items, s.toItem(g, true, 0, 0))
	}
	return items, nil
}

// generateOrderID 生成订单号：G + 时间戳 + uid 后 4 位
func generateOrderID(uid uint) string {
	return fmt.Sprintf("G%d%04d%04d", time.Now().Unix(), uid%10000, time.Now().Nanosecond()%10000)
}

// derefWallet 安全解引用钱包
func derefWallet(w *entities.WalletInfo) entities.WalletInfo {
	if w == nil {
		return entities.WalletInfo{}
	}
	return *w
}

// EnsureUserGames 确保用户拥有所有 default_owned 的游戏（登录后调用）
func (s *GameService) EnsureUserGames(ctx context.Context, uid uint) error {
	all, _, err := s.GameRepo.ListGames(ctx, &entities.GameListReq{})
	if err != nil {
		return apperrors.WithError(err)
	}
	for _, g := range all {
		if !g.DefaultOwned {
			continue
		}
		grant := &entities.UserGame{
			UID:    uid,
			GameID: g.GameID,
			Source: entities.GameOwnedByDefault,
		}
		if err := s.GameRepo.GrantGame(ctx, grant); err != nil {
			logger.ZWarn("grant default game failed",
				zap.Uint("uid", uid), zap.String("game_id", g.GameID), zap.Error(err))
		}
	}
	return nil
}

// 确保 errors 包被使用（补偿逻辑里用到）
var _ = errors.Is
