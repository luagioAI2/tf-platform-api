package api

import (
	"tf-api/internal/app/entities"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var RedeemAPISet = wire.NewSet(wire.Struct(new(RedeemAPI), "*"))

// RedeemAPI 兑换商城接口。
//
// 权限口径：
//   - 商品增删改 = 家长
//   - 列表 / 兑换 = 孩子（家长也能看）
//   - 兑现 / 取消 = 家长
type RedeemAPI struct {
	Srv *service.RedeemService
}

// ============================ 商品管理（家长） ============================

// CreateItem 创建兑换商品
// @Tags Redeem[兑换商城]
// @Summary 创建兑换商品（家长）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.CreateRedeemItemReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.RedeemItem}
// @Router /api/redeem/item/create [post]
func (c *RedeemAPI) CreateItem(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.CreateRedeemItemReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	item, err := c.Srv.CreateItem(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, item)
}

// UpdateItem 更新兑换商品
// @Tags Redeem[兑换商城]
// @Summary 更新兑换商品（家长）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.UpdateRedeemItemReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.RedeemItem}
// @Router /api/redeem/item/update [post]
func (c *RedeemAPI) UpdateItem(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req struct {
		ItemID string `json:"item_id" binding:"required,max=64"`
		entities.UpdateRedeemItemReq
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	item, err := c.Srv.UpdateItem(ctx.Request.Context(), uid, req.ItemID, &req.UpdateRedeemItemReq)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, item)
}

// DeleteItem 删除兑换商品
// @Tags Redeem[兑换商城]
// @Summary 删除兑换商品（家长）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/redeem/item/delete [post]
func (c *RedeemAPI) DeleteItem(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req struct {
		ItemID string `json:"item_id" binding:"required,max=64"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	if err := c.Srv.DeleteItem(ctx.Request.Context(), uid, req.ItemID); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"deleted": true})
}

// ============================ 列表与兑换 ============================

// ListItems 可兑换商品列表
// @Tags Redeem[兑换商城]
// @Summary 可兑换商品列表（含分账预览）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.ListRedeemItemReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/redeem/list [post]
func (c *RedeemAPI) ListItems(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.ListRedeemItemReq
	_ = ctx.ShouldBindJSON(&req)

	list, err := c.Srv.ListItems(ctx.Request.Context(), uid, req.IncludeArchived)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"list": list, "count": len(list)})
}

// Redeem 兑换
// @Tags Redeem[兑换商城]
// @Summary 兑换（券优先 + 积分补差）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.RedeemReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.RedeemResultResp}
// @Router /api/redeem/redeem [post]
func (c *RedeemAPI) Redeem(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.RedeemReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	res, err := c.Srv.Redeem(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, res)
}

// ============================ 记录与处理 ============================

// ListRecords 兑换记录
// @Tags Redeem[兑换商城]
// @Summary 兑换记录
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.ListRedeemRecordReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/redeem/records [post]
func (c *RedeemAPI) ListRecords(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.ListRedeemRecordReq
	_ = ctx.ShouldBindJSON(&req)

	list, total, err := c.Srv.ListRecords(ctx.Request.Context(), uid, req.Page, req.PageSize)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{
		"page": req.Page, "pageSize": req.PageSize,
		"count": total, "list": list,
	})
}

// Fulfill 标记已兑现
// @Tags Redeem[兑换商城]
// @Summary 标记已兑现（家长）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.RedeemHandleReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.RedeemRecord}
// @Router /api/redeem/fulfill [post]
func (c *RedeemAPI) Fulfill(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.RedeemHandleReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	rec, err := c.Srv.Fulfill(ctx.Request.Context(), uid, req.RecordID, req.Note)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, rec)
}

// Cancel 取消兑换（退券退分）
// @Tags Redeem[兑换商城]
// @Summary 取消兑换并退款（家长）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.RedeemHandleReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.RedeemRecord}
// @Router /api/redeem/cancel [post]
func (c *RedeemAPI) Cancel(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.RedeemHandleReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	rec, err := c.Srv.Cancel(ctx.Request.Context(), uid, req.RecordID, req.Reason)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, rec)
}

// Stats 兑换统计
// @Tags Redeem[兑换商城]
// @Summary 兑换统计（家长看板）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.RedeemStatsReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/redeem/stats [post]
func (c *RedeemAPI) Stats(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.RedeemStatsReq
	_ = ctx.ShouldBindJSON(&req)

	stats, err := c.Srv.Stats(ctx.Request.Context(), uid, req.Days)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, stats)
}
