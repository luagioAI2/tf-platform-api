package api

import (
	"tf-api/internal/app/entities"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var WishAPISet = wire.NewSet(wire.Struct(new(WishAPI), "*"))

// WishAPI 心愿商城接口。
//
// ⚠️ 孩子端 / 家长端的差别必须在**服务端**区分，不能靠前端藏：
//   - 外链（`BuyURL`）只在家长端返回
//   - 收货信息在孩子端要脱敏
//
// 判据是请求里的 `as_parent`。生产环境应由「家长身份验证」决定 ——
// 本项目的家长 PIN 还没实现，所以这里先用显式标记，
// 并在文档里写明「`as_parent` 不可信，上生产前必须接真正的家长校验」。
type WishAPI struct {
	Srv *service.WishService
}

// ============================ 商品 ============================

// ListItems 心愿商品列表
// @Tags Wish[心愿商城]
// @Summary 心愿商品列表
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.ListWishItemReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/wish/list [post]
func (c *WishAPI) ListItems(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.ListWishItemReq
	_ = ctx.ShouldBindJSON(&req)

	list, err := c.Srv.ListItems(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"list": list, "count": len(list)})
}

// Detail 心愿商品详情
// @Tags Wish[心愿商城]
// @Summary 心愿商品详情（家长端含外链）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp{data=entities.WishItemDetailResp}
// @Router /api/wish/detail [post]
func (c *WishAPI) Detail(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req struct {
		ItemID   string `json:"item_id" binding:"required,max=64"`
		AsParent bool   `json:"as_parent"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	detail, err := c.Srv.Detail(ctx.Request.Context(), uid, req.ItemID, req.AsParent)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, detail)
}

// ============================ 家长定价 ============================

// SetOffer 家长定价 / 开放商品
// @Tags Wish[心愿商城]
// @Summary 家长定价（0 = 关闭该商品）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.WishSetOfferReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.WishOffer}
// @Router /api/wish/set-offer [post]
func (c *WishAPI) SetOffer(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.WishSetOfferReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	offer, err := c.Srv.SetOffer(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, offer)
}

// ============================ 孩子操作 ============================

// Wish 表态「想要这个」
// @Tags Wish[心愿商城]
// @Summary 想要这个（不扣券）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.WishReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.WishOrder}
// @Router /api/wish/wish [post]
func (c *WishAPI) Wish(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.WishReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	order, err := c.Srv.Wish(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, order)
}

// Redeem 确定兑换（扣券 + 交付）
// @Tags Wish[心愿商城]
// @Summary 确定兑换（扣券，数字商品当场交付）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.WishRedeemReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.WishOrderView}
// @Router /api/wish/redeem [post]
func (c *WishAPI) Redeem(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.WishRedeemReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	view, err := c.Srv.Redeem(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, view)
}

// ============================ 状态推进（家长） ============================

// Confirm 家长确认（已去外链购买）
// @Tags Wish[心愿商城]
// @Summary 家长确认
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.WishHandleReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.WishOrderView}
// @Router /api/wish/confirm [post]
func (c *WishAPI) Confirm(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.WishHandleReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	view, err := c.Srv.Confirm(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, view)
}

// Done 家长收尾
// @Tags Wish[心愿商城]
// @Summary 家长收尾（已买到 / 已收到）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.WishHandleReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.WishOrderView}
// @Router /api/wish/done [post]
func (c *WishAPI) Done(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.WishHandleReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	view, err := c.Srv.Done(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, view)
}

// Cancel 取消（退券 + 收回游戏）
// @Tags Wish[心愿商城]
// @Summary 取消心愿（退券；数字商品同时收回游戏）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.WishHandleReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.WishOrderView}
// @Router /api/wish/cancel [post]
func (c *WishAPI) Cancel(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.WishHandleReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	view, err := c.Srv.Cancel(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, view)
}

// ============================ 查询 ============================

// Orders 心愿单列表
// @Tags Wish[心愿商城]
// @Summary 心愿单列表
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.ListWishOrderReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/wish/orders [post]
func (c *WishAPI) Orders(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req struct {
		entities.ListWishOrderReq
		AsParent bool `json:"as_parent"`
	}
	_ = ctx.ShouldBindJSON(&req)

	list, total, err := c.Srv.ListOrders(ctx.Request.Context(), uid, &req.ListWishOrderReq, req.AsParent)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{
		"page": req.Page, "pageSize": req.PageSize,
		"count": total, "list": list,
	})
}

// OrderDetail 心愿单详情
// @Tags Wish[心愿商城]
// @Summary 心愿单详情
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp{data=entities.WishOrderView}
// @Router /api/wish/order-detail [post]
func (c *WishAPI) OrderDetail(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req struct {
		OrderID  string `json:"order_id" binding:"required,max=64"`
		AsParent bool   `json:"as_parent"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	view, err := c.Srv.OrderView(ctx.Request.Context(), uid, req.OrderID, req.AsParent)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, view)
}

// Pending 家长待办。
// @Tags Wish[心愿商城]
// @Summary 家长待办（wished 与 committed 分开计数）
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/wish/pending [post]
func (c *WishAPI) Pending(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	tasks, err := c.Srv.PendingTasks(ctx.Request.Context(), uid)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, tasks)
}

// ============================ 兑换码 ============================

// RedeemCode 兑换码解锁
// @Tags Wish[心愿商城]
// @Summary 用兑换码解锁
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.WishCodeRedeemReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.WishCodeResp}
// @Router /api/wish/redeem-code [post]
func (c *WishAPI) RedeemCode(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.WishCodeRedeemReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.RedeemCode(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}
