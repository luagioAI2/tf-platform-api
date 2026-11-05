package service

import (
	"context"
	"encoding/json"
	"errors"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"

	"github.com/google/wire"
)

var TemplateServiceSet = wire.NewSet(ProvideTemplateService)

// TemplateService 模板下发 + 邀请码归属 + 首次落地
type TemplateService struct {
	Repo *repository.TemplateRepository
}

func ProvideTemplateService(repo *repository.TemplateRepository) *TemplateService {
	return &TemplateService{Repo: repo}
}

// picked 解析出来的一份模板（模板头 + 条目）
type picked struct {
	tpl   *entities.Template
	items []entities.TemplateItem
}

// pick 给某个账号挑一份模板（按 kind）。
//
// 口径（用户选的）：商家默认模板优先，否则官方默认。
// 这是「选一个」，**不是**把官方和商家的条目叠加 —— 叠加会让「商家
// 只想换掉三条任务」变成「十六条全在」，而且谁覆盖谁说不清。
func (s *TemplateService) pick(ctx context.Context, kind entities.TemplateKind, sourceUID uint) (*picked, error) {
	tpl, err := s.Repo.DefaultFor(ctx, kind, sourceUID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if tpl == nil && sourceUID != 0 {
		// 该商家没配默认模板 ⇒ 回落官方
		tpl, err = s.Repo.DefaultFor(ctx, kind, 0)
		if err != nil {
			return nil, apperrors.WithError(err)
		}
	}
	if tpl == nil {
		return nil, nil
	}
	items, err := s.Repo.ItemsOf(ctx, tpl.ID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	return &picked{tpl: tpl, items: items}, nil
}

// Resolve 给当前用户挑一份模板（只读，不写任何实例）。
func (s *TemplateService) Resolve(ctx context.Context, uid uint, kind entities.TemplateKind) (*entities.TemplateResolveResp, error) {
	if !kind.IsValid() {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}

	sourceUID, err := s.Repo.SourceMerchantUID(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	p, err := s.pick(ctx, kind, sourceUID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		// 连官方种子都没有（AutoMigrate 关了 / 库是老的）：宁可报错，
		// 也不要静默返回空数组 —— 客户端会以为「模板就是空的」而铺出 0 条。
		return nil, apperrors.WithCode(apperrors.OperationFailed)
	}

	resp := &entities.TemplateResolveResp{
		TemplateID:        p.tpl.ID,
		Name:              p.tpl.Name,
		Kind:              string(p.tpl.Kind),
		Scope:             string(p.tpl.Scope),
		OwnerUID:          p.tpl.OwnerUID,
		IsDefault:         p.tpl.IsDefault,
		SourceMerchantUID: sourceUID,
		Items:             make([]map[string]interface{}, 0, len(p.items)),
	}
	for _, it := range p.items {
		m := map[string]interface{}{
			"item_id":         it.ItemID,
			"name":            it.Name,
			"emoji":           it.Emoji,
			"category":        it.Category,
			"cycle":           it.Cycle,
			"planned_minutes": it.PlannedMinutes,
			"base_points":     it.BasePoints,
			"price":           it.Price,
			"note":            it.Note,
			"limit_per_day":   it.LimitPerDay,
			"stock":           it.Stock,
			"once":            it.Once,
			"sort":            it.Sort,
		}
		// Extra 里是 App 原生的 camelCase 玩法参数。只补 typed 列里没有的键，
		// 所以后台改了「基础积分」之类不会被 Extra 里的旧值盖回去。
		if it.Extra != "" {
			var extra map[string]interface{}
			if json.Unmarshal([]byte(it.Extra), &extra) == nil {
				for k, v := range extra {
					m[k] = v
				}
			}
		}
		resp.Items = append(resp.Items, m)
	}
	return resp, nil
}

// Apply 按模板给账号**铺一次**初始任务 / 兑换清单。
//
// 为什么在服务端铺、而不是客户端写本地库：
// 在线版的数据源是账号（`task` / `redeem_item` 都带 uid），客户端写本地
// Dexie 的那几行服务端并不知道 —— 下一次 pull 就被覆盖掉，孩子看到的是
// 「一进来有 16 条任务，过两秒全没了」，而且不报错。
//
// 幂等由 `template_apply` 的 uid 唯一键兜住：重复调用直接返回 `already`。
func (s *TemplateService) Apply(ctx context.Context, uid uint) (*entities.TemplateApplyResp, error) {
	if uid == 0 {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}

	sourceUID, err := s.Repo.SourceMerchantUID(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	plan := repository.ApplyPlan{}
	if p, err := s.pick(ctx, entities.TemplateKindTask, sourceUID); err != nil {
		return nil, err
	} else if p != nil {
		plan.TaskTemplate, plan.TaskItems = p.tpl, p.items
	}
	if p, err := s.pick(ctx, entities.TemplateKindRedeem, sourceUID); err != nil {
		return nil, err
	} else if p != nil {
		plan.RedeemTemplate, plan.RedeemItems = p.tpl, p.items
	}

	row, err := s.Repo.ApplyOnce(ctx, uid, plan)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	resp := &entities.TemplateApplyResp{SourceMerchantUID: sourceUID}
	if row == nil {
		resp.Applied = false
		resp.Reason = entities.ApplyReasonAlready
		return resp, nil
	}
	resp.TaskTemplateID = row.TaskTemplateID
	resp.RedeemTemplateID = row.RedeemTemplateID
	resp.TaskCount = row.TaskCount
	resp.RedeemCount = row.RedeemCount
	resp.Reason = row.Skipped
	resp.Applied = row.TaskCount > 0 || row.RedeemCount > 0
	return resp, nil
}

// BindInvite 绑定邀请码，返回生效的商家 uid。
func (s *TemplateService) BindInvite(ctx context.Context, uid uint, code string) (uint, error) {
	merchantUID, err := s.Repo.ApplyInvite(ctx, uid, code)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrInvalidInvite),
			errors.Is(err, repository.ErrInviteMerchantNotApproved):
			return 0, apperrors.WithCode(apperrors.InvalidParam)
		default:
			return 0, apperrors.WithError(err)
		}
	}
	return merchantUID, nil
}
