package service

import (
	"context"
	"sort"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
)

// ============================ 心愿上架（定价 = 上架） ============================

// ShelfEntries 返回**已上架**的心愿商品（家长定了价 = 上架）。
//
// 家长口径（2026-10-29）：
//
//	「爸爸妈妈已经定好价格的，相当于添加了兑换，应该出现在兑换列表里。」
//
// ## 四个排除条件，一个都不能少
//
//  1. **没定价 / 已关闭** —— 那是「等爸妈回应」，不是能换的东西
//  2. **已下架**（`Archived`）—— 目录里撤掉的商品不该还挂在兑换列表上
//  3. **已经拥有** —— 走 `ownedGameSet` 同一套规则（游戏盒子）。
//     少了这一条，孩子会在这里看到自己早就换到的游戏，点进去又是
//     「这个游戏你已经有了」
//  4. **盲盒池空** —— 池子空了还摆在「能换什么」里，点进去只会被拒
//
// 另外排除**已在走流程**的单子（`committed` / `confirmed`）：
// 那件东西已经付过钱了，再列一次等于鼓励孩子换第二遍。
// `wished` **不排除** —— 家长定了价，孩子就该能去换。
//
// 对应前端 `domain/wish.ts` 的 `wishShelf`。
func (s *WishService) ShelfEntries(ctx context.Context, uid uint) ([]*entities.WishShelfEntry, error) {
	offers, err := s.Repo.ListOpenOffers(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if len(offers) == 0 {
		return nil, nil
	}

	itemIDs := make([]string, 0, len(offers))
	priceOf := make(map[string]int64, len(offers))
	for _, o := range offers {
		itemIDs = append(itemIDs, o.ItemID)
		priceOf[o.ItemID] = o.VoucherPrice
	}

	items, err := s.Repo.ListItemsByIDs(ctx, itemIDs)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	byID := make(map[string]*entities.WishItem, len(items))
	for _, it := range items {
		byID[it.ItemID] = it
	}

	inFlight, err := s.Repo.ListInFlightItemIDs(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	ownedSet, err := s.ownedGameSet(ctx, uid)
	if err != nil {
		return nil, err
	}
	ownedList := keysOf(ownedSet)

	out := make([]*entities.WishShelfEntry, 0, len(offers))
	for _, o := range offers {
		if inFlight[o.ItemID] {
			continue
		}
		it := byID[o.ItemID]
		if it == nil || it.Archived {
			continue
		}
		// 已经拥有（游戏盒子）→ 不列
		if it.Kind == entities.WishKindGamebox && it.GameID != "" {
			if _, ok := ownedSet[it.GameID]; ok {
				continue
			}
		}
		// 盲盒池空 → 不列
		if it.Kind == entities.WishKindBlindbox {
			if len(entities.BlindBoxRemaining(it.Pool, ownedList)) == 0 {
				continue
			}
		}
		out = append(out, &entities.WishShelfEntry{Item: it, Price: priceOf[o.ItemID]})
	}
	return out, nil
}

// ============================ 兑换记录（合并时间线） ============================

// History 把「家里换的」和「心愿换的」合成一条按时间倒序的流水。
//
// ## 为什么必须合并
//
// 见 `entities.HistoryRow` 的注释 —— 家长报的 bug：
// 「孩子刚兑换了一个游戏，为啥我兑换记录里看不到」。
//
// ## `wished` 不算兑换
//
// 那是「想要」，**一分钱没扣**。列进来的话会显示「🎫 0」
// 或者按标价显示，两种都是**假账**。
//
// 对应前端 `domain/redeemHistory.ts` 的 `redeemHistory()`。
func (s *WishService) History(ctx context.Context, uid uint, limit int) ([]*entities.HistoryRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows := make([]*entities.HistoryRow, 0, limit)

	// 1. 家里的兑换记录
	if s.RedeemRepo != nil {
		records, _, err := s.RedeemRepo.ListRecords(ctx, uid, 1, limit)
		if err != nil {
			return nil, apperrors.WithError(err)
		}
		for _, r := range records {
			// 文案与前端 `redeemHistory` 逐字对齐
			tag := "待兑现"
			if r.Fulfilled {
				tag = "已兑现"
			}
			if r.Cancelled {
				tag = "已取消 · 已退回"
			}
			rows = append(rows, &entities.HistoryRow{
				ID:         r.RecordID,
				Source:     "redeem",
				Name:       r.Name,
				Emoji:      r.Emoji,
				CreatedAt:  r.CreatedAt,
				Pay:        entities.DescribeRedeemPayment(r.CouponPaid, r.PointsPaid),
				CouponPaid: r.CouponPaid,
				PointsPaid: r.PointsPaid,
				Cost:       r.Cost,
				Tag:        tag,
				Pending:    !r.Fulfilled && !r.Cancelled,
			})
		}
	}

	// 2. 心愿单（**只收已经扣过钱的**）
	orders, _, err := s.Repo.ListOrders(ctx, uid, "", 1, limit)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	for _, o := range orders {
		// `wished` = 还没扣钱，不是兑换
		if o.Status == entities.WishStatusWished {
			continue
		}

		tag := entities.WishStatusText[o.Status]
		delivered := o.OpenedGameID != ""

		// 取消的单子要写清「钱退回来了」，不然家长以为券白花了
		if o.Status == entities.WishStatusCancelled {
			tag = "已取消 · 已退回"
		}

		// ⚠️ 用 `ChildPending`，**不是** `NeedsParentAction`。
		//
		// 已经交付的数字商品不算待兑现：它确实还停在 `committed`（等家长点
		// 「完成这一单」），但东西孩子已经拿到手了 —— 计数把它算进去的话，
		// 记录页会出现「红点 1 + 绿标签 已放进游戏柜」这种自相矛盾的一屏
		// （前端截图走查抓到过）。家长那边的收尾提示在心愿页顶部，不靠这个数。
		//
		// 所以同一个单子：心愿页说「有 1 单在等爸妈处理」，这里没有徽标 ——
		// **故意的**，两个口径都写在 `entities/wish_pending.go` 里了。
		pending := entities.ChildPending(o.Status, o.OpenedGameID)

		var buyURL, buyLabel string
		if it, err := s.Repo.GetItem(ctx, o.ItemID); err == nil && it != nil {
			// `buy_open` 关掉 = 这里也空（记录页那颗「去购买」跟着消失）
			buyURL, buyLabel = it.BuyLink()
		}

		rows = append(rows, &entities.HistoryRow{
			ID:         o.OrderID,
			Source:     "wish",
			Name:       o.ItemName,
			Emoji:      o.ItemEmoji,
			CreatedAt:  o.CreatedAt,
			Pay:        entities.DescribeRedeemPayment(o.VouchersHeld, o.PointsHeld),
			CouponPaid: o.VouchersHeld,
			PointsPaid: o.PointsHeld,
			Cost:       o.VouchersHeld + o.PointsHeld,
			Tag:        tag,
			Pending:    pending,
			Delivered:  delivered,
			Status:     o.Status,
			BuyURL:     buyURL,
			BuyLabel:   buyLabel,
		})
	}

	// 3. 按时间倒序（并集的排序键）
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].CreatedAt > rows[j].CreatedAt
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// ============================ 统一兑换列表 ============================

// UnifiedRows 构造「兑换」那一格的完整列表：家里的兑换品 + 已上架的心愿商品。
//
// 两者**混在同一个列表里**，按价格升序 —— 家长口径是
// 「和家里的兑换品排在同一个分类列表里」，不是单独一块。
func (s *WishService) UnifiedRows(ctx context.Context, uid uint, redeemRows []*entities.RedeemRow) ([]*entities.RedeemRow, error) {
	shelf, err := s.ShelfEntries(ctx, uid)
	if err != nil {
		return nil, err
	}

	wallet, err := s.WalletSrv.GetWallet(ctx, uid)
	if err != nil {
		return nil, err
	}
	var coupon, points int64
	if wallet != nil {
		coupon, points = wallet.Coupon, wallet.Points
	}

	out := make([]*entities.RedeemRow, 0, len(redeemRows)+len(shelf))
	out = append(out, redeemRows...)

	ownedSet, err := s.ownedGameSet(ctx, uid)
	if err != nil {
		return nil, err
	}
	ownedList := keysOf(ownedSet)

	for _, e := range shelf {
		it := e.Item
		plan := entities.PlanRedeemPayment(e.Price, coupon, points)

		row := &entities.RedeemRow{
			Source: "wish",
			ID:     it.ItemID,
			Name:   it.Name,
			Emoji:  it.Emoji,
			// 分类固定 gift —— **只决定分组标题，不决定付款规则**
			// （家长口径：「兑换 里的东西都是一样」）
			Category:    "gift",
			Note:        it.Note,
			Price:       e.Price,
			Plan:        plan,
			CanRedeem:   plan.Affordable,
			Description: entities.DescribeRedeemPayment(plan.CouponPaid, plan.PointsPaid),
			Kind:        it.Kind,
			PartnerName: it.PartnerName,
			Notice:      it.Notice,
		}
		if it.Kind == entities.WishKindBlindbox {
			row.BlindBoxRemaining = len(entities.BlindBoxRemaining(it.Pool, ownedList))
		}
		out = append(out, row)
	}

	// 按价格升序混排（同价按名字，保证顺序稳定）
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Price != out[j].Price {
			return out[i].Price < out[j].Price
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
