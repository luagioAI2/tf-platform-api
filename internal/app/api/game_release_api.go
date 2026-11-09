package api

import (
	"fmt"
	"strings"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var GameReleaseAPISet = wire.NewSet(wire.Struct(new(GameReleaseAPI), "*"))

// GameReleaseAPI 游戏版本发布与分发接口。
//
// 分工：
//   - 发布 / 列表 / 删除 = 管理端（走后台令牌）
//   - 目录 / 更新检查 / 下载 = 客户端（公开或需登录）
type GameReleaseAPI struct {
	Srv *service.GameReleaseService
}

// ============================ 客户端：目录与下载 ============================

// Catalog 返回签名后的 catalog.json。
//
// ⚠️ **公开接口**（不挂 JWT）：客户端在装包前就要拿到目录，
// 而且目录地址是**烧进 APK** 的，不存在「先登录再拉目录」的时机。
//
// 安全靠两件事，不是靠鉴权：
//  1. Ed25519 签名 —— 中间人改不了目录内容
//  2. 客户端验包 SHA-256 —— 包体被换会被发现
//
// @Tags GameRelease[游戏分发]
// @Summary 游戏目录（catalog.json，Ed25519 已签名）
// @Produce json
// @Success 200 {string} string "catalog.json 原始字节"
// @Router /api/game/catalog [get]
func (c *GameReleaseAPI) Catalog(ctx *gin.Context) {
	channel := ctx.Query("channel")

	body, _, err := c.Srv.Catalog(ctx.Request.Context(), channel)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}

	// 原样返回字节 —— 客户端要对**这串字节**验签，不能经过任何重编码
	ctx.Header("Cache-Control", "public, max-age=60")
	ctx.Data(200, "application/json; charset=utf-8", body)
}

// CatalogSig 返回目录签名（`.sig` 文件内容）。
//
// 格式：**单行 base64**（前端 `decodeSignature` 用 `atob` 解，换行会解出垃圾）
//
// @Tags GameRelease[游戏分发]
// @Summary 游戏目录签名（单行 base64）
// @Produce text/plain
// @Success 200 {string} string "base64 签名"
// @Router /api/game/catalog.sig [get]
func (c *GameReleaseAPI) CatalogSig(ctx *gin.Context) {
	channel := ctx.Query("channel")

	_, sig, err := c.Srv.Catalog(ctx.Request.Context(), channel)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}

	ctx.Header("Cache-Control", "public, max-age=60")
	// 不写换行：`atob` 对换行敏感
	ctx.Data(200, "text/plain; charset=utf-8", []byte(sig))
}

// CheckUpdate 检查更新
// @Tags GameRelease[游戏分发]
// @Summary 检查更新
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameUpdateCheckReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.GameUpdateCheckResp}
// @Router /api/game/update-check [post]
func (c *GameReleaseAPI) CheckUpdate(ctx *gin.Context) {
	var req entities.GameUpdateCheckReq
	_ = ctx.ShouldBindJSON(&req)

	resp, err := c.Srv.CheckUpdate(ctx.Request.Context(), &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}

// Download 获取下载地址
// @Tags GameRelease[游戏分发]
// @Summary 获取游戏包下载地址（校验是否已拥有）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameDownloadReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.GameDownloadResp}
// @Router /api/game/download [post]
func (c *GameReleaseAPI) Download(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.GameDownloadReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	resp, err := c.Srv.Download(ctx.Request.Context(), uid, &req)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, resp)
}

// ============================ 管理端 ============================

// Publish 发布版本
// @Tags GameRelease[游戏分发]
// @Summary 发布游戏版本（管理端）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.PublishReleaseReq true "参数"
// @Success 200 {object} ginx.Resp{data=entities.GameRelease}
// @Router /api/game/admin/release/publish [post]
func (c *GameReleaseAPI) Publish(ctx *gin.Context) {
	var req entities.PublishReleaseReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	// operatorID：后台令牌场景下由中间件写入；这里取 0 表示未知
	rel, err := c.Srv.Publish(ctx.Request.Context(), &req, 0)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, rel)
}

