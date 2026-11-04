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

// TemplateService 模板下发 + 邀请码归属
type TemplateService struct {
	Repo *repository.TemplateRepository
}

func ProvideTemplateService(repo *repository.TemplateRepository) *TemplateService {
	return &TemplateService{Repo: repo}
}

// Resolve 给当前用户挑一份模板。
//
// 口径（用户选的）：商家默认模板优先，否则官方默认。
// 这是「选一个」，**不是**把官方和商家的条目叠加 —— 叠加会让「商家
// 只想换掉三条任务」变成「十六条全在」，而且谁覆盖谁说不清。
func (s *TemplateService) Resolve(ctx context.Context, uid uint, kind entities.TemplateKind) (*entities.TemplateResolveResp, error) {
	if !kind.IsValid() {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}

	sourceUID, err := s.Repo.SourceMerchantUID(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

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
		// 连官方种子都没有（AutoMigrate 关了 / 库是老的）：宁可报错，
		// 也不要静默返回空数组 —— 客户端会以为「模板就是空的」而铺出 0 条。
		return nil, apperrors.WithCode(apperrors.OperationFailed)
	}

	items, err := s.Repo.ItemsOf(ctx, tpl.ID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	resp := &entities.TemplateResolveResp{
		TemplateID:        tpl.ID,
		Name:              tpl.Name,
		Kind:              string(tpl.Kind),
		Scope:             string(tpl.Scope),
		OwnerUID:          tpl.OwnerUID,
		IsDefault:         tpl.IsDefault,
		SourceMerchantUID: sourceUID,
		Items:             make([]map[string]interface{}, 0, len(items)),
	}
	for _, it := range items {
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
