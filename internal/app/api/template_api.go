package api

import (
	"tf-api/internal/app/entities"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var TemplateAPISet = wire.NewSet(wire.Struct(new(TemplateAPI), "*"))

// TemplateAPI 模板下发 / 邀请码
type TemplateAPI struct {
	Srv *service.TemplateService
}

// Resolve 取当前用户该用的那份模板
// @Tags Template[模板]
// @Summary 解析任务/兑换模板
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.ResolveTemplateReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.TemplateResolveResp}
// @Router /api/template/resolve [post]
func (c *TemplateAPI) Resolve(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.ResolveTemplateReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.Resolve(ctx.Request.Context(), uid, req.Kind)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}

// BindInvite 注册流程里带上邀请码。
//
// 单独一个入口（而不是改 AuthService 的四个建号分支）是因为游客、
// 手机号注册、微信登录是三条完全不同的建号路径，每条都塞一遍解析逻辑
// 迟早漏一条；统一在「拿到 token 之后调一次」最可靠。
// @Tags Template[模板]
// @Summary 绑定商家邀请码
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.BindInviteReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/template/bind-invite [post]
func (c *TemplateAPI) BindInvite(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.BindInviteReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	merchantUID, err := c.Srv.BindInvite(ctx.Request.Context(), uid, req.InviteCode)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{
		"source_merchant_uid": merchantUID,
		"invite_code":         entities.MakeInviteCode(merchantUID),
	})
}

// InviteCode 返回「当前登录账号作为商家」的邀请码。
//
// 商家后台把这个码印在推广物料上；家长/普通用户调它也能拿到自己的码，
// 但那个码在 ApplyInvite 里过不了「已审核商家」那道闸，等于没用。
// @Tags Template[模板]
// @Summary 我的邀请码
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/template/invite-code [post]
func (c *TemplateAPI) InviteCode(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	ginx.RespSucc(ctx, gin.H{
		"uid":         uid,
		"invite_code": entities.MakeInviteCode(uid),
	})
}

// Apply 按模板给这个账号铺一次初始任务 / 兑换清单。
//
// 为什么单独一个入口、而不是在建号时顺手铺：
//   · 建号有**三条**路径（游客 / 手机号注册 / 微信），每条都塞一遍迟早漏一条；
//   · 邀请码是「拿到 token 之后」才绑的，建号那一刻还不知道该用谁的模板。
// 所以统一成「登录后调一次」，幂等由 template_apply 的 uid 唯一键兜住 ——
// 客户端每次启动都调也没关系（第二次起返回 applied=false）。
// @Tags Template[模板]
// @Summary 首次落地模板（幂等）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp{data=entities.TemplateApplyResp}
// @Router /api/template/apply [post]
func (c *TemplateAPI) Apply(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	resp, err := c.Srv.Apply(ctx.Request.Context(), uid)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}
