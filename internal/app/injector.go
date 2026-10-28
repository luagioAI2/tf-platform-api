package internal

import (
	"tf-api/internal/app/service"
	"tf-api/internal/app/service/async"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

var InjectorSet = wire.NewSet(wire.Struct(new(Injector), "*"))

// Injector 依赖注入的最终产物：HTTP 引擎 + 后台常驻服务
type Injector struct {
	Engine  *gin.Engine
	Service async.IAsyncService

	// 需要随进程启停的常驻服务在此声明，例如：
	// AnalyticsSrv *service.AnalyticsService
	SystemSrv *service.SystemService
}
