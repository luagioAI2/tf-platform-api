package api

import (
	"tf-api/internal/app/entities"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var UserAPISet = wire.NewSet(wire.Struct(new(UserAPI), "*"))

// UserAPI 用户资料接口
type UserAPI struct {
	Srv *service.UserService
}

// Profile 获取当前用户资料
// @Tags User[用户]
// @Summary 获取用户资料
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp{data=entities.UserInfo}
// @Router /api/user/profile [post]
func (c *UserAPI) Profile(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	info, err := c.Srv.GetProfile(ctx.Request.Context(), uid)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, info)
}

// UpdateProfile 更新资料
// @Tags User[用户]
// @Summary 更新用户资料
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.UpdateProfileReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.UserInfo}
// @Router /api/user/update-profile [post]
func (c *UserAPI) UpdateProfile(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.UpdateProfileReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	info, err := c.Srv.UpdateProfile(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, info)
}