// Upload 上传包并发布（multipart）。
//
// 和 `Publish` 的区别只有一件事：**包从哪来**。
// 这里收文件、落盘、算哈希，再交给同一个 `Publish` ——
// 所以「上传发的版」和「手填 URL 发的版」在库里长得一样。
//
// 表单字段：
//
//	game_id / version / channel / release_notes / forced / min_host_api / file
//
// `file` 只收 `.mjs` / `.js` / `.zip`（zip 里是 `<id>.plugin.mjs` + `assets/`）。
// URL / SHA-256 / 体积由服务端算，**表单里给了也会被覆盖**。
//
// @Tags GameRelease[游戏分发]
// @Summary 上传游戏包并发布（管理端）
// @Accept multipart/form-data
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp{data=entities.GameRelease}
// @Router /api/admin/game/release/upload [post]
func (c *GameReleaseAPI) Upload(ctx *gin.Context) {
	var req entities.PublishReleaseReq
	req.GameID = ctx.PostForm("game_id")
	req.Version = ctx.PostForm("version")
	req.Channel = ctx.PostForm("channel")
	req.ReleaseNotes = ctx.PostForm("release_notes")
	req.Forced = ctx.PostForm("forced") == "1" || ctx.PostForm("forced") == "true"
	if v := ctx.PostForm("min_host_api"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			req.MinHostApi = n
		}
	}
	if strings.TrimSpace(req.GameID) == "" || strings.TrimSpace(req.Version) == "" {
		ginx.RespErr(ctx, apperrors.WithCode(apperrors.InvalidParam))
		return
	}

	fh, err := ctx.FormFile("file")
	if err != nil {
		ginx.RespErr(ctx, apperrors.WithCode(apperrors.InvalidParam))
		return
	}

	rel, err := c.Srv.PublishFromUpload(ctx.Request.Context(), &req, fh, 0)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, rel)
}

// List 版本列表
// @Tags GameRelease[游戏分发]
// @Summary 版本列表（管理端）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.ListReleaseReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/game/admin/release/list [post]
func (c *GameReleaseAPI) List(ctx *gin.Context) {
	var req struct {
		entities.ListReleaseReq
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
	}
	_ = ctx.ShouldBindJSON(&req)

	list, total, err := c.Srv.List(ctx.Request.Context(), &req.ListReleaseReq, req.Page, req.PageSize)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"count": total, "list": list})
}

// Delete 删除版本
// @Tags GameRelease[游戏分发]
// @Summary 删除版本（管理端）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/game/admin/release/delete [post]
func (c *GameReleaseAPI) Delete(ctx *gin.Context) {
	var req struct {
		ReleaseID uint `json:"release_id" binding:"required,min=1"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	if err := c.Srv.Delete(ctx.Request.Context(), req.ReleaseID); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"deleted": true})
}

// SetActive 上架 / 下架一个版本（active=false 即下架）。
//
// 用 `*bool` 而不是 `bool`：`binding:"required"` 对 bool 的零值（false）
// 判定为「没传」，于是**下架请求永远过不了校验** —— 传 false 反而被拒。
func (c *GameReleaseAPI) SetActive(ctx *gin.Context) {
	var req struct {
		ReleaseID uint  `json:"release_id" binding:"required,min=1"`
		Active    *bool `json:"active" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	if err := c.Srv.SetActive(ctx.Request.Context(), req.ReleaseID, *req.Active); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"release_id": req.ReleaseID, "active": *req.Active})
}

// SelfCheck 自检签名密钥配置
// @Tags GameRelease[游戏分发]
// @Summary 自检 Ed25519 密钥配置（返回公钥，用于粘到前端）
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/game/admin/release/self-check [post]
func (c *GameReleaseAPI) SelfCheck(ctx *gin.Context) {
	res, err := c.Srv.SelfCheck()
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, res)
}
