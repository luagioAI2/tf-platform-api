package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"

	"github.com/google/wire"
	"gorm.io/gorm"
)

var FarmServiceSet = wire.NewSet(ProvideFarmService)

// FarmService 农场状态服务。
//
// 服务端**不理解**农场的玩法（作物怎么长、行情怎么走），它只负责三件事：
//
//	① 谁的那一份（uid 隔离）
//	② 别被两台设备同时写坏（rev 乐观锁）
//	③ 农场里的钱要和服务端账本对上（随状态提交的账本条目）
//
// 为什么不把玩法搬到服务端：客户端的 `src/domain/` 有 1000+ 条单测在守它，
// 搬一份到 Go 就是两份规则、两个真相，改一处必然漏另一处。
type FarmService struct {
	Repo       *repository.FarmStateRepository
	WalletRepo *repository.WalletRepository
}

// ProvideFarmService 构造函数
func ProvideFarmService(
	repo *repository.FarmStateRepository,
	walletRepo *repository.WalletRepository,
) *FarmService {
	return &FarmService{Repo: repo, WalletRepo: walletRepo}
}

// 允许随农场状态一起提交的账本来源。
//
// 白名单而不是「客户端说啥就是啥」：账本来源是给家长看流水的
// （「卖出 12 券」vs「任务奖励 10 分」），来源错了流水就读不懂。
// 这也挡住客户端顺手把自己的消费记成 `task_reward`。
var farmClaimSources = map[string]bool{
	entities.SourceFarmHarvest:   true, // 作物卖出
	entities.SourceAnimalProduce: true, // 动物产出卖出
	entities.SourceSeedPurchase:  true, // 买种子（扣积分）
	entities.SourceAnimalPurchase: true, // 买动物（扣积分）
	entities.SourcePluginMint:    true, // 插件游戏发券
}

// Get 读取农场状态
func (s *FarmService) Get(ctx context.Context, uid uint) (*entities.FarmStateGetResp, error) {
	st, err := s.Repo.Get(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if st == nil {
		// 新用户：客户端拿到 `Empty` 就用本地初始农场播种，
		// 而不是「服务端给一个空农场」—— 那样地块会全空，孩子一进去以为菜没了。
		return &entities.FarmStateGetResp{Empty: true}, nil
	}
	return &entities.FarmStateGetResp{
		Rev:       st.Rev,
		Payload:   st.Payload,
		UpdatedAt: st.UpdatedAt,
	}, nil
}

// Save 保存农场状态（可同时提交账本变动）。
//
// 顺序很重要：**先存状态（含 rev 校验），再记账**。
// 反过来的话，版本冲突时账已经记上了，客户端一重拉就会发现
// 「券多了但菜还在」——而它没有任何办法撤销。
func (s *FarmService) Save(
	ctx context.Context, uid uint, req *entities.FarmStateSaveReq,
) (*entities.FarmStateSaveResp, error) {
	if len(req.Payload) > entities.FarmStateMaxPayload {
		return nil, apperrors.WithCode(apperrors.FarmStateTooLarge)
	}
	if !json.Valid([]byte(req.Payload)) {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}
	for _, c := range req.Ledger {
		if err := validateFarmClaim(c); err != nil {
			return nil, err
		}
	}

	var (
		newRev   int64
		conflict bool
		applied  int
	)

	err := s.Repo.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rev, cf, err := s.Repo.SaveTx(ctx, tx, uid, req.Payload, req.Rev, req.Force)
		if err != nil {
			return err
		}
		if cf {
			conflict = true
			newRev = rev
			return nil
		}
		newRev = rev

		for _, c := range req.Ledger {
			_, _, err := s.WalletRepo.ApplyChangeWithTx(ctx, tx, repository.BalanceChange{
				UID:            uid,
				Currency:       entities.Currency(c.Currency),
				Delta:          c.Delta,
				Source:         c.Source,
				Memo:           c.Memo,
				IdempotencyKey: farmOpKey(uid, c.OpID),
			})
			// 幂等命中 = 这一笔之前已经入过账（重试 / 连点），不算失败也不算新增
			if errors.Is(err, repository.ErrDuplicateLedger) {
				continue
			}
			if err != nil {
				return err
			}
			applied++
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, repository.ErrInsufficientBalance) {
			return nil, apperrors.WithCode(apperrors.InsufficientBalance)
		}
		return nil, apperrors.WithError(err)
	}

	if conflict {
		return &entities.FarmStateSaveResp{Conflict: true, Rev: newRev}, nil
	}
	return &entities.FarmStateSaveResp{
		Rev:      newRev,
		Applied:  applied,
		Balances: s.balances(ctx, uid),
	}, nil
}

// balances 读三套币余额。
//
// 读失败**不影响保存结果** —— 状态已经落库了，余额只是顺带给客户端的便利。
// 但必须留日志：返回空 map 时客户端会退回「自己再查一次钱包」。
func (s *FarmService) balances(ctx context.Context, uid uint) map[string]int64 {
	w, err := s.WalletRepo.GetWallet(ctx, uid)
	if err != nil || w == nil {
		return map[string]int64{}
	}
	return map[string]int64{
		string(entities.CurrencyPoints):  w.Points,
		string(entities.CurrencyCoupon):  w.Coupon,
		string(entities.CurrencyDiamond): w.Diamond,
	}
}

// validateFarmClaim 校验一条随状态提交的账本变动
func validateFarmClaim(c entities.FarmLedgerClaim) error {
	if !entities.Currency(c.Currency).IsValid() {
		return apperrors.WithCode(apperrors.FarmLedgerBadClaim)
	}
	if c.Delta == 0 {
		return apperrors.WithCode(apperrors.FarmLedgerBadClaim)
	}
	if c.OpID == "" || len(c.OpID) > 64 {
		return apperrors.WithCode(apperrors.FarmLedgerBadClaim)
	}
	if !farmClaimSources[c.Source] {
		return apperrors.WithCode(apperrors.FarmLedgerBadClaim)
	}
	return nil
}

// farmOpKey 幂等键。
//
// ★ 必须带 uid：`ledger_entry.idempotency_key` 是全局唯一的，
//   只带客户端生成的 op_id 的话，两个用户的随机 id 撞了会互相吃掉对方的一笔账。
func farmOpKey(uid uint, opID string) string {
	return fmt.Sprintf("farm:%d:%s", uid, opID)
}
