package api

import (
	"tf-api/internal/app/entities"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var GameCoinAPISet = wire.NewSet(wire.Struct(new(GameCoinAPI), "*"))

// GameCoinAPI 游戏币接口
type GameCoinAPI struct {
	Srv *service.GameCoinService
}

// Info 查询单个游戏的币余额
// @Tags GameCoin[游戏币]
// @Summary 查询游戏币余额
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameKvListReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.GameCoinInfo}
// @Router /api/game/coin/info [post]
func (c *GameCoinAPI) Info(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.GameKvListReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	info, err := c.Srv.Info(ctx.Request.Context(), uid, req.GameID)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, info)
}

// ListInfo 查询所有游戏的币余额
// @Tags GameCoin[游戏币]
// @Summary 查询所有游戏币余额
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/game/coin/list [post]
func (c *GameCoinAPI) ListInfo(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	list, err := c.Srv.ListInfo(ctx.Request.Context(), uid)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"list": list, "count": len(list)})
}

// Buy 用积分买游戏币
// @Tags GameCoin[游戏币]
// @Summary 用积分买游戏币
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameCoinBuyReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.GameCoinResp}
// @Router /api/game/coin/buy [post]
func (c *GameCoinAPI) Buy(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.GameCoinBuyReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.Buy(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}

// Spend 消费游戏币
// @Tags GameCoin[游戏币]
// @Summary 消费游戏币（玩一局）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameCoinSpendReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.GameCoinResp}
// @Router /api/game/coin/spend [post]
func (c *GameCoinAPI) Spend(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.GameCoinSpendReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.Spend(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}

// Mint 铸币
// @Tags GameCoin[游戏币]
// @Summary 铸币（插件调用，只进不出）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameCoinMintReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.GameCoinResp}
// @Router /api/game/coin/mint [post]
func (c *GameCoinAPI) Mint(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.GameCoinMintReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.Mint(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}

// Redeem 游戏币换兑换券
// @Tags GameCoin[游戏币]
// @Summary 游戏币换兑换券
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameCoinRedeemReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.GameCoinResp}
// @Router /api/game/coin/redeem [post]
func (c *GameCoinAPI) Redeem(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.GameCoinRedeemReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.Redeem(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}

// Voucher 插件抓到奖品 → 直接发兑换券
// @Tags GameCoin[游戏币]
// @Summary 发兑换券（插件奖品，不经过游戏币）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameCoinVoucherReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.GameCoinVoucherResp}
// @Router /api/game/coin/voucher [post]
func (c *GameCoinAPI) Voucher(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.GameCoinVoucherReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.MintVoucher(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}

// Ledger 游戏币账本
// @Tags GameCoin[游戏币]
// @Summary 查询游戏币账本
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/game/coin/ledger [post]
func (c *GameCoinAPI) Ledger(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req struct {
		GameID   string `json:"game_id"`
		Page     int    `json:"page"`
		PageSize int    `json:"page_size"`
	}
	_ = ctx.ShouldBindJSON(&req)

	list, total, err := c.Srv.Ledger(ctx.Request.Context(), uid, req.GameID, req.Page, req.PageSize)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"count": total, "list": list})
}
