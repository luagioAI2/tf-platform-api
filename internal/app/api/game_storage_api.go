package api

import (
	"encoding/json"

	"tf-api/internal/app/entities"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var GameStorageAPISet = wire.NewSet(wire.Struct(new(GameStorageAPI), "*"))

// GameStorageAPI 游戏存档接口（通用 KV）。
//
// 对齐 kid-quest-farm 的 `host.storage` 语义：
// 整块读、整块写、列出全部。
type GameStorageAPI struct {
	Srv *service.GameStorageService
}

// Get 读取存档
// @Tags GameStorage[游戏存档]
// @Summary 读取单个键
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameKvGetReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/game/kv/get [post]
func (c *GameStorageAPI) Get(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.GameKvGetReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	v, err := c.Srv.Get(ctx.Request.Context(), uid, req.GameID, req.Key)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"key": req.Key, "value": v})
}

// Set 写入存档
// @Tags GameStorage[游戏存档]
// @Summary 整块写入
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameKvSetReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/game/kv/set [post]
func (c *GameStorageAPI) Set(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}

	// 用 RawMessage 接收 value，保留原始 JSON（不丢精度、不改结构）
	var raw struct {
		GameID          string          `json:"game_id" binding:"required,max=32"`
		Key             string          `json:"key" binding:"required,max=128"`
		Value           json.RawMessage `json:"value" binding:"required"`
		ExpectedVersion int64           `json:"expected_version"`
	}
	if err := ctx.ShouldBindJSON(&raw); err != nil {
		ginx.RespErr(ctx, err)
		return
	}

	version, err := c.Srv.Set(ctx.Request.Context(), uid, raw.GameID, raw.Key, raw.Value, raw.ExpectedVersion)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"key": raw.Key, "version": version})
}

// List 列出全部键值
// @Tags GameStorage[游戏存档]
// @Summary 列出该游戏全部存档
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameKvListReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/game/kv/list [post]
func (c *GameStorageAPI) List(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.GameKvListReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	all, err := c.Srv.List(ctx.Request.Context(), uid, req.GameID)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"game_id": req.GameID, "data": all, "count": len(all)})
}

// Delete 删除存档
// @Tags GameStorage[游戏存档]
// @Summary 删除单个键或整个游戏存档
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameKvDeleteReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/game/kv/delete [post]
func (c *GameStorageAPI) Delete(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.GameKvDeleteReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}

	// key 为空 = 卸载整个游戏
	if req.Key == "" {
		n, err := c.Srv.Uninstall(ctx.Request.Context(), uid, req.GameID)
		if err != nil {
			ginx.RespErr(ctx, err)
			return
		}
		ginx.RespSucc(ctx, gin.H{"deleted_keys": n})
		return
	}

	if err := c.Srv.Delete(ctx.Request.Context(), uid, req.GameID, req.Key); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"deleted": true})
}

// Usage 存储用量
// @Tags GameStorage[游戏存档]
// @Summary 查询存储用量
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/game/kv/usage [post]
func (c *GameStorageAPI) Usage(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	usage, err := c.Srv.Usage(ctx.Request.Context(), uid)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, usage)
}
