package api

import (
	"tf-api/internal/app/entities"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var FarmAPISet = wire.NewSet(wire.Struct(new(FarmAPI), "*"))

// FarmAPI 农场状态接口。
//
// 只有两个动作：整块读、整块写。没有「种一块地」「收一只鸡」这种细粒度接口 ——
// 玩法规则在客户端，服务端只存结果（原因见 service.FarmService 的注释）。
type FarmAPI struct {
	Srv *service.FarmService
}

// Get 读取农场状态
// @Tags Farm[农场]
// @Summary 读取农场状态
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp{data=entities.FarmStateGetResp}
// @Router /api/farm/state/get [post]
func (c *FarmAPI) Get(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	resp, err := c.Srv.Get(ctx.Request.Context(), uid)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}

// Save 保存农场状态（可同时提交账本变动）
// @Tags Farm[农场]
// @Summary 保存农场状态
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.FarmStateSaveReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.FarmStateSaveResp}
// @Router /api/farm/state/save [post]
func (c *FarmAPI) Save(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.FarmStateSaveReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.Save(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}
