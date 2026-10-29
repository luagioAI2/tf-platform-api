package api

import (
	"tf-api/internal/app/entities"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var MerchantAPISet = wire.NewSet(wire.Struct(new(MerchantAPI), "*"))

// MerchantAPI 招商接口。
//
// ## 分工（上游客户端把这一半明确留出来了）
//
// 客户端 `MerchantView.tsx` 只做「**提交**」：
// 写一条 `status: 'pending'`，并明确说
//
//	「`approved` + `adminUrl` **只能由后端回填**。
//	  别在客户端自己推到 `approved` —— 那等于给所有用户凭空发一个后台入口。」
//
// 所以这里提供两件事：
//   - **客户端**：提交 / 查我的（`/api/merchant/*`）
//   - **后台**：列表 / 审核（`/api/admin/merchant/*`，走后台令牌）
type MerchantAPI struct {
	Srv *service.MerchantService
}

// ============================ 客户端 ============================

// Apply 提交/更新招商申请
// @Tags Merchant[招商]
// @Summary 提交招商申请（同角色重复提交 = 改申请）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.MerchantApplyReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.MerchantApplicationView}
// @Router /api/merchant/apply [post]
func (c *MerchantAPI) Apply(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.MerchantApplyReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	view, err := c.Srv.Apply(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, view)
}

// My 查我的申请
// @Tags Merchant[招商]
// @Summary 我的招商申请（含审核结果与后台地址）
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp{data=entities.MerchantMyResp}
// @Router /api/merchant/my [post]
func (c *MerchantAPI) My(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	resp, err := c.Srv.My(ctx.Request.Context(), uid)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}

// InviteStats 家长的邀请统计（那一屏写的「邀请记录和分成」）
// @Tags Merchant[招商]
// @Summary 邀请统计（家长侧）
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/merchant/invite-stats [post]
func (c *MerchantAPI) InviteStats(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	stats, err := c.Srv.InviteStats(ctx.Request.Context(), uid)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, stats)
}

// ============================ 后台 ============================

// List 申请列表
// @Tags Merchant[招商]
// @Summary 招商申请列表（后台）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/admin/merchant/list [post]
func (c *MerchantAPI) List(ctx *gin.Context) {
	var req entities.MerchantListReq
	_ = ctx.ShouldBindJSON(&req)

	list, total, err := c.Srv.List(ctx.Request.Context(), &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{
		"page": req.Page, "pageSize": req.PageSize,
		"count": total, "list": list,
	})
}

// Review 审核
// @Tags Merchant[招商]
// @Summary 审核招商申请（后台；通过时必须填后台地址）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.MerchantReviewReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.MerchantApplicationView}
// @Router /api/admin/merchant/review [post]
func (c *MerchantAPI) Review(ctx *gin.Context) {
	var req entities.MerchantReviewReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	// 后台令牌场景下没有后台用户 ID，先传 0（审计用；接上后台账号后换成真 ID）
	view, err := c.Srv.Review(ctx.Request.Context(), &req, 0)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, view)
}

// PendingCount 待审核数
// @Tags Merchant[招商]
// @Summary 待审核数（后台红点）
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/admin/merchant/pending-count [post]
func (c *MerchantAPI) PendingCount(ctx *gin.Context) {
	n, err := c.Srv.PendingCount(ctx.Request.Context())
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"pending": n})
}
