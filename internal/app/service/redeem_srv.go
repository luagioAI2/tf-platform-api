package service

import (
	"context"
	"errors"
	"fmt"
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

var RedeemServiceSet = wire.NewSet(ProvideRedeemService)

// RedeemService 兑换商城服务。
//
// ## 支付规则（对齐前端 `domain/redeem.ts`）
//
//  1. 兑换券和积分 **1:1 等价**
//  2. **先花兑换券**，花光了再用积分
//  3. 可以两种币混着付
//
// 「券优先」不是技术选择，是产品口径：券是农场产出的出口，
// 先花券能让孩子感受到「种地有回报」。
type RedeemService struct {
	Repo      *repository.RedeemRepository
	WalletSrv *WalletService
	EventSrv  *EventService
	// WishSrv 供「定价 = 上架」把心愿商品混进兑换列表（见 UnifiedRows）
	WishSrv  *WishService
	DB       *gorm.DB
	timezone *time.Location
}

// ProvideRedeemService 构造函数
func ProvideRedeemService(
	repo *repository.RedeemRepository,
	walletSrv *WalletService,
	eventSrv *EventService,
	wishSrv *WishService,
	db *gorm.DB,
) *RedeemService {
	loc, err := time.LoadLocation(config.Get().ServiceSettings.Timezone)
	if err != nil {
		loc = time.UTC
	}
	return &RedeemService{
		Repo:      repo,
		WalletSrv: walletSrv,
		EventSrv:  eventSrv,
		WishSrv:   wishSrv,
		DB:        db,
		timezone:  loc,
	}
}

// ============================ 商品管理（家长） ============================

// CreateItem 创建商品
func (s *RedeemService) CreateItem(ctx context.Context, uid uint, req *entities.CreateRedeemItemReq) (*entities.RedeemItem, error) {
	if !req.Category.IsValid() {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}
	if req.Price <= 0 {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}

	itemID := req.ItemID
	if itemID == "" {
		itemID = generateRedeemID("R")
	}

	// 业务 ID 唯一性
	existing, err := s.Repo.GetItem(ctx, uid, itemID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if existing != nil {
		return nil, apperrors.WithCode(apperrors.DuplicateOperation)
	}

	stock := req.Stock
	if stock == 0 {
		stock = -1 // 默认无限
	}

	item := &entities.RedeemItem{
		UID:             uid,
		ItemID:          itemID,
		Name:            req.Name,
		Emoji:           req.Emoji,
		Category:        req.Category,
		Price:           req.Price,
		Note:            req.Note,
		LimitPerDay:     req.LimitPerDay,
		Once:            req.Once,
		Stock:           stock,
		CreatedByParent: true,
		Sort:            req.Sort,
	}
	if err := s.Repo.CreateItem(ctx, item); err != nil {
		return nil, apperrors.WithError(err)
	}
	return item, nil
}

// UpdateItem 更新商品
func (s *RedeemService) UpdateItem(ctx context.Context, uid uint, itemID string, req *entities.UpdateRedeemItemReq) (*entities.RedeemItem, error) {
	item, err := s.Repo.GetItem(ctx, uid, itemID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if item == nil {
		return nil, apperrors.WithCode(apperrors.RedeemItemNotExist)
	}

	fields := make(map[string]interface{}, 10)
	if req.Name != nil {
		fields["name"] = *req.Name
	}
	if req.Emoji != nil {
		fields["emoji"] = *req.Emoji
	}
	if req.Category != nil {
		if !req.Category.IsValid() {
			return nil, apperrors.WithCode(apperrors.InvalidParam)
		}
		fields["category"] = string(*req.Category)
	}
	if req.Price != nil {
		if *req.Price <= 0 {
			return nil, apperrors.WithCode(apperrors.InvalidParam)
		}
		fields["price"] = *req.Price
	}
	if req.Note != nil {
		fields["note"] = *req.Note
	}
	if req.LimitPerDay != nil {
		fields["limit_per_day"] = *req.LimitPerDay
	}
	if req.Once != nil {
		fields["once"] = *req.Once
	}
	if req.Stock != nil {
		fields["stock"] = *req.Stock
	}
	if req.Archived != nil {
		fields["archived"] = *req.Archived
	}
	if req.Sort != nil {
		fields["sort"] = *req.Sort
	}

	if err := s.Repo.UpdateItem(ctx, uid, itemID, fields); err != nil {
		return nil, apperrors.WithError(err)
	}
	return s.Repo.GetItem(ctx, uid, itemID)
}

// DeleteItem 删除商品
func (s *RedeemService) DeleteItem(ctx context.Context, uid uint, itemID string) error {
	item, err := s.Repo.GetItem(ctx, uid, itemID)
	if err != nil {
		return apperrors.WithError(err)
	}
	if item == nil {
		return apperrors.WithCode(apperrors.RedeemItemNotExist)
	}
	// 同 task_srv.DeleteTask：`WithError(nil)` 是非 nil 的 500，
	// 直接 return 会让「删成功」也回「系统错误」。
	if err := s.Repo.DeleteItem(ctx, uid, itemID); err != nil {
		return apperrors.WithError(err)
	}
	return nil
}

// ============================ 商品列表（孩子/家长） ============================

// ListItems 列出「兑换」那一格的全部可换商品。
//
// ## 两个来源，同一个列表（2026-10-29 家长口径）
//
//	Source = "redeem" —— 家长自己加的兑换品
//	Source = "wish"   —— 心愿商品被家长**定了价**（「定价 = 上架」）
//
// 家长原话：「爸爸妈妈已经定好价格的，相当于添加了兑换，应该出现在
// 兑换列表里」「和家里的兑换品排在同一个分类列表里」。
//
// ⚠️ 所以这里**不是**分成两块返回，而是混成一个列表按价格升序 ——
// 分块的话就成了「另一个收银台」，而家长要的正是「不需要去心愿商城换」。
//
// ## 家里的兑换品的过滤规则
//
//   - 已归档的不出现
//   - `once` 且换过的不出现（**永不重置**）
//   - 库存为 0 的不出现
//
// 心愿那半的过滤规则见 `WishService.ShelfEntries`（四条，一个都不能少）。
//
// 每行附带：分账预览（券多少 + 积分多少）/ 够不够换 / 今日上限。
//
// ## raw 模式（客户端镜像用）
//
// `raw = true` 时**一条都不删**，改为在每行上标 `Visible`。原因见
// `ListRedeemItemReq.Raw` 的注释：上面那三条是「给孩子看」的口径，
// 家长管理页拿它当数据源就会看到「商品不见了」，然后重新建一条一模一样的。
// 过滤规则本身只有一份（下面这个 `visible` 表达式），两种模式共用，
// 所以不会出现「列表里没有、兑换时又说能换」这种自相矛盾。
func (s *RedeemService) ListItems(
	ctx context.Context, uid uint, includeArchived, raw bool,
) ([]*entities.RedeemRow, error) {
	items, err := s.Repo.ListItems(ctx, uid, includeArchived)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	wallet, err := s.WalletSrv.GetWallet(ctx, uid)
	if err != nil {
		return nil, err
	}
	var coupon, points int64
	if wallet != nil {
		coupon, points = wallet.Coupon, wallet.Points
	}

	dayStart := s.todayStart()
	out := make([]*entities.RedeemRow, 0, len(items))

	for _, it := range items {
		// once：换过就不再出现
		onceDone := false
		if it.Once {
			has, err := s.Repo.HasRedeemedOnce(ctx, uid, it.ItemID)
			if err != nil {
				return nil, apperrors.WithError(err)
			}
			onceDone = has
		}

		// 库存
		soldOut := it.Stock == 0
		if !soldOut && it.Stock > 0 {
			used, err := s.Repo.CountRedeemedByItem(ctx, uid, it.ItemID)
			if err != nil {
				return nil, apperrors.WithError(err)
			}
			soldOut = used >= int64(it.Stock)
		}

		/* 三条过滤规则只写在这一处 —— 默认模式和 raw 模式共用，
		   所以不会出现「列表里没有它、兑换时又说能换」。 */
		visible := !it.Archived && !onceDone && !soldOut
		if !raw && !visible {
			continue
		}

		todayCount, err := s.Repo.CountTodayByItem(ctx, uid, it.ItemID, dayStart)
		if err != nil {
			return nil, apperrors.WithError(err)
		}
		remainingToday := -1 // -1 = 不限
		if it.LimitPerDay > 0 {
			remainingToday = it.LimitPerDay - int(todayCount)
			if remainingToday < 0 {
				remainingToday = 0
			}
		}

		plan := entities.PlanRedeemPayment(it.Price, coupon, points)
		blockedByLimit := it.LimitPerDay > 0 && todayCount >= int64(it.LimitPerDay)

		out = append(out, &entities.RedeemRow{
			Source:          "redeem",
			ID:              it.ItemID,
			Name:            it.Name,
			Emoji:           it.Emoji,
			Category:        string(it.Category),
			Note:            it.Note,
			Price:           it.Price,
			Plan:            plan,
			CanRedeem:       plan.Affordable && !blockedByLimit,
			Description:     entities.DescribeRedeemPayment(plan.CouponPaid, plan.PointsPaid),
			BlockedByLimit:  blockedByLimit,
			TodayCount:      todayCount,
			RemainingToday:  remainingToday,
			Once:            it.Once,
			Stock:           it.Stock,
			Archived:        it.Archived,
			LimitPerDay:     it.LimitPerDay,
			CreatedByParent: it.CreatedByParent,
			CreatedAt:       it.CreatedAt,
			UpdatedAt:       it.UpdatedAt,
			Visible:         visible,
		})
	}

	// 混入「已上架」的心愿商品，并按价格升序排序
	if s.WishSrv != nil {
		return s.WishSrv.UnifiedRows(ctx, uid, out)
	}
	return out, nil
}

// ============================ 兑换（孩子） ============================

// Redeem 兑换。
//
// ## 事务边界（关键）
//
// 全部在一个事务里：
//  1. 加锁读商品（防并发改价/改库存）
//  2. 校验 `once` / 每日限额 / 库存
//  3. 算分账（券优先）
//  4. 扣兑换券 + 扣积分
//  5. 写兑换记录
//
// ## 为什么必须原子
//
// 拆开就会出现「扣了券没记记录」或「记了记录没扣券」，
// 而且**不报错** —— 孩子白花一次，或者白拿一次。
func (s *RedeemService) Redeem(ctx context.Context, uid uint, req *entities.RedeemReq) (*entities.RedeemResultResp, error) {
	quantity := req.Quantity
	if quantity <= 0 {
		quantity = 1
	}

	var result *entities.RedeemResultResp

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 加锁读商品
		item, err := s.Repo.GetItemForUpdate(ctx, tx, uid, req.ItemID)
		if err != nil {
			return err
		}
		if item == nil {
			return apperrors.WithCode(apperrors.RedeemItemNotExist)
		}
		if item.Archived {
			return apperrors.WithCode(apperrors.RedeemItemOffline)
		}

		// 2. 校验 once
		if item.Once {
			has, err := s.Repo.HasRedeemedOnce(ctx, uid, item.ItemID)
			if err != nil {
				return err
			}
			if has {
				return apperrors.WithCode(apperrors.RedeemOnceOnly)
			}
		}

		// 3. 每日限额
		if item.LimitPerDay > 0 {
			todayCount, err := s.Repo.CountTodayByItem(ctx, uid, item.ItemID, s.todayStart())
			if err != nil {
				return err
			}
			if todayCount+int64(quantity) > int64(item.LimitPerDay) {
				return apperrors.WithCode(apperrors.RedeemLimitExceeded)
			}
		}

		// 4. 库存
		if item.Stock > 0 {
			used, err := s.Repo.CountRedeemedByItem(ctx, uid, item.ItemID)
			if err != nil {
				return err
			}
			if used+int64(quantity) > int64(item.Stock) {
				return apperrors.WithCode(apperrors.RedeemOutOfStock)
			}
		}

		// 5. 算分账（单价 × 数量）
		totalPrice := item.Price * int64(quantity)

		wallet, err := s.WalletSrv.GetWalletWithTx(ctx, tx, uid)
		if err != nil {
			return err
		}
		var couponBal, pointsBal int64
		if wallet != nil {
			couponBal, pointsBal = wallet.Coupon, wallet.Points
		}

		plan := entities.PlanRedeemPayment(totalPrice, couponBal, pointsBal)
		if !plan.Affordable {
			return apperrors.WithCode(apperrors.InsufficientBalance)
		}

		// 6. 扣券（先花券）
		if plan.CouponPaid > 0 {
			if _, _, err := s.WalletSrv.ChangeWithTx(ctx, tx, repository.BalanceChange{
				UID:            uid,
				Currency:       entities.CurrencyCoupon,
				Delta:          -plan.CouponPaid,
				Source:         entities.SourceRedeemCoupon,
				RefID:          item.ItemID,
				Memo:           fmt.Sprintf("兑换：%s", item.Name),
				IdempotencyKey: "redeem_coupon:" + req.ClientRequestID,
			}); err != nil {
				return err
			}
		}

		// 7. 扣积分（券不够的部分）
		if plan.PointsPaid > 0 {
			if _, _, err := s.WalletSrv.ChangeWithTx(ctx, tx, repository.BalanceChange{
				UID:            uid,
				Currency:       entities.CurrencyPoints,
				Delta:          -plan.PointsPaid,
				Source:         entities.SourceRedeem,
				RefID:          item.ItemID,
				Memo:           fmt.Sprintf("兑换：%s", item.Name),
				IdempotencyKey: "redeem_points:" + req.ClientRequestID,
			}); err != nil {
				return err
			}
		}

		// 8. 写记录（含余额快照）
		after, err := s.WalletSrv.GetWalletWithTx(ctx, tx, uid)
		if err != nil {
			return err
		}
		var afterCoupon, afterPoints int64
		if after != nil {
			afterCoupon, afterPoints = after.Coupon, after.Points
		}

		rec := &entities.RedeemRecord{
			UID:                uid,
			RecordID:           generateRedeemID("RR"),
			ItemID:             item.ItemID,
			Name:               item.Name,
			Emoji:              item.Emoji,
			Quantity:           quantity,
			Cost:               plan.Total,
			CouponPaid:         plan.CouponPaid,
			PointsPaid:         plan.PointsPaid,
			PointsBalanceAfter: afterPoints,
			CouponBalanceAfter: afterCoupon,
		}
		if err := s.Repo.CreateRecord(ctx, tx, rec); err != nil {
			return err
		}

		result = &entities.RedeemResultResp{
			RecordID:    rec.RecordID,
			ItemID:      item.ItemID,
			Name:        item.Name,
			Emoji:       item.Emoji,
			Quantity:    quantity,
			Plan:        plan,
			Description: entities.DescribeRedeemPayment(plan.CouponPaid, plan.PointsPaid),
			Wallet: entities.WalletInfo{
				UID:     uid,
				Points:  afterPoints,
				Coupon:  afterCoupon,
				Diamond: walletDiamond(after),
			},
		}
		return nil
	})

	if err != nil {
		if apperrors.IsBizError(err) {
			return nil, err
		}
		logger.ZError("redeem failed", zap.Uint("uid", uid), zap.String("item_id", req.ItemID), zap.Error(err))
		return nil, apperrors.WithError(err)
	}

	s.EventSrv.Publish(RedeemCompletedEvent, map[string]interface{}{
		"uid":         uid,
		"item_id":     req.ItemID,
		"record_id":   result.RecordID,
		"coupon_paid": result.Plan.CouponPaid,
		"points_paid": result.Plan.PointsPaid,
	})
	return result, nil
}

// ============================ 兑现与取消（家长） ============================

// Fulfill 标记已兑现（「已经给过了」）
func (s *RedeemService) Fulfill(ctx context.Context, uid uint, recordID, note string) (*entities.RedeemRecord, error) {
	now := time.Now().Unix()
	rows, err := s.Repo.UpdateRecordStatusCAS(ctx, nil, uid, recordID, false, false, map[string]interface{}{
		"fulfilled":      true,
		"fulfilled_at":   now,
		"fulfilled_note": note,
	})
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if rows == 0 {
		return nil, apperrors.WithCode(apperrors.RedeemAlreadyHandled)
	}
	return s.Repo.GetRecord(ctx, uid, recordID)
}

// Cancel 取消兑换并**退券退分**。
//
// ## 防重复退款
//
// 必须在**事务内先读 `cancelled` 状态**并 CAS 更新，
// 不能靠按钮 disabled —— 两个标签页同时点会各退一次。
// 退款按记录里的分账退（`CouponPaid` / `PointsPaid`），
// 不重新读商品价格：商品改价了也不能退错。
func (s *RedeemService) Cancel(ctx context.Context, uid uint, recordID, reason string) (*entities.RedeemRecord, error) {
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 加锁读记录
		rec, err := s.Repo.GetRecordForUpdate(ctx, tx, uid, recordID)
		if err != nil {
			return err
		}
		if rec == nil {
			return apperrors.WithCode(apperrors.RedeemRecordNotExist)
		}
		if rec.Cancelled {
			return apperrors.WithCode(apperrors.RedeemAlreadyHandled)
		}

		// 2. 退券
		if rec.CouponPaid > 0 {
			if _, _, err := s.WalletSrv.ChangeWithTx(ctx, tx, repository.BalanceChange{
				UID:            uid,
				Currency:       entities.CurrencyCoupon,
				Delta:          rec.CouponPaid,
				Source:         entities.SourceRedeemRefund,
				RefID:          rec.ItemID,
				Memo:           fmt.Sprintf("取消兑换退回：%s", rec.Name),
				IdempotencyKey: "redeem_refund_coupon:" + recordID,
			}); err != nil {
				return err
			}
		}

		// 3. 退积分
		if rec.PointsPaid > 0 {
			if _, _, err := s.WalletSrv.ChangeWithTx(ctx, tx, repository.BalanceChange{
				UID:            uid,
				Currency:       entities.CurrencyPoints,
				Delta:          rec.PointsPaid,
				Source:         entities.SourceRedeemRefund,
				RefID:          rec.ItemID,
				Memo:           fmt.Sprintf("取消兑换退回：%s", rec.Name),
				IdempotencyKey: "redeem_refund_points:" + recordID,
			}); err != nil {
				return err
			}
		}

		// 4. CAS 标记取消（防并发重复退款）
		rows, err := s.Repo.UpdateRecordStatusCAS(ctx, tx, uid, recordID, false, false, map[string]interface{}{
			"cancelled":     true,
			"cancelled_at":  time.Now().Unix(),
			"cancel_reason": reason,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return apperrors.WithCode(apperrors.RedeemAlreadyHandled)
		}
		return nil
	})

	if err != nil {
		if apperrors.IsBizError(err) {
			return nil, err
		}
		return nil, apperrors.WithError(err)
	}
	return s.Repo.GetRecord(ctx, uid, recordID)
}

