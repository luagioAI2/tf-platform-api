package service

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"tf-api/internal/app/config"
	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"
	"tf-api/pkg/logger"

	"github.com/google/wire"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var WishServiceSet = wire.NewSet(ProvideWishService)

// WishService 心愿商城服务。
//
// ## 与兑换商城的区别
//
//   - **只收兑换券**，不混付积分
//   - 成交在 App 外（淘宝），所以券必须**真扣**
//   - 数字商品（盲盒/游戏盒子）兑换当场交付
type WishService struct {
	Repo      *repository.WishRepository
	GameRepo  *repository.GameRepository
	WalletSrv *WalletService
	EventSrv  *EventService
	// RedeemRepo 供「兑换记录」合并时间线读家里的那半（见 History）
	RedeemRepo *repository.RedeemRepository
	DB         *gorm.DB
	timezone   *time.Location
}

// ProvideWishService 构造函数
func ProvideWishService(
	repo *repository.WishRepository,
	gameRepo *repository.GameRepository,
	walletSrv *WalletService,
	eventSrv *EventService,
	redeemRepo *repository.RedeemRepository,
	db *gorm.DB,
) *WishService {
	loc, err := time.LoadLocation(config.Get().ServiceSettings.Timezone)
	if err != nil {
		loc = time.UTC
	}
	return &WishService{
		Repo:       repo,
		GameRepo:   gameRepo,
		WalletSrv:  walletSrv,
		EventSrv:   eventSrv,
		RedeemRepo: redeemRepo,
		DB:         db,
		timezone:   loc,
	}
}

// ============================ 商品列表 ============================

