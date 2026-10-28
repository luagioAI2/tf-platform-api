package api

import (
	"net/http"
	"runtime"

	"tf-api/internal/app/buildinfo"
	"tf-api/internal/app/ginx"
	"tf-api/internal/app/service"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var SystemAPISet = wire.NewSet(wire.Struct(new(SystemAPI), "*"))

// SystemAPI 系统级接口（健康检查、运行时信息）
type SystemAPI struct {
	Srv  *service.SystemService
	Meta buildinfo.Info
}

// Live 存活探针：只表示进程活着，不检查依赖。
//
// 为什么与 Ready 分开：k8s 的 livenessProbe 一旦失败会重启 Pod。
// 如果 liveness 也检查 DB，那么 DB 抖动会导致所有 Pod 被重启，
// 反而放大故障。liveness 只看进程，readiness 才看依赖。
//
// @Tags System
// @Summary 存活探针
// @Produce json
// @Success 200 {object} map[string]string
// @Router /healthz [get]
func (c *SystemAPI) Live(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"version": c.Meta.Version,
	})
}

// Ready 就绪探针：检查 DB / Redis，任一不可用返回 503。
// @Tags System
// @Summary 就绪探针
// @Produce json
// @Success 200 {object} service.HealthStatus
// @Failure 503 {object} service.HealthStatus
// @Router /health [get]
func (c *SystemAPI) Ready(ctx *gin.Context) {
	status := c.Srv.Health(ctx.Request.Context(), c.Meta.Version, c.Meta.Env)
	if status.Status != service.CheckOK {
		ctx.JSON(http.StatusServiceUnavailable, status)
		return
	}
	ctx.JSON(http.StatusOK, status)
}

// Ping 连通性探测（不依赖 DB/Redis）
// @Tags System
// @Summary Ping
// @Produce json
// @Success 200 {object} ginx.Resp{data=string}
// @Router /api/ping [post]
func (c *SystemAPI) Ping(ctx *gin.Context) {
	ginx.RespSucc(ctx, "pong")
}

// RuntimeInfo 运行时信息
// @Tags System
// @Summary 运行时信息
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ginx.Resp
// @Router /api/system/info [post]
func (c *SystemAPI) RuntimeInfo(ctx *gin.Context) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	ginx.RespSucc(ctx, gin.H{
		"version":    c.Meta.Version,
		"env":        c.Meta.Env,
		"uid":        ginx.Mine(ctx),
		"goroutines": runtime.NumGoroutine(),
		"mem_alloc":  m.Alloc,
		"mem_sys":    m.Sys,
		"gc_count":   m.NumGC,
		"cpus":       runtime.NumCPU(),
	})
}
