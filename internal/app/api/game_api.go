package api

import (
	"tf-api/internal/app/entities"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var GameAPISet = wire.NewSet(wire.Struct(new(GameAPI), "*"))

// GameAPI 游戏商城接口
type GameAPI struct {
	Srv *service.GameService
}

// List 游戏列表（登录可选：未登录也能看列表，但 owned 全为 false）
// @Tags Game[游戏]
// @Summary 游戏列表
// @Accept json
// @Produce json
// @Param req body entities.GameListReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/game/list [post]
func (c *GameAPI) List(ctx *gin.Context) {
	var req entities.GameListReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	uid := ginx.Mine(ctx) // 可为 0（未登录）

	items, total, err := c.Srv.List(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{
		"page":     req.Page,
		"pageSize": req.PageSize,
		"count":    total,
		"list":     items,
	})
}

// Detail 游戏详情
// @Tags Game[游戏]
// @Summary 游戏详情
// @Accept json
// @Produce json
// @Param req body object{game_id=string} true "参数"
// @Success 200 {object} ginx.Resp{data=entities.GameDetail}
// @Router /api/game/detail [post]
func (c *GameAPI) Detail(ctx *gin.Context) {
	var req struct {
		GameID string `json:"game_id" binding:"required,max=32"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	uid := ginx.Mine(ctx)

	detail, err := c.Srv.Detail(ctx.Request.Context(), uid, req.GameID)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, detail)
}

// Purchase 购买游戏
// @Tags Game[游戏]
// @Summary 购买游戏
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.PurchaseGameReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.PurchaseGameResp}
// @Router /api/game/purchase [post]
func (c *GameAPI) Purchase(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.PurchaseGameReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.Purchase(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}

// MyGames 我拥有的游戏
// @Tags Game[游戏]
// @Summary 我拥有的游戏
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/game/my-games [post]
func (c *GameAPI) MyGames(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	items, err := c.Srv.MyGames(ctx.Request.Context(), uid)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"list": items, "count": len(items)})
}
