package service

import (
	"time"

	"tf-api/internal/app/service/repository"

	"github.com/google/wire"
)

// SrvSet 所有 Service 的 wire 集合。
// 新增 Service 时：在这里加一行 XxxServiceSet。
var SrvSet = wire.NewSet(
	// 基础设施
	EventServiceSet,
	StateServiceSet,
	SystemServiceSet,

	// 业务
	WalletServiceSet,
	UserServiceSet,
	UserSettingsServiceSet,
	WechatServiceSet,
	AuthServiceSet,
	GameServiceSet,
	GameStorageServiceSet,
	GameSnapshotServiceSet,
	GameCoinServiceSet,
	TaskServiceSet,
	CheckInServiceSet,

	// 农场
	FarmServiceSet,

	// 商城与游戏分发
	RedeemServiceSet,
	WishServiceSet,
	GameReleaseServiceSet,
	MerchantServiceSet,

	// 异步门面（必须最后，它依赖上面多个 Service）
	AsyncServiceManagerSet,
)

// AsyncServiceManagerSet 异步任务门面
var AsyncServiceManagerSet = wire.NewSet(wire.Struct(new(AsyncServiceManager), "*"))

// AsyncServiceManager 供 MQ / Cron 调用的门面。
//
// 设计意图：mq/handle 与 task 只依赖 async.IAsyncService 接口，
// 不直接依赖具体 Service，避免「基础设施 → 业务」的反向耦合。
// 新增一个异步能力需要改 3 处：
//  1. service/async/async.go 的 IAsyncService 接口
//  2. 本结构体加字段 + 方法实现
//  3. mq/queue.go 或 task/listen.go 注册
type AsyncServiceManager struct {
	SystemSrv *SystemService
	EventSrv  *EventService
	TokenRepo *repository.UserTokenRepository
	SmsRepo   *repository.SmsCodeRepository
}

// Noop 占位实现，保证 wire 生成通过；接入业务后删除。
func (m *AsyncServiceManager) Noop() error {
	return nil
}

// Stats 汇总后台服务指标（供 /metrics 或调试接口读取）
func (m *AsyncServiceManager) Stats() map[string]interface{} {
	out := make(map[string]interface{}, 2)
	if m.EventSrv != nil {
		out["event"] = m.EventSrv.Stats()
	}
	return out
}

// Close 关闭后台常驻服务
func (m *AsyncServiceManager) Close() {
	if m.EventSrv != nil {
		m.EventSrv.ShutdownWithTimeout(5 * time.Second)
	}
}
