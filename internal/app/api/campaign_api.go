package api

import (
	"tf-api/internal/app/entities"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var CampaignAPISet = wire.NewSet(wire.Struct(new(CampaignAPI), "*"))

// CampaignAPI 主线活动（客户端 `/api/campaign/*`）。
//
// 只有两个动作：
//   - `State`  领了哪几关 + 服务端算的进度
//   - `Claim`  领一关（**服务端复核条件并授游戏**）
//
// ⚠️ 没有「上报进度」这种接口：进度由服务端从 `task_instance` 和账本现算。
//
//	加一个「客户端上报」的口子，等于把「送不送游戏」的决定权交给客户端。
type CampaignAPI struct {
	Srv *service.CampaignService
}

// State 主线活动状态
// @Tags Campaign[主线活动]
// @Summary 活动状态（领了哪几关 + 进度）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp{data=entities.CampaignStateResp}
// @Router /api/campaign/state [post]
func (c *CampaignAPI) State(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	resp, err := c.Srv.State(ctx.Request.Context(), uid)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}

// Claim 领一关奖励
// @Tags Campaign[主线活动]
// @Summary 领取一关奖励（送一款游戏）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.CampaignClaimReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.CampaignClaimResp}
// @Router /api/campaign/claim [post]
func (c *CampaignAPI) Claim(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.CampaignClaimReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.Claim(ctx.Request.Context(), uid, req.StepID)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}
