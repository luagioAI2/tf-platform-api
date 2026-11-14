package service

import (
	"context"
	"crypto/rand"
	"math/big"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"

	"github.com/google/wire"
	"gorm.io/gorm"
)

var CampaignServiceSet = wire.NewSet(ProvideCampaignService)

/* ============================================================
   主线活动（在线版）
   ------------------------------------------------------------
   在线版的拥有关系只认 `user_game`，所以「免费激活一款游戏」这件事
   **只能由服务端做** —— 客户端自己写本地账本，换台设备就没了，
   而且存档写不进去（`checkGameExists` 要 `user_game` 有行）。

   ## 三条必须守住的口径

   1. **进度服务端现算。** 客户端传上来的只是 `step_id`。
      让客户端传 `have` / `target` 等于让它自己决定送哪款游戏。

   2. **条件不满足就拒绝，且不写任何东西。** 先查条件、再插领取记录，
      顺序反了会出现「记录写了、游戏没授」——孩子这一关永远领不到，
      而两边都不报错。

   3. **重复领取是幂等的。** 手抖点两下不该报错，也不该授两次。
   ============================================================ */

// CampaignService 主线活动服务
type CampaignService struct {
	GameRepo *repository.GameRepository
	DB       *gorm.DB
}

// ProvideCampaignService 构造函数
func ProvideCampaignService(gameRepo *repository.GameRepository, db *gorm.DB) *CampaignService {
	return &CampaignService{GameRepo: gameRepo, DB: db}
}

// couponEarnSources 算作「赚到兑换券」的来源白名单。
//
// ⚠️ 必须是白名单，不能写成「所有正数流水」：
//
//	· `wish_refund` / `redeem_refund` 是退款 —— 退回来的是自己花掉的；
//	· `manual_adjust` 是家长手工加的 —— 算进去的话发一笔就能点亮全部关卡，
//	  这条主线当场失去意义。
//
// ★ 这份清单要和客户端 `domain/campaign.ts` 的 `EARN_SOURCES` 保持一致。
//
//	两边不一致的症状：客户端说「可以领」，点了服务端说「还没到」——
//	不崩，但用户会觉得这个按钮坏了。
var couponEarnSources = []string{
	"farm_harvest",   // 作物收获卖出
	"farm_market",    // 市场卖出产出
	"animal_produce", // 动物产出
	"claw_prize",     // 奖品 → 兑换券
	"plugin_mint",    // 插件游戏发券
	"game_redeem",    // 游戏币换券
}

// stats 现算两个指标。`tasksDone` 认 completed + failed（后者是「做完了但零分」）。
func (s *CampaignService) stats(ctx context.Context, uid uint) (tasksDone, couponsEarned int64, err error) {
	if err = s.DB.WithContext(ctx).Model(&entities.TaskInstance{}).
		Where("uid = ? AND status IN ?", uid, []string{"completed", "failed"}).
		Count(&tasksDone).Error; err != nil {
		return 0, 0, apperrors.WithError(err)
	}

	if err = s.DB.WithContext(ctx).Model(&entities.LedgerEntry{}).
		Where("uid = ? AND currency = ? AND delta > 0 AND source IN ?",
			uid, entities.CurrencyCoupon, couponEarnSources).
		Select("COALESCE(SUM(delta), 0)").
		Scan(&couponsEarned).Error; err != nil {
		return 0, 0, apperrors.WithError(err)
	}
	return tasksDone, couponsEarned, nil
}

// State 拉「领了哪几关 + 服务端算的进度」。
//
// 客户端也会自己算一份（本地版必须有那份），这里返回的值用来：
// ① 标出哪几关已领（**以服务端为准**，拥有关系在服务端）；
// ② 领奖后对账。
func (s *CampaignService) State(ctx context.Context, uid uint) (*entities.CampaignStateResp, error) {
	tasksDone, couponsEarned, err := s.stats(ctx, uid)
	if err != nil {
		return nil, err
	}

	var rows []entities.CampaignClaim
	if err := s.DB.WithContext(ctx).
		Where("uid = ?", uid).
		Find(&rows).Error; err != nil {
		return nil, apperrors.WithError(err)
	}
	claimed := make(map[string]bool, len(rows))
	for _, r := range rows {
		claimed[r.StepID] = true
	}

	steps := make([]entities.CampaignStepView, 0, len(entities.CampaignSteps()))
	for _, def := range entities.CampaignSteps() {
		have := tasksDone
		if def.Metric == entities.CampaignMetricCouponEarned {
			have = couponsEarned
		}
		steps = append(steps, entities.CampaignStepView{
			ID:      def.StepID,
			GameID:  def.GameID,
			Claimed: claimed[def.StepID],
			Have:    have,
			Target:  def.Target,
		})
	}

	return &entities.CampaignStateResp{
		Steps:         steps,
		TasksDone:     tasksDone,
		CouponsEarned: couponsEarned,
	}, nil
}

