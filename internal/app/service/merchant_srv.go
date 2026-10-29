package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"
	"tf-api/pkg/logger"

	"github.com/google/wire"
	"go.uber.org/zap"
)

var MerchantServiceSet = wire.NewSet(ProvideMerchantService)

// MerchantService 招商服务。
//
// 上游客户端**明确只做「提交」那一半**（`MerchantView.tsx` 文件头）：
//
//	「`approved` + `adminUrl` **只能由后端回填**。
//	  别在客户端自己推到 `approved` —— 别加「模拟审核通过」这种按钮：
//	  那等于给所有用户凭空发一个后台入口。」
//
// 所以这个服务负责那两件客户端**明确不自己造**的事：
// 审核 + 回填后台地址。
type MerchantService struct {
	Repo     *repository.MerchantRepository
	UserRepo *repository.UserRepository
	EventSrv *EventService
}

// ProvideMerchantService 构造函数
func ProvideMerchantService(
	repo *repository.MerchantRepository,
	userRepo *repository.UserRepository,
	eventSrv *EventService,
) *MerchantService {
	return &MerchantService{Repo: repo, UserRepo: userRepo, EventSrv: eventSrv}
}

// ============================ 客户端：提交 ============================

// Apply 提交/更新申请。
//
// ## 幂等语义（客户端明确要求）
//
// 「同一个角色重复提交 = **改申请**，不是提第二份 —— 后端那边也要按这个语义」
//
// 所以唯一索引在 `(uid, role)`，这里是 upsert。
//
// ## 改申请**不会**把审核结果冲掉
//
// upsert 只更新 `name` / `contact` / `note` —— `status` / `admin_url` /
// `reviewed_*` 不碰。
//
// 反例：家长提交后已通过审核（拿到后台地址），过几天回来改联系电话 ——
// 如果 upsert 把 status 重置成 pending、admin_url 清空，他就**莫名其妙
// 失去了后台入口**，而且不报错。
func (s *MerchantService) Apply(ctx context.Context, uid uint, req *entities.MerchantApplyReq) (*entities.MerchantApplicationView, error) {
	if !req.Role.IsValid() {
		return nil, apperrors.WithCode(apperrors.InvalidParam)
	}

	name, contact, err := s.identity(ctx, uid, req)
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	app := &entities.MerchantApplication{
		UID:     uid,
		Role:    req.Role,
		Status:  entities.MerchantStatusPending,
		Name:    name,
		Contact: contact,
		Note:    req.Note,
	}
	app.CreatedAt = now
	app.UpdatedAt = now

	if err := s.Repo.Upsert(ctx, app); err != nil {
		logger.ZError("merchant apply failed", zap.Uint("uid", uid), zap.String("role", string(req.Role)), zap.Error(err))
		return nil, apperrors.WithError(err)
	}

	// upsert 之后重新读 —— 拿的可能是「已存在那条」的审核结果
	fresh, err := s.Repo.Get(ctx, uid, req.Role)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	return s.toView(fresh), nil
}

// identity 决定这条申请上写谁、怎么联系。
//
// 2026-10-29 上游改口（`MerchantView.tsx` 第二版）：
//
//	「点申请即可。不需要填 称呼 这些。**因为会根据当前账号，申请**。」
//
// 所以 name / contact 都可以不传，缺的那一半由账号补：
//
//	name    = 昵称 → 用户名 → 「家长<uid>」
//	contact = 账号手机号
//
// ⚠️ 手机号**只能这里填**：登录响应里 `mobile` 是 `138****8000`（脱敏），
// 客户端手上没有明文，指望它补就是给审核的人一个假号码。
//
// ⚠️ 只补**缺的那一半** —— 商家显式传了店铺名就用店铺名。
func (s *MerchantService) identity(
	ctx context.Context,
	uid uint,
	req *entities.MerchantApplyReq,
) (string, string, error) {
	name := strings.TrimSpace(req.Name)
	contact := strings.TrimSpace(req.Contact)
	if name != "" && contact != "" {
		return name, contact, nil
	}

	u, err := s.UserRepo.GetByID(ctx, uid)
	if err != nil || u == nil {
		// 账号查不到就**不能编**一个联系方式 —— 审核的人会拿着假号码去联系
		logger.ZError("merchant apply: load user failed", zap.Uint("uid", uid), zap.Error(err))
		return "", "", apperrors.WithCode(apperrors.UserNotExist)
	}

	if name == "" {
		name = strings.TrimSpace(u.Nickname)
		if name == "" {
			name = strings.TrimSpace(u.Username)
		}
		if name == "" {
			name = fmt.Sprintf("家长%d", uid)
		}
	}
	if contact == "" {
		contact = strings.TrimSpace(u.Mobile)
	}
	return name, contact, nil
}

// My 查我的申请（客户端「招商」那一屏读这个）
func (s *MerchantService) My(ctx context.Context, uid uint) (*entities.MerchantMyResp, error) {
	list, err := s.Repo.ListByUID(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}

	out := make([]*entities.MerchantApplicationView, 0, len(list))
	for _, a := range list {
		out = append(out, s.toView(a))
	}
	return &entities.MerchantMyResp{Applications: out}, nil
}

