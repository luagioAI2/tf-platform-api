package api

import (
	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var GameSnapshotAPISet = wire.NewSet(wire.Struct(new(GameSnapshotAPI), "*"))

// GameSnapshotAPI 存档快照与回档接口。
//
// 用途：
//   - 家长查看存档历史
//   - 误操作/bug 导致存档损坏时回档
//   - 争议时审计
type GameSnapshotAPI struct {
	Srv *service.GameSnapshotService
}

// List 快照列表
// @Tags GameSnapshot[存档快照]
// @Summary 列出存档快照
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameSnapshotListReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/game/snapshot/list [post]
func (c *GameSnapshotAPI) List(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.GameSnapshotListReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	list, err := c.Srv.List(ctx.Request.Context(), uid, req.GameID, req.Key, req.Limit)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"list": list, "count": len(list)})
}

// Get 快照详情（含内容）
// @Tags GameSnapshot[存档快照]
// @Summary 查看快照内容
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameSnapshotGetReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/game/snapshot/get [post]
func (c *GameSnapshotAPI) Get(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.GameSnapshotGetReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	detail, err := c.Srv.Get(ctx.Request.Context(), uid, req.SnapshotID)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, detail)
}

// Create 手动创建快照
// @Tags GameSnapshot[存档快照]
// @Summary 手动保存快照
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameSnapshotCreateReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/game/snapshot/create [post]
func (c *GameSnapshotAPI) Create(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.GameSnapshotCreateReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}

	// 读当前值再存快照
	kv, err := c.Srv.KvRepo.Get(ctx.Request.Context(), uid, req.GameID, req.Key)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	if kv == nil {
		// 还没有存档，没什么可快照的
		ginx.RespErr(ctx, apperrors.WithCode(apperrors.ResourceNotExist))
		return
	}

	snap, err := c.Srv.Create(ctx.Request.Context(), uid, req.GameID, req.Key,
		kv.Value, kv.Version, entities.SnapshotReasonManual, req.Note)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"snapshot_id": snap.ID, "version": snap.Version})
}

// Restore 回档到指定快照
// @Tags GameSnapshot[存档快照]
// @Summary 回档到指定快照
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameSnapshotRestoreReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/game/snapshot/restore [post]
func (c *GameSnapshotAPI) Restore(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.GameSnapshotRestoreReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	kv, err := c.Srv.Restore(ctx.Request.Context(), uid, req.SnapshotID, 0, req.Reason)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{
		"restored": true,
		"game_id":  kv.GameID,
		"key":      kv.Key,
		"version":  kv.Version,
	})
}

// RestoreByTime 按时间点回档
// @Tags GameSnapshot[存档快照]
// @Summary 按时间点回档（恢复到该时刻之前最近的快照）
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param req body entities.GameSnapshotRestoreByTimeReq true "参数"
// @Success 200 {object} ginx.Resp
// @Router /api/game/snapshot/restore-by-time [post]
func (c *GameSnapshotAPI) RestoreByTime(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req entities.GameSnapshotRestoreByTimeReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ginx.RespErr(ctx, err)
		return
	}

	// key 为空 = 回档整个游戏
	if req.Key == "" {
		n, err := c.Srv.RestoreGame(ctx.Request.Context(), uid, req.GameID, req.At, 0, req.Reason)
		if err != nil {
			ginx.RespErr(ctx, err)
			return
		}
		ginx.RespSucc(ctx, gin.H{"restored_keys": n})
		return
	}

	kv, err := c.Srv.RestoreByTime(ctx.Request.Context(), uid, req.GameID, req.Key, req.At, 0, req.Reason)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{
		"restored": true,
		"key":      kv.Key,
		"version":  kv.Version,
	})
}

// RestoreLogs 回档日志
// @Tags GameSnapshot[存档快照]
// @Summary 回档操作日志
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/game/snapshot/restore-logs [post]
func (c *GameSnapshotAPI) RestoreLogs(ctx *gin.Context) {
	uid, ok := ginx.MustMine(ctx)
	if !ok {
		return
	}
	var req struct {
		GameID string `json:"game_id"`
		Limit  int    `json:"limit"`
	}
	_ = ctx.ShouldBindJSON(&req)

	list, err := c.Srv.RestoreLogs(ctx.Request.Context(), uid, req.GameID, req.Limit)
	if err != nil {
		ginx.RespErr(ctx, err)
		return
	}
	ginx.RespSucc(ctx, gin.H{"list": list, "count": len(list)})
}
