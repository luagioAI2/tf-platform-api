package api

import (
	"time"

	"tf-api/internal/app/entities"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var AuthAPISet = wire.NewSet(wire.Struct(new(AuthAPI), "*"))

// AuthAPI 认证接口：注册 / 登录 / 验证码 / 令牌
type AuthAPI struct {
	Srv *service.AuthService
}

// SendSmsCode 发送验证码
// @Tags Auth[认证]
// @Summary 发送短信验证码
// @Accept json
// @Produce json
// @Param req body entities.SendSmsCodeReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/auth/send-sms-code [post]
func (c *AuthAPI) SendSmsCode(ctx *gin.Context) {
	var req entities.SendSmsCodeReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	if err := c.Srv.SendSmsCode(ctx.Request.Context(), &req, ginx.GetClientIP(ctx)); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"sent": true})
}

// Register 手机号注册
// @Tags Auth[认证]
// @Summary 手机号注册
// @Accept json
// @Produce json
// @Param req body entities.RegisterReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.LoginResp}
// @Router /api/auth/register [post]
func (c *AuthAPI) Register(ctx *gin.Context) {
	var req entities.RegisterReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.Register(ctx.Request.Context(), &req,
		ginx.GetClientIP(ctx),
		ctx.GetHeader("User-Agent"),
		ctx.GetHeader("X-Device-ID"))
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.SetAuthTokens(ctx, resp.AccessToken, resp.RefreshToken)
	ginx.RespSucc(ctx, resp)
}

// Login 密码登录
// @Tags Auth[认证]
// @Summary 手机号 + 密码登录
// @Accept json
// @Produce json
// @Param req body entities.LoginReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.LoginResp}
// @Router /api/auth/login [post]
func (c *AuthAPI) Login(ctx *gin.Context) {
	var req entities.LoginReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.Login(ctx.Request.Context(), &req,
		ginx.GetClientIP(ctx),
		ctx.GetHeader("User-Agent"),
		ctx.GetHeader("X-Device-ID"))
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.SetAuthTokens(ctx, resp.AccessToken, resp.RefreshToken)
	ginx.RespSucc(ctx, resp)
}

// SmsLogin 验证码登录
// @Tags Auth[认证]
// @Summary 手机号 + 验证码登录（未注册自动注册）
// @Accept json
// @Produce json
// @Param req body entities.SmsLoginReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.LoginResp}
// @Router /api/auth/sms-login [post]
func (c *AuthAPI) SmsLogin(ctx *gin.Context) {
	var req entities.SmsLoginReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.SmsLogin(ctx.Request.Context(), &req,
		ginx.GetClientIP(ctx),
		ctx.GetHeader("User-Agent"),
		ctx.GetHeader("X-Device-ID"))
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.SetAuthTokens(ctx, resp.AccessToken, resp.RefreshToken)
	ginx.RespSucc(ctx, resp)
}

// WechatLogin 微信登录
// @Tags Auth[认证]
// @Summary 微信登录（未绑定则自动注册）
// @Accept json
// @Produce json
// @Param req body entities.WechatLoginReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.LoginResp}
// @Router /api/auth/wechat-login [post]
func (c *AuthAPI) WechatLogin(ctx *gin.Context) {
	var req entities.WechatLoginReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.WechatLogin(ctx.Request.Context(), &req,
		ginx.GetClientIP(ctx),
		ctx.GetHeader("User-Agent"),
		ctx.GetHeader("X-Device-ID"))
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.SetAuthTokens(ctx, resp.AccessToken, resp.RefreshToken)
	ginx.RespSucc(ctx, resp)
}

// BindMobile 绑定手机号
// @Tags Auth[认证]
// @Summary 绑定手机号（微信用户）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.BindMobileReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/auth/bind-mobile [post]
func (c *AuthAPI) BindMobile(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.BindMobileReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	if err := c.Srv.BindMobile(ctx.Request.Context(), uid, &req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"bound": true})
}

// Refresh 刷新令牌
// @Tags Auth[认证]
// @Summary 刷新 access token
// @Accept json
// @Produce json
// @Param req body entities.RefreshTokenReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.LoginResp}
// @Router /api/auth/refresh [post]
func (c *AuthAPI) Refresh(ctx *gin.Context) {
	var req entities.RefreshTokenReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.Refresh(ctx.Request.Context(), req.RefreshToken)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.SetAuthTokens(ctx, resp.AccessToken, resp.RefreshToken)
	ginx.RespSucc(ctx, resp)
}

// Logout 登出
// @Tags Auth[认证]
// @Summary 登出
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/auth/logout [post]
func (c *AuthAPI) Logout(ctx *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = ctx.ShouldBindJSON(&req)

	jti := ctx.GetString("tokenJTI")
	if err := c.Srv.Logout(ctx.Request.Context(), req.RefreshToken, jti, 2*time.Hour); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"logged_out": true})
}

// ResetPassword 重置密码
// @Tags Auth[认证]
// @Summary 重置密码（验证码）
// @Accept json
// @Produce json
// @Param req body entities.ResetPasswordReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/auth/reset-password [post]
func (c *AuthAPI) ResetPassword(ctx *gin.Context) {
	var req entities.ResetPasswordReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	if err := c.Srv.ResetPassword(ctx.Request.Context(), &req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"reset": true})
}

// ChangePassword 修改密码
// @Tags Auth[认证]
// @Summary 修改密码（需旧密码）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.ChangePasswordReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/auth/change-password [post]
func (c *AuthAPI) ChangePassword(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.ChangePasswordReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	if err := c.Srv.ChangePassword(ctx.Request.Context(), uid, &req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"changed": true})
}