// toView 转视图。
//
// ⚠️ **`AdminURL` 只在 `approved` 时给出** —— 服务端负责抑制，
// 不让客户端去判断「这个状态该不该渲染那个链接」。
// 客户端原话：那个区域 `pending` 时写的是「审核通过后，这里会出现一个后台链接」。
func (s *MerchantService) toView(a *entities.MerchantApplication) *entities.MerchantApplicationView {
	if a == nil {
		return nil
	}
	v := &entities.MerchantApplicationView{
		ID:         a.ID,
		Role:       a.Role,
		Status:     a.Status,
		StatusText: entities.MerchantStatusText[a.Status],
		Name:       a.Name,
		Contact:    a.Contact,
		Note:       a.Note,
		CreatedAt:  a.CreatedAt,
		ReviewedAt: a.ReviewedAt,
	}
	if a.Status == entities.MerchantStatusApproved {
		v.AdminURL = a.AdminURL
	}
	return v
}

// ============================ 后台：审核 ============================

// List 后台申请列表
func (s *MerchantService) List(ctx context.Context, req *entities.MerchantListReq) ([]*entities.MerchantApplicationView, int64, error) {
	list, total, err := s.Repo.List(ctx, repository.MerchantQuery{
		Role:     req.Role,
		Status:   req.Status,
		Keyword:  req.Keyword,
		Page:     req.Page,
		PageSize: req.PageSize,
	})
	if err != nil {
		return nil, 0, apperrors.WithError(err)
	}

	out := make([]*entities.MerchantApplicationView, 0, len(list))
	for _, a := range list {
		// 后台**要看得到** admin_url（运营要核对给他的是哪个地址），
		// 所以这里不走 toView 的抑制逻辑
		out = append(out, &entities.MerchantApplicationView{
			ID:         a.ID,
			Role:       a.Role,
			Status:     a.Status,
			StatusText: entities.MerchantStatusText[a.Status],
			Name:       a.Name,
			Contact:    a.Contact,
			Note:       a.Note,
			AdminURL:   a.AdminURL,
			CreatedAt:  a.CreatedAt,
			ReviewedAt: a.ReviewedAt,
		})
	}
	return out, total, nil
}

// Review 审核（通过 / 拒绝）。
//
// ## 通过时 `AdminURL` 必填
//
// 通过了却不给地址的话，客户端那一屏会**永远**停在
// 「审核通过后，这里会出现后台链接」—— 用户看到「已通过」却拿不到入口，
// 只能来问客服。所以这里直接拒绝这种调用。
//
// ## 并发：状态 CAS
//
// 只允许从 `pending` 审核。已通过的单子再点一次不该改动 `ReviewedAt`
// （审计上看不出谁什么时候审的），也不该被改成拒绝。
func (s *MerchantService) Review(ctx context.Context, req *entities.MerchantReviewReq, operatorID uint) (*entities.MerchantApplicationView, error) {
	target := entities.MerchantStatusApproved
	if !req.Approve {
		target = entities.MerchantStatusRejected
	}

	if req.Approve && req.AdminURL == "" {
		return nil, apperrors.WithCode(apperrors.MerchantAdminURLRequired)
	}

	// 先确认目标存在（拿到 uid 用于日志）
	cur, err := s.Repo.GetByID(ctx, req.ApplicationID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if cur == nil {
		return nil, apperrors.WithCode(apperrors.MerchantApplicationNotExist)
	}

	rows, err := s.Repo.ReviewCAS(ctx, cur.UID, req.ApplicationID, target,
		req.AdminURL, req.Note, operatorID, time.Now().Unix())
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	if rows == 0 {
		return nil, apperrors.WithCode(apperrors.MerchantAlreadyReviewed)
	}

	logger.ZInfo("merchant application reviewed",
		zap.Uint("application_id", req.ApplicationID),
		zap.String("role", string(cur.Role)),
		zap.String("status", string(target)),
		zap.Uint("operator", operatorID))

	s.EventSrv.Publish(MerchantReviewedEvent, map[string]interface{}{
		"application_id": req.ApplicationID,
		"uid":            cur.UID,
		"role":           string(cur.Role),
		"status":         string(target),
	})

	fresh, err := s.Repo.GetByID(ctx, req.ApplicationID)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	return s.toView(fresh), nil
}

// PendingCount 待审核数（后台红点）
func (s *MerchantService) PendingCount(ctx context.Context) (int64, error) {
	n, err := s.Repo.CountByStatus(ctx, entities.MerchantStatusPending)
	if err != nil {
		return 0, apperrors.WithError(err)
	}
	return n, nil
}

// InviteStats 家长那一侧的邀请统计。
//
// 客户端文案：「审核通过后，这里会出现一个后台链接，**能看邀请记录和分成**」。
// 分成金额要等结算规则定下来 —— 这里先给「邀请了几家、几家通过」，
// 够撑起那一屏，也不至于先造一套假的金额。
func (s *MerchantService) InviteStats(ctx context.Context, uid uint) (map[string]interface{}, error) {
	total, approved, err := s.Repo.CountInvitedBy(ctx, uid)
	if err != nil {
		return nil, apperrors.WithError(err)
	}
	return map[string]interface{}{
		"invited_total":    total,
		"invited_approved": approved,
		// ⚠️ 分成金额**故意留空** —— 结算规则还没定，先造一个数字
		// 会让家长以为「这就是我能拿到的钱」
		"share_amount": nil,
		"share_note":   "分成规则确定后开放",
	}, nil
}