// ListItems 列出心愿商品（孩子视图）。
//
// 过滤与标注：
//   - 已拥有（gamebox）→ **不列出**（换不了、也没动作，纯粹占位置）
//   - 盲盒不隐藏（「这些游戏你都有啦」本身是信息）
//   - 已表明意向 / 有进行中订单 → 标出来
func (s *WishService) ListItems(ctx context.Context, uid uint, req *entities.ListWishItemReq) ([]*entities.WishItemView, error) {
	items, err := s.Repo.ListItems(ctx, req.Kind, req.IncludeArchived)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if len(items) == 0 {
		return nil, nil
	}

	// 批量取依赖数据，避免 N+1
	itemIDs := make([]string, 0, len(items))
	for _, it := range items {
		itemIDs = append(itemIDs, it.ItemID)
	}
	offers, err := s.Repo.ListOffers(ctx, uid, itemIDs)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	ownedSet, err := s.ownedGameSet(ctx, uid)
	if err != nil {
		return nil, err
	}

	wallet, err := s.WalletSrv.GetWallet(ctx, uid)
	if err != nil {
		return nil, err
	}
	var vouchers int64
	if wallet != nil {
		vouchers = wallet.Coupon
	}

	activeOrders, err := s.Repo.ListActiveOrders(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	orderByItem := make(map[string]*entities.WishOrder, len(activeOrders))
	for _, o := range activeOrders {
		if _, ok := orderByItem[o.ItemID]; !ok {
			orderByItem[o.ItemID] = o
		}
	}

	out := make([]*entities.WishItemView, 0, len(items))
	for _, it := range items {
		view := s.buildItemView(ctx, uid, it, offers[it.ItemID], ownedSet, vouchers, orderByItem[it.ItemID])

		// gamebox 已拥有 → 不列出
		if it.Kind == entities.WishKindGamebox && view.Owned {
			continue
		}
		out = append(out, view)
	}
	return out, nil
}

// Detail 商品详情（家长端，含外链）
func (s *WishService) Detail(ctx context.Context, uid uint, itemID string, isParent bool) (*entities.WishItemDetailResp, error) {
	it, err := s.Repo.GetItem(ctx, itemID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if it == nil {
		return nil, apperrors.WithCode(apperrors.WishItemNotExist)
	}
	if it.Archived {
		return nil, apperrors.WithCode(apperrors.WishItemOffline)
	}

	offer, err := s.Repo.GetOffer(ctx, uid, itemID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	ownedSet, err := s.ownedGameSet(ctx, uid)
	if err != nil {
		return nil, err
	}
	wallet, _ := s.WalletSrv.GetWallet(ctx, uid)
	var vouchers int64
	if wallet != nil {
		vouchers = wallet.Coupon
	}
	active, err := s.Repo.GetActiveOrderByItem(ctx, uid, itemID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	base := s.buildItemView(ctx, uid, it, offer, ownedSet, vouchers, active)
	resp := &entities.WishItemDetailResp{WishItemView: *base}

	// ⚠️ 外链**只在家长端返回** —— 孩子端读不到
	if isParent {
		resp.BuyURL = it.BuyURL
		resp.BuyLabel = it.BuyLabel
	}
	return resp, nil
}

// buildItemView 组装商品视图
func (s *WishService) buildItemView(
	ctx context.Context, uid uint, it *entities.WishItem,
	offer *entities.WishOffer, ownedSet map[string]struct{}, vouchers int64, active *entities.WishOrder,
) *entities.WishItemView {
	view := &entities.WishItemView{WishItem: *it}

	view.Open = offer.IsOpen()
	if view.Open {
		view.VoucherPrice = offer.VoucherPrice
	}

	// 券够不够
	if view.VoucherPrice > 0 {
		view.Affordable = vouchers >= view.VoucherPrice
		if !view.Affordable {
			view.Shortfall = view.VoucherPrice - vouchers
		}
	}

	// 拥有状态
	switch it.Kind {
	case entities.WishKindGamebox:
		if it.GameID != "" {
			_, view.Owned = ownedSet[it.GameID]
		}
	case entities.WishKindBlindbox:
		remain := entities.BlindBoxRemaining(it.Pool, keysOf(ownedSet))
		view.BlindBoxRemaining = len(remain)
		// 池子空了：不可兑换
		view.Affordable = view.Affordable && len(remain) > 0
	}

	// 进行中订单
	if active != nil {
		view.HasOrder = true
		view.OrderStatus = active.Status
		view.MyWish = true
	}

	// 上次被回绝的原因（「上次爸妈说：…」）
	if last, err := s.Repo.GetLastCancelled(ctx, uid, it.ItemID); err == nil && last != nil {
		view.LastRejectReason = last.CancelReason
	}

	// 大约要攒几天（用最近 7 天日均券收入估算）
	view.DaysToSave = -1
	if view.VoucherPrice > 0 && !view.Owned {
		perDay := s.averageVoucherIncome(ctx, uid)
		view.DaysToSave = entities.DaysToSave(view.VoucherPrice, perDay)
	}
	return view
}

// averageVoucherIncome 估算日均兑换券收入（用于「大约要攒几天」）。
//
// 取最近 7 天的**券收入合计** ÷ 7。只算收入（不含支出）——
// 孩子想知道的是「照我现在的赚钱速度，要攒几天」，
// 把他花掉的也算进来会得出一个永远攒不到的假数。
//
// 算不出来（没记录）返回 0，调用方据此显示「算不出」
// —— 显示「要攒 ∞ 天」比不显示更糟。
func (s *WishService) averageVoucherIncome(ctx context.Context, uid uint) int64 {
	const days = int64(7)
	since := time.Now().AddDate(0, 0, -int(days)).Unix()

	sum, err := s.WalletSrv.Repo.SumIncomeSince(ctx, uid, entities.CurrencyCoupon, since)
	if err != nil {
		logger.ZWarn("sum voucher income failed", zap.Uint("uid", uid), zap.Error(err))
		return 0
	}
	if sum <= 0 {
		return 0
	}
	perDay := sum / days
	if perDay < 1 {
		perDay = 1
	}
	return perDay
}

// ownedGameSet 该家庭「已拥有」的游戏 id 集合。
//
// ⚠️ 三个来源必须**都算上**，缺一个就会重复扣券：
//  1. `user_game` 表（购买/默认拥有）
//  2. 心愿单里兑换到手过的（卸载也删不掉，见 ListRedeemedGameIDs）
//  3. 默认拥有的游戏
func (s *WishService) ownedGameSet(ctx context.Context, uid uint) (map[string]struct{}, error) {
	set := make(map[string]struct{})

	ids, err := s.GameRepo.ListOwnedGameIDs(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	for _, id := range ids {
		set[id] = struct{}{}
	}

	wishIDs, err := s.Repo.ListRedeemedGameIDs(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	for _, id := range wishIDs {
		set[id] = struct{}{}
	}

	// 默认拥有的游戏
	all, _, err := s.GameRepo.ListGames(ctx, &entities.GameListReq{})
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	for _, g := range all {
		if g.DefaultOwned {
			set[g.GameID] = struct{}{}
		}
	}
	return set, nil
}

// ============================ 家长定价 ============================

// SetOffer 家长定价/开放商品。
//
// `VoucherPrice > 0` = 开放；`= 0` = 关闭（closed_at 置为当前时间）。
func (s *WishService) SetOffer(ctx context.Context, uid uint, req *entities.WishSetOfferReq) (*entities.WishOffer, error) {
	it, err := s.Repo.GetItem(ctx, req.ItemID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if it == nil {
		return nil, apperrors.WithCode(apperrors.WishItemNotExist)
	}

	now := time.Now().Unix()
	o := &entities.WishOffer{
		UID:          uid,
		ItemID:       req.ItemID,
		VoucherPrice: req.VoucherPrice,
		DiamondPrice: req.DiamondPrice,
	}
	if req.VoucherPrice > 0 {
		o.OpenedAt = now
		o.ClosedAt = 0
	} else {
		o.OpenedAt = now
		o.ClosedAt = now // 显式关闭
	}

	if err := s.Repo.UpsertOffer(ctx, o); err != nil {
		return nil, apperrors.WithError(err)
	}
	return s.Repo.GetOffer(ctx, uid, req.ItemID)
}

// ============================ 孩子许愿 ============================

// Wish 孩子表明意向（「🤍 想要这个」）。
//
// **不碰账本** —— 只是意向，随时可撤。
func (s *WishService) Wish(ctx context.Context, uid uint, req *entities.WishReq) (*entities.WishOrder, error) {
	it, err := s.Repo.GetItem(ctx, req.ItemID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if it == nil {
		return nil, apperrors.WithCode(apperrors.WishItemNotExist)
	}
	if it.Archived {
		return nil, apperrors.WithCode(apperrors.WishItemOffline)
	}

	// 已有进行中订单 → 幂等返回
	if active, err := s.Repo.GetActiveOrderByItem(ctx, uid, req.ItemID); err != nil {
		return nil, apperrors.WithError(err)
	} else if active != nil {
		return active, nil
	}

	o := &entities.WishOrder{
		UID:          uid,
		OrderID:      generateRedeemID("WO"),
		ItemID:       it.ItemID,
		Kind:         it.Kind,
		ItemName:     it.Name,
		ItemEmoji:    it.Emoji,
		PartnerName:  it.PartnerName,
		VoucherPrice: 0, // 还没定价
		Status:       entities.WishStatusWished,
		VouchersHeld: 0,
	}
	if err := s.Repo.CreateOrder(ctx, nil, o); err != nil {
		return nil, apperrors.WithError(err)
	}
	return o, nil
}

// ============================ 确定兑换（扣钱 + 交付） ============================

// Redeem 孩子确定兑换。
//
// ## 付款规则（2026-10-29 v3 起与「兑换」完全一致）
//
// 用户原话：「就跟兑换模块一样的，可以用兑换券和积分兑换」。
// 所以这里**直接委托 `entities.PlanRedeemPayment`**：
//
//	券先花 → 券不够用积分补 → 1 券 ≡ 1 积分 → 可混付
//
// ⚠️ **不要再在这里写一遍分账公式**。两处各写一遍的话，
// 改一处忘另一处时界面预览和真正扣款会算出两个数，
// 差额通常只有几分 —— 孩子的余额看起来就是「莫名其妙少了 1」。
//
// ⚠️ v2 的「只收券、不混付」**已作废**，别再按那条写校验。
//
// ## 事务边界（关键）
//
// 全部在一个事务里：
//  1. 加锁读商品 + 进行中订单
//  2. 校验：已开放、两种币加起来够、盲盒池非空、实物要收货信息
//  3. **扣钱**（券 + 积分**各写一笔账**，为 0 的那一侧不写）
//  4. 数字商品：当场交付（开盲盒 / 装包 → 授予游戏）
//  5. 状态 → `committed`
//
// ## 为什么钱必须真扣
//
// 因为**成交在 App 外面**。钱如果不扣，孩子可以同时挂 10 个「已兑换」，
// 家长每个都要去淘宝买 —— 那是灾难。
//
// ## 为什么数字商品直接到 `committed` 而不是 `done`
//
// 兑换那一刻东西已经给了（装包成功），但家长那边还没「收尾」。
// 前端用 `OpenedGameID != ""` 判断该说「已经放进你的游戏柜了」
// 还是「等爸妈去购买」——同一状态、两句话。
func (s *WishService) Redeem(ctx context.Context, uid uint, req *entities.WishRedeemReq) (*entities.WishOrderView, error) {
	var orderID string

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		it, err := s.Repo.GetItem(ctx, req.ItemID)
		if err != nil {
			return err
		}
		if it == nil {
			return apperrors.WithCode(apperrors.WishItemNotExist)
		}
		if it.Archived {
			return apperrors.WithCode(apperrors.WishItemOffline)
		}

		offer, err := s.Repo.GetOffer(ctx, uid, req.ItemID)
		if err != nil {
			return err
		}
		if !offer.IsOpen() {
			return apperrors.WithCode(apperrors.WishItemNotOpen)
		}

		// 进行中订单（wished → committed 复用它；没有就新建）
		order, err := s.Repo.GetActiveOrderByItem(ctx, uid, req.ItemID)
		if err != nil {
			return err
		}

		price := offer.VoucherPrice
		if price <= 0 {
			return apperrors.WithCode(apperrors.WishItemNotOpen)
		}

		// 实物必须有收货信息
		if !it.Kind.IsDigital() && req.Ship == nil {
			return apperrors.WithCode(apperrors.WishShipInfoRequired)
		}

		// 算分账：券优先，不够用积分补（与「兑换」同一个函数）
		wallet, err := s.WalletSrv.GetWalletWithTx(ctx, tx, uid)
		if err != nil {
			return err
		}
		var couponBal, pointsBal int64
		if wallet != nil {
			couponBal, pointsBal = wallet.Coupon, wallet.Points
		}
		plan := entities.PlanRedeemPayment(price, couponBal, pointsBal)
		if !plan.Affordable {
			// 差额是**两种币合起来**算的，文案里不带「张」
			return apperrors.WithCode(apperrors.WishVoucherShort)
		}

		// 数字商品：先算能不能交付，**不能先扣再发现交付不了**
		var openedGameID string
		if it.Kind == entities.WishKindBlindbox {
			ownedSet, err := s.ownedGameSet(ctx, uid)
			if err != nil {
				return err
			}
			pick := entities.PickBlindBox(it.Pool, keysOf(ownedSet), randomFloat())
			if pick == "" {
				return apperrors.WithCode(apperrors.WishBlindBoxEmpty)
			}
			openedGameID = pick
		} else if it.Kind == entities.WishKindGamebox {
			if it.GameID == "" {
				return apperrors.WithCode(apperrors.WishGameRequired)
			}
			openedGameID = it.GameID
		}

		// 订单号要在**扣款之前**定下来 —— 幂等键要用它。
		//
		// ⚠️ 原来这里用 `"wish_redeem:" + req.ClientRequestID`，而
		// `ClientRequestID` 允许为空 —— 于是幂等键退化成一个**常量**
		// `"wish_redeem:"`，第二次兑换必然撞账本唯一索引，
		// 报「该笔记录已存在」，孩子**再也换不了这件商品**。
		// 实测踩过：取消后想重新兑换，报 10020801。
		//
		// 用 `orderID` 才对：一个订单对每种币**最多扣一次**，
		// 而「客户端重试」的幂等由**状态 CAS** 兜着
		// （重试时订单已是 committed，`UpdateOrderCAS(from=wished)` 会失败）。
		now := time.Now().Unix()
		if order == nil {
			order = &entities.WishOrder{
				UID:         uid,
				OrderID:     generateRedeemID("WO"),
				ItemID:      it.ItemID,
				Kind:        it.Kind,
				ItemName:    it.Name,
				ItemEmoji:   it.Emoji,
				PartnerName: it.PartnerName,
			}
			if err := s.Repo.CreateOrder(ctx, tx, order); err != nil {
				return err
			}
		}
		idem := order.OrderID

		// 扣券（为 0 不写 —— 写一笔 delta 0 的账，流水里会凭空多出一条）
		if plan.CouponPaid > 0 {
			if _, _, err := s.WalletSrv.ChangeWithTx(ctx, tx, repository.BalanceChange{
				UID:            uid,
				Currency:       entities.CurrencyCoupon,
				Delta:          -plan.CouponPaid,
				Source:         entities.SourceWishRedeem,
				RefID:          it.ItemID,
				Memo:           fmt.Sprintf("心愿兑换：%s", it.Name),
				IdempotencyKey: "wish_redeem:" + idem,
			}); err != nil {
				return err
			}
		}

		// 扣积分（为 0 不写）。source 与券那侧**同名** —— 靠 Currency 区分，
		// 前端 `LedgerSource` 也是这么设计的（2026-10-29 补的两个值）。
		if plan.PointsPaid > 0 {
			if _, _, err := s.WalletSrv.ChangeWithTx(ctx, tx, repository.BalanceChange{
				UID:            uid,
				Currency:       entities.CurrencyPoints,
				Delta:          -plan.PointsPaid,
				Source:         entities.SourceWishRedeem,
				RefID:          it.ItemID,
				Memo:           fmt.Sprintf("心愿兑换：%s", it.Name),
				IdempotencyKey: "wish_redeem_points:" + idem,
			}); err != nil {
				return err
			}
		}

		orderID = order.OrderID

		// 交付数字商品（授予游戏）
		//
		// `PricePaid` 记**券那部分**（游戏用券换的语义），
		// 混付时积分部分不重复记 —— 它是同一件商品的另一半付款，
		// 记两次会让「这个游戏花了多少」翻倍。
		if openedGameID != "" {
			grant := &entities.UserGame{
				UID:       uid,
				GameID:    openedGameID,
				Source:    entities.GameOwnedByBlindbox,
				PricePaid: plan.CouponPaid,
				Currency:  entities.CurrencyCoupon,
				OrderID:   order.OrderID,
			}
			if it.Kind == entities.WishKindGamebox {
				grant.Source = entities.GameOwnedByWish
			}
			if err := s.GameRepo.GrantGameWithTx(tx, grant); err != nil {
				return err
			}
		}

		// 状态 → committed（CAS 防并发）
		//
		// ⚠️ `vouchers_held` 和 `points_held` **必须一起写** ——
		// 它们是退款凭据的两半。少写一个，取消时那半就退不回来，
		// 而且**不报错**（账本只看 delta 之和，只会表现为「孩子的钱少了」）。
		fields := map[string]interface{}{
			"status":         string(entities.WishStatusCommitted),
			"vouchers_held":  plan.CouponPaid,
			"points_held":    plan.PointsPaid,
			"voucher_price":  price,
			"opened_game_id": openedGameID,
			"committed_at":   now,
		}
		if req.Ship != nil {
			ship := *req.Ship
			ship.PurgeAfter = time.Now().AddDate(0, 0, 90).Unix()
			fields["ship"] = ship
		}

		rows, err := s.Repo.UpdateOrderCAS(ctx, tx, uid, order.OrderID, order.Status, fields)
		if err != nil {
			return err
		}
		if rows == 0 {
			return apperrors.WithCode(apperrors.WishStatusNotAllowed)
		}
		return nil
	})

	if err != nil {
		if apperrors.IsBizError(err) {
			return nil, err
		}
		logger.ZError("wish redeem failed", zap.Uint("uid", uid), zap.String("item_id", req.ItemID), zap.Error(err))
		return nil, apperrors.WithError(err)
	}

	s.EventSrv.Publish(WishOrderChangedEvent, map[string]interface{}{
		"uid": uid, "order_id": orderID, "status": string(entities.WishStatusCommitted),
	})

	return s.OrderView(ctx, uid, orderID, false)
}

// ============================ 状态推进（家长） ============================

// Confirm 家长确认（已去外链购买）
func (s *WishService) Confirm(ctx context.Context, uid uint, req *entities.WishHandleReq) (*entities.WishOrderView, error) {
	return s.transition(ctx, uid, req, entities.WishStatusCommitted, entities.WishStatusConfirmed, nil)
}

// Done 家长收尾（买好了 / 收到了）
func (s *WishService) Done(ctx context.Context, uid uint, req *entities.WishHandleReq) (*entities.WishOrderView, error) {
	extra := map[string]interface{}{"done_at": time.Now().Unix()}
	if req.TrackingNo != "" {
		extra["tracking_no"] = req.TrackingNo
	}

	// committed → done 和 confirmed → done 都允许（数字商品不必经过 confirm）
	order, err := s.Repo.GetOrder(ctx, uid, req.OrderID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if order == nil {
		return nil, apperrors.WithCode(apperrors.WishOrderNotExist)
	}
	return s.transition(ctx, uid, req, order.Status, entities.WishStatusDone, extra)
}

// Cancel 家长取消 —— **退钱（券 + 积分各退一笔）**，数字商品**同时收回游戏**。
//
// ## 为什么要收回游戏（spec §5「取消 = 撤销交易」）
//
// 不收回的话就是「白拿」：孩子兑换→立刻取消→钱退回来了、
// 游戏还留着。那就是印钞机。
//
// ## 退钱必须退两种币
//
// 2026-10-29 起可以券 + 积分混付。退款只退一种的话，
// 另一半就**凭空蒸发**，而且**不报错** ——
// 账本只看 delta 之和，症状只会表现为「孩子的钱少了」。
//
// ## 防重复退款
//
// 必须在**事务内先读 `status`** 并 CAS 更新，
// 不能靠按钮 disabled —— 两个标签页同时点会各退一次。
// 退款按 `VouchersHeld` / `PointsHeld` 退，**不重新读商品** ——
// 商品改价了也不能退错（前端 2026-10-29 的 `f274c48` 专门为这条加了单测）。
func (s *WishService) Cancel(ctx context.Context, uid uint, req *entities.WishHandleReq) (*entities.WishOrderView, error) {
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		order, err := s.Repo.GetOrderForUpdate(ctx, tx, uid, req.OrderID)
		if err != nil {
			return err
		}
		if order == nil {
			return apperrors.WithCode(apperrors.WishOrderNotExist)
		}
		if !order.Status.CanTransitionTo(entities.WishStatusCancelled) {
			return apperrors.WithCode(apperrors.WishStatusNotAllowed)
		}

		// 退券（按实际扣掉的数）
		if order.VouchersHeld > 0 {
			if _, _, err := s.WalletSrv.ChangeWithTx(ctx, tx, repository.BalanceChange{
				UID:            uid,
				Currency:       entities.CurrencyCoupon,
				Delta:          order.VouchersHeld,
				Source:         entities.SourceWishRefund,
				RefID:          order.ItemID,
				Memo:           fmt.Sprintf("取消心愿退回：%s", order.ItemName),
				IdempotencyKey: "wish_refund:" + order.OrderID,
			}); err != nil {
				return err
			}
		}

		// 退积分。
		//
		// ⚠️ **这一步不能省**：2026-10-29 起可以券 + 积分混付，
		// 而「一笔成交扣两种币、退款只退一种」= 另一半凭空蒸发，
		// 而且**不报错**（账本只看 delta 之和，只会表现为「孩子的钱少了」）。
		// 前端 `wishCancel` 也是两张账本各退一笔。
		if order.PointsHeld > 0 {
			if _, _, err := s.WalletSrv.ChangeWithTx(ctx, tx, repository.BalanceChange{
				UID:            uid,
				Currency:       entities.CurrencyPoints,
				Delta:          order.PointsHeld,
				Source:         entities.SourceWishRefund,
				RefID:          order.ItemID,
				Memo:           fmt.Sprintf("取消心愿退回：%s", order.ItemName),
				IdempotencyKey: "wish_refund_points:" + order.OrderID,
			}); err != nil {
				return err
			}
		}

		// 数字商品：收回游戏
		if order.OpenedGameID != "" {
			if err := s.GameRepo.RevokeGameWithTx(tx, uid, order.OpenedGameID); err != nil {
				return err
			}
		}

		rows, err := s.Repo.UpdateOrderCAS(ctx, tx, uid, order.OrderID, order.Status, map[string]interface{}{
			"status":        string(entities.WishStatusCancelled),
			"cancelled_at":  time.Now().Unix(),
			"cancel_reason": req.Reason,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return apperrors.WithCode(apperrors.WishStatusNotAllowed)
		}
		return nil
	})

	if err != nil {
		if apperrors.IsBizError(err) {
			return nil, err
		}
		logger.ZError("wish cancel failed", zap.Uint("uid", uid), zap.String("order_id", req.OrderID), zap.Error(err))
		return nil, apperrors.WithError(err)
	}

	s.EventSrv.Publish(WishOrderChangedEvent, map[string]interface{}{
		"uid": uid, "order_id": req.OrderID, "status": string(entities.WishStatusCancelled),
	})
	return s.OrderView(ctx, uid, req.OrderID, false)
}

// transition 通用状态推进一步
func (s *WishService) transition(
	ctx context.Context, uid uint, req *entities.WishHandleReq,
	from, to entities.WishStatus, extra map[string]interface{},
) (*entities.WishOrderView, error) {
	if !from.CanTransitionTo(to) {
		return nil, apperrors.WithCode(apperrors.WishStatusNotAllowed)
	}

	fields := map[string]interface{}{"status": string(to)}
	switch to {
	case entities.WishStatusConfirmed:
		fields["confirmed_at"] = time.Now().Unix()
	case entities.WishStatusDone:
		fields["done_at"] = time.Now().Unix()
	}
	for k, v := range extra {
		fields[k] = v
	}

	rows, err := s.Repo.UpdateOrderCAS(ctx, nil, uid, req.OrderID, from, fields)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if rows == 0 {
		return nil, apperrors.WithCode(apperrors.WishStatusNotAllowed)
	}

	s.EventSrv.Publish(WishOrderChangedEvent, map[string]interface{}{
		"uid": uid, "order_id": req.OrderID, "status": string(to),
	})
	return s.OrderView(ctx, uid, req.OrderID, false)
}

// ============================ 查询 ============================

// OrderView 组装订单视图
func (s *WishService) OrderView(ctx context.Context, uid uint, orderID string, isParent bool) (*entities.WishOrderView, error) {
	o, err := s.Repo.GetOrder(ctx, uid, orderID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if o == nil {
		return nil, apperrors.WithCode(apperrors.WishOrderNotExist)
	}

	view := &entities.WishOrderView{
		WishOrder:  *o,
		StatusText: entities.WishStatusText[o.Status],
		// ⚠️ 判据是 OpenedGameID != ""，**不是** kind：
		// 盒子装包失败会走取消退回，那种单子本来就不该显示成「已交付」
		Digital: o.OpenedGameID != "",
	}

	if o.OpenedGameID != "" {
		if g, err := s.GameRepo.GetByGameID(ctx, o.OpenedGameID); err == nil && g != nil {
			view.GameName = g.Name
		}
	}

	if isParent {
		if it, err := s.Repo.GetItem(ctx, o.ItemID); err == nil && it != nil {
			view.BuyURL = it.BuyURL
			view.BuyLabel = it.BuyLabel
		}
	} else if view.Ship != nil {
		// 孩子端：收货信息脱敏
		view.Ship = view.Ship.Masked()
	}
	return view, nil
}

// ListOrders 查询心愿单
func (s *WishService) ListOrders(ctx context.Context, uid uint, req *entities.ListWishOrderReq, isParent bool) ([]*entities.WishOrderView, int64, error) {
	list, total, err := s.Repo.ListOrders(ctx, uid, req.Status, req.Page, req.PageSize)
	if err != nil {
		return nil, 0, apperrors.WithError(err)
	}

	out := make([]*entities.WishOrderView, 0, len(list))
	for _, o := range list {
		view := &entities.WishOrderView{
			WishOrder:  *o,
			StatusText: entities.WishStatusText[o.Status],
			Digital:    o.OpenedGameID != "",
		}
		if o.OpenedGameID != "" {
			if g, err := s.GameRepo.GetByGameID(ctx, o.OpenedGameID); err == nil && g != nil {
				view.GameName = g.Name
			}
		}
		if isParent {
			if it, err := s.Repo.GetItem(ctx, o.ItemID); err == nil && it != nil {
				view.BuyURL = it.BuyURL
				view.BuyLabel = it.BuyLabel
			}
		} else if view.Ship != nil {
			view.Ship = view.Ship.Masked()
		}
		out = append(out, view)
	}
	return out, total, nil
}

// PendingTasks 家长待办统计。
//
// ⚠️ `wished` 也算待办（2026-10-29 起）—— 之前只收 `committed`/`confirmed`，
// 于是孩子点完「想要这个」、他那一侧写「已经告诉爸妈」，
// 家长那一侧却一片安静。两类要分开计数，因为**该做的动作不一样**：
//
//	wished（还没定价）    → 定价，或者这次先不换
//	committed/confirmed → 去外链买，或者收尾
//
// ## ★「待回应」的判据**不是** `status == wished`
//
// 家长定价之后，那条单子**仍然停在 `wished`** —— 它要等孩子真的来兑换，
// 才会被复用途中并翻成 `committed`。于是「还没被回应」和「已开放、等孩子来换」
// 在**数据上是同一个状态**。
//
// 只按状态算的话，家长定完价、红点还亮着，点进去却是一件已经开放的商品 ——
// **一个点不完的数字比没有红点更糟**。
//
// 判据取「这件商品现在有没有开放价」：定价就是家长的**回应**。
// `ClosedAt > 0` 的行不算开放（家长把它关了），那时重新变回「等你回应」是对的。
//
// 对应前端 `domain/wish.ts` 的 `wishesAwaitingReply`。
func (s *WishService) PendingTasks(ctx context.Context, uid uint) (map[string]interface{}, error) {
	committed, err := s.Repo.CountActiveByStatus(ctx, uid,
		entities.WishStatusCommitted, entities.WishStatusConfirmed)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	all, err := s.Repo.ListActiveOrders(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	// 已开放的商品 id 集合（有价、且没关闭）
	priced, err := s.Repo.ListOpenOfferItemIDs(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	wishedItems := make([]map[string]interface{}, 0)
	actionItems := make([]map[string]interface{}, 0)
	for _, o := range all {
		item := map[string]interface{}{
			"order_id": o.OrderID,
			"item_id":  o.ItemID,
			"name":     o.ItemName,
			"emoji":    o.ItemEmoji,
			"status":   string(o.Status),
			"kind":     string(o.Kind),
		}
		// 还没定价的才算「等你回应」
		if o.Status == entities.WishStatusWished && !priced[o.ItemID] {
			wishedItems = append(wishedItems, item)
		} else if o.Status != entities.WishStatusWished {
			actionItems = append(actionItems, item)
		}
		// 已定价但仍 wished 的：两边都不进 —— 家长回应过了，等孩子来换
	}

	wishedCount := int64(len(wishedItems))

	return map[string]interface{}{
		"wished_count": wishedCount,
		"action_count": committed,
		"wished_items": wishedItems,
		"action_items": actionItems,
		"total":        wishedCount + committed,
	}, nil
}

// ============================ 兑换码 ============================

// RedeemCode 用兑换码解锁（P2）。
//
// 码存的是 **sha256**，不是明文 —— 数据库泄露也不能直接拿来用。
func (s *WishService) RedeemCode(ctx context.Context, uid uint, req *entities.WishCodeRedeemReq) (*entities.WishCodeResp, error) {
	hash := hashWishCode(req.Code)
	now := time.Now().Unix()

	var resp *entities.WishCodeResp

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		code, err := s.Repo.GetCodeByHashForUpdate(ctx, tx, hash)
		if err != nil {
			return err
		}
		if code == nil {
			return apperrors.WithCode(apperrors.WishCodeInvalid)
		}
		if code.Used {
			return apperrors.WithCode(apperrors.WishCodeUsed)
		}
		if code.ExpireAt > 0 && code.ExpireAt < now {
			return apperrors.WithCode(apperrors.WishCodeExpired)
		}

		// CAS 标记已用（防并发重复使用）
		rows, err := s.Repo.MarkCodeUsed(ctx, tx, hash, uid, now)
		if err != nil {
			return err
		}
		if rows == 0 {
			return apperrors.WithCode(apperrors.WishCodeUsed)
		}

		resp = &entities.WishCodeResp{
			Code:     maskCode(req.Code),
			ItemID:   code.ItemID,
			Unlocked: true,
		}

		// 码对应游戏 → 解锁
		if code.ItemID != "" {
			it, err := s.Repo.GetItem(ctx, code.ItemID)
			if err != nil {
				return err
			}
			if it != nil && it.GameID != "" {
				grant := &entities.UserGame{
					UID:    uid,
					GameID: it.GameID,
					Source: entities.GameOwnedByCode,
				}
				if err := s.GameRepo.GrantGameWithTx(tx, grant); err != nil {
					return err
				}
				resp.GameID = it.GameID
			}
		}
		return nil
	})

	if err != nil {
		if apperrors.IsBizError(err) {
			return nil, err
		}
		return nil, apperrors.WithError(err)
	}
	return resp, nil
}

// ============================ 辅助 ============================

// hashWishCode 兑换码哈希（去空格、转大写后 sha256）
func hashWishCode(code string) string {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), " ", ""))
	h := sha256.Sum256([]byte("tf-wish:" + normalized))
	return hex.EncodeToString(h[:])
}

// maskCode 只显示后 4 位
func maskCode(code string) string {
	c := strings.TrimSpace(code)
	if len(c) <= 4 {
		return "****"
	}
	return "****" + c[len(c)-4:]
}

// keysOf 取 map 的键
func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// randomFloat 返回 [0,1) 的随机数。
//
// ⚠️ 用 **crypto/rand**，不是时间戳/数学随机。
// 盲盒开奖必须**不可预测**：用 `time.Now().UnixNano()` 这类种子，
// 孩子（或任何盯着代码的人）能算出下一次开出什么，
// 「盲盒」就变成了「选盒」。
func randomFloat() float64 {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		// 拿不到熵时退回时间种子（仍能工作，只是可预测性变差）
		return float64(time.Now().UnixNano()%1_000_000) / 1_000_000.0
	}
	v := uint64(b[0])<<56 | uint64(b[1])<<48 | uint64(b[2])<<40 | uint64(b[3])<<32 |
		uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7])
	// 取 53 位尾数（float64 精度上限）
	return float64(v>>11) / float64(1<<53)
}