// Claim 领一关。
//
// 顺序（**不能换**）：查关卡 → 已领则幂等返回 → 算进度 → 够不够 → 事务里
// 插记录 + 授游戏。先在内存里判完再进事务，是为了「条件不满足时一个字节都不写」。
func (s *CampaignService) Claim(ctx context.Context, uid uint, stepID string) (*entities.CampaignClaimResp, error) {
	def := entities.CampaignStepByID(stepID)
	if def == nil {
		return nil, apperrors.WithCode(apperrors.CampaignStepNotExist)
	}

	// ① 已领过 ⇒ 幂等返回（手抖点两下不该报错，也不该授两次）
	var existing entities.CampaignClaim
	err := s.DB.WithContext(ctx).Where("uid = ? AND step_id = ?", uid, stepID).First(&existing).Error
	if err == nil {
		return &entities.CampaignClaimResp{
			StepID:  stepID,
			GameID:  existing.GameID,
			Already: true,
		}, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, apperrors.WithError(err)
	}

	// ② 现算进度，够不够由服务端说了算
	tasksDone, couponsEarned, err := s.stats(ctx, uid)
	if err != nil {
		return nil, err
	}
	have := tasksDone
	if def.Metric == entities.CampaignMetricCouponEarned {
		have = couponsEarned
	}
	if have < def.Target {
		return nil, apperrors.WithCode(apperrors.CampaignNotReady)
	}

	/* ★ 随机奖励（用户 2026-11-14 第 5 关）：现在现挑一款。
	   必须在**事务之前**挑好 —— 挑的过程中要读 `user_game`，
	   放在事务里读会让「挑哪款」依赖事务内的可见性，容易挑到刚授的那款。 */
	giftID := def.GameID
	if def.RandomGift {
		giftID, err = s.pickRandomGift(ctx, uid)
		if err != nil {
			return nil, err
		}
	}

	// ③ 事务：记录 + 授游戏。两件事必须一起成，否则「记录有了、游戏没授」
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&entities.CampaignClaim{
			UID:    uid,
			StepID: stepID,
			GameID: giftID,
		}).Error; err != nil {
			// 并发重复点：唯一索引会拦下来。此时当作「已领」返回，不算失败。
			return err
		}
		return s.GameRepo.GrantGameWithTx(tx, &entities.UserGame{
			UID:    uid,
			GameID: giftID,
			Source: entities.GameOwnedByGift,
		})
	})
	if err != nil {
		// 唯一索引冲突 ⇒ 另一个请求刚领完，幂等返回
		var again entities.CampaignClaim
		if e2 := s.DB.WithContext(ctx).
			Where("uid = ? AND step_id = ?", uid, stepID).
			First(&again).Error; e2 == nil {
			return &entities.CampaignClaimResp{
				StepID:  stepID,
				GameID:  again.GameID,
				Already: true,
			}, nil
		}
		return nil, apperrors.WithError(err)
	}

	return &entities.CampaignClaimResp{StepID: stepID, GameID: giftID}, nil
}

/* ------------------------------------------------------------------ *
 * 随机奖励（第 5 关，2026-11-14）
 * ------------------------------------------------------------------ */

// errCampaignNoGiftLeft 随机奖励没有可送的了（全都拥有 / 全被明码送过）。
//
// ⚠️ 必须报错而不是「随便送一款已有的」—— 后者会让界面显示领取成功
//
//	而游戏柜没有变化，是这套功能里最坏的一种失败。
var errCampaignNoGiftLeft = apperrors.With("你已经把能送的游戏都拿到了，这一关先留着")

// pickRandomGift 挑一款这个用户还没有的游戏。
//
// 候选 = available 且非 default_owned 且未拥有 且**没被主线明码送过**。
// 排除「明码送过的」是因为第 5 关抽到第 1 关刚送的那款，孩子会觉得被耍了。
func (s *CampaignService) pickRandomGift(ctx context.Context, uid uint) (string, error) {
	all, _, err := s.GameRepo.ListGames(ctx, &entities.GameListReq{})
	if err != nil {
		return "", apperrors.WithError(err)
	}

	// 已拥有
	owned := map[string]bool{}
	var ugs []entities.UserGame
	if err := s.DB.WithContext(ctx).Where("uid = ?", uid).Find(&ugs).Error; err != nil {
		return "", apperrors.WithError(err)
	}
	for _, g := range ugs {
		owned[g.GameID] = true
	}

	// 主线明码送过的（随机关自己不算）
	fixed := map[string]bool{}
	for _, st := range entities.CampaignSteps() {
		if !st.RandomGift && st.GameID != "" {
			fixed[st.GameID] = true
		}
	}

	cands := make([]string, 0, len(all))
	for _, g := range all {
		if g.Status != entities.GameStatusAvailable {
			continue
		}
		if g.DefaultOwned || owned[g.GameID] || fixed[g.GameID] {
			continue
		}
		cands = append(cands, g.GameID)
	}
	if len(cands) == 0 {
		return "", errCampaignNoGiftLeft
	}

	// 随机：用 crypto/rand 取模会有偏，但这里只是给孩子抽个玩具，
	// 不是安全场景 —— math/rand 足够，且要的是可重放性 / 简单。
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(cands))))
	if err != nil {
		// 拿不到随机源就退到第一个：宁可确定性，也不能让领奖卡住
		return cands[0], nil
	}
	return cands[n.Int64()], nil
}
