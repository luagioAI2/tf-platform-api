package api

import (
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var UserSettingsAPISet = wire.NewSet(wire.Struct(new(UserSettingsAPI), "*"))

// UserSettingsAPI 家长设置接口
type UserSettingsAPI struct {
	Srv *service.UserSettingsService
}

// SettingsSaveReq 保存设置
type SettingsSaveReq struct {
	// Payload 设置本体（JSON 字符串）。**服务端不解释字段**，只校验形状与大小。
	Payload string `json:"payload" binding:"required"`
	// Version 客户端自己的版本号（排查「谁覆盖了谁」用，可不传）
	Version int64 `json:"version"`
}

// SettingsResp 设置响应
type SettingsResp struct {
	// Payload 设置本体。`null` = 这个账号还没设过（用客户端默认值）
	Payload string `json:"payload"`
	// UpdatedAt 服务端最后写入时间（Unix 秒）
	UpdatedAt int64 `json:"updated_at"`
	// Version 写入时带的版本号
	Version int64 `json:"version"`
}

// Get 读设置
// @Tags User[用户]
// @Summary 读家长设置（整份 JSON）
// @Produce json
// @Security ApiKeyAuth
// @Router /api/user/settings/get [post]
func (c *UserSettingsAPI) Get(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	row, err := c.Srv.Get(ctx.Request.Context(), uid)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	if row == nil {
		// 没设过：返回空 payload 而不是 404 —— 「还没设」不是错误，
		// 客户端拿到空串就用默认值，别让它为此写一个 catch。
		ginx.RespSucc(ctx, SettingsResp{})
		return
	}
	ginx.RespSucc(ctx, SettingsResp{
		Payload:   row.Payload,
		UpdatedAt: row.UpdatedAt,
		Version:   row.Version,
	})
}

// Save 保存设置（整份覆盖）
// @Tags User[用户]
// @Summary 保存家长设置（整份覆盖）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param data body SettingsSaveReq true "设置"
// @Router /api/user/settings/save [post]
func (c *UserSettingsAPI) Save(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req SettingsSaveReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	row, err := c.Srv.Save(ctx.Request.Context(), uid, req.Payload, req.Version)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, SettingsResp{
		Payload:   row.Payload,
		UpdatedAt: row.UpdatedAt,
		Version:   row.Version,
	})
}