// ListRecords 查询兑换记录
func (s *RedeemService) ListRecords(ctx context.Context, uid uint, page, pageSize int) ([]*entities.RedeemRecordView, int64, error) {
	list, total, err := s.Repo.ListRecords(ctx, uid, page, pageSize)
	if err != nil {
		return nil, 0, apperrors.WithError(err)
	}
	out := make([]*entities.RedeemRecordView, 0, len(list))
	for _, r := range list {
		out = append(out, &entities.RedeemRecordView{
			RedeemRecord: *r,
			Description:  entities.DescribeRedeemPayment(r.CouponPaid, r.PointsPaid),
		})
	}
	return out, total, nil
}

// Stats 兑换统计（家长看板）
func (s *RedeemService) Stats(ctx context.Context, uid uint, days int) (map[string]interface{}, error) {
	if days <= 0 {
		days = 30
	}
	now := time.Now().In(s.timezone)
	from := now.AddDate(0, 0, -days).Unix()

	points, coupon, err := s.Repo.SumPointsRedeemed(ctx, uid, from, now.Unix())
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	return map[string]interface{}{
		"days":            days,
		"points_redeemed": points,
		"coupon_redeemed": coupon,
		"total_redeemed":  points + coupon,
	}, nil
}

// ============================ 辅助 ============================

// todayStart 今天 0 点（配置时区）
func (s *RedeemService) todayStart() int64 {
	now := time.Now().In(s.timezone)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.timezone).Unix()
}

// walletDiamond 安全读钻石余额
func walletDiamond(w *entities.Wallet) int64 {
	if w == nil {
		return 0
	}
	return w.Diamond
}

// generateRedeemID 生成业务 ID：前缀 + 时间戳 + 随机后缀
func generateRedeemID(prefix string) string {
	return fmt.Sprintf("%s%d%06d", prefix, time.Now().Unix(), time.Now().Nanosecond()/1000%1000000)
}

// 确保 errors 被使用
var _ = errors.Is
