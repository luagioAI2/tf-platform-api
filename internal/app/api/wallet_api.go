package api

import (
	"tf-api/internal/app/entities"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var WalletAPISet = wire.NewSet(wire.Struct(new(WalletAPI), "*"))

// WalletAPI 钱包接口
type WalletAPI struct {
	Srv *service.WalletService
}

// Info 获取钱包余额
// @Tags Wallet[钱包]
// @Summary 获取钱包余额（三套币）
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp{data=entities.WalletInfo}
// @Router /api/wallet/info [post]
func (c *WalletAPI) Info(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	info, err := c.Srv.GetWallet(ctx.Request.Context(), uid)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, info)
}

// Spend 扣积分（插件游戏开局这类「游戏内消费」）
// @Tags Wallet[钱包]
// @Summary 扣积分
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.WalletSpendReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.WalletInfo}
// @Router /api/wallet/spend [post]
func (c *WalletAPI) Spend(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.WalletSpendReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	info, err := c.Srv.SpendPoints(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, info)
}

// Ledger 查询账本流水
// @Tags Wallet[钱包]
// @Summary 查询账本流水
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.LedgerListReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/wallet/ledger [post]
func (c *WalletAPI) Ledger(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.LedgerListReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	if err := c.Srv.ListLedger(ctx.Request.Context(), uid, &req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{
		"page":     req.Page,
		"pageSize": req.PageSize,
		"count":    req.Count,
		"list":     req.List,
	})
}
