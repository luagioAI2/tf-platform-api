package service

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"tf-api/pkg/logger"

	"github.com/google/wire"
	"go.uber.org/zap"
)

// EventType 事件类型（新增事件时在这里加常量）
type EventType string

const (
	// UserRegisteredEvent 用户注册成功
	UserRegisteredEvent EventType = "user_registered"
	// OrderCreatedEvent 订单创建
	OrderCreatedEvent EventType = "order_created"
	// OrderStatusChangedEvent 订单状态变更
	OrderStatusChangedEvent EventType = "order_status_changed"

	// TaskCompletedEvent 任务完成（审核通过）
	TaskCompletedEvent EventType = "task_completed"
	// TaskCheckedInEvent 签到成功
	TaskCheckedInEvent EventType = "task_checked_in"
	// RedeemCompletedEvent 兑换商城兑换成功
	RedeemCompletedEvent EventType = "redeem_completed"
	// WishOrderChangedEvent 心愿单状态变更
	WishOrderChangedEvent EventType = "wish_order_changed"
	// GameReleasePublishedEvent 游戏版本发布
	GameReleasePublishedEvent EventType = "game_release_published"
)

// EventHandler 事件处理函数
type EventHandler func(data interface{})

var EventServiceSet = wire.NewSet(ProvideEventService)

// 事件总线默认参数
const (
	// defaultEventWorkers 每个事件类型的并发 worker 数。
	// 原实现是「每个 handler 一个 goroutine」，高并发下会瞬间打爆 goroutine 数量，
	// 改为固定 worker 池 + 有界队列，超出时丢弃并计数（事件是尽力而为语义）。
	defaultEventWorkers = 16
	// defaultEventQueueSize 每个事件类型的队列容量。
	// 按 5W 并发估算：峰值事件速率约 1w/s，worker 处理能力约 2w/s，
	// 队列需能吸收 1~2 秒的突发，故设为 32768。
	defaultEventQueueSize = 32768
)

// task 队列元素：闭包已经绑定了 handler 与 payload
type eventTask func()

// EventService 进程内事件总线。
//
// 设计要点（相比初版）：
//   - handlers 用 RWMutex 保护，修复并发读写 map 的 data race
//   - 用有界队列 + 固定 worker 池，避免高并发下 goroutine 爆炸
//   - 队列满时丢弃并计数，不阻塞业务主流程
//   - Close 时 drain 队列
//
// 适用场景：非关键路径的副作用（发通知、打点、刷新缓存）。
// 关键路径请用 mq（asynq）保证至少一次投递。
type EventService struct {
	mu       sync.RWMutex
	handlers map[EventType][]EventHandler
	queues   map[EventType]chan eventTask

	workers   int // 每事件类型的 worker 数（默认 defaultEventWorkers）
	queueSize int // 每事件类型的队列容量（默认 defaultEventQueueSize）

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	published atomic.Int64
	handled   atomic.Int64
	dropped   atomic.Int64
	panicked  atomic.Int64
}

// ProvideEventService 构造函数
func ProvideEventService() *EventService {
	ctx, cancel := context.WithCancel(context.Background())
	return &EventService{
		handlers: make(map[EventType][]EventHandler),
		queues:   make(map[EventType]chan eventTask),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// ProvideEventServiceWith 自定义 worker 数与队列容量（供压测与特殊场景使用）
func ProvideEventServiceWith(workers, queueSize int) *EventService {
	es := ProvideEventService()
	if workers > 0 {
		es.workers = workers
	}
	if queueSize > 0 {
		es.queueSize = queueSize
	}
	return es
}

// ensureQueueLocked 为该事件类型懒创建 worker 池。调用方必须持有写锁。
func (es *EventService) ensureQueueLocked(eventType EventType) chan eventTask {
	if q, ok := es.queues[eventType]; ok {
		return q
	}

	workers := es.workers
	if workers <= 0 {
		workers = defaultEventWorkers
	}
	size := es.queueSize
	if size <= 0 {
		size = defaultEventQueueSize
	}

	q := make(chan eventTask, size)
	es.queues[eventType] = q

	et := eventType // 闭包捕获，避免共享变量
	for i := 0; i < workers; i++ {
		es.wg.Add(1)
		go es.worker(et, q)
	}
	return q
}

func (es *EventService) worker(eventType EventType, q chan eventTask) {
	defer es.wg.Done()
	for {
		select {
		case <-es.ctx.Done():
			// 关闭：把队列剩余任务执行完再退出
			for {
				select {
				case t := <-q:
					es.runSafe(eventType, t)
				default:
					return
				}
			}
		case t := <-q:
			es.runSafe(eventType, t)
		}
	}
}

func (es *EventService) runSafe(eventType EventType, t eventTask) {
	defer func() {
		if r := recover(); r != nil {
			es.panicked.Add(1)
			logger.ZError("event handler panic",
				zap.String("eventType", string(eventType)),
				zap.Any("panic", r))
		}
	}()
	t()
	es.handled.Add(1)
}

// Subscribe 订阅事件（并发安全）
func (es *EventService) Subscribe(eventType EventType, handler EventHandler) {
	if handler == nil {
		return
	}
	es.mu.Lock()
	defer es.mu.Unlock()

	es.handlers[eventType] = append(es.handlers[eventType], handler)
	es.ensureQueueLocked(eventType)
	logger.ZInfo("event subscribed", zap.String("eventType", string(eventType)))
}

// Publish 发布事件（异步、非阻塞）。
// 队列满时丢弃并计数，绝不阻塞调用方。
func (es *EventService) Publish(eventType EventType, data interface{}) {
	es.mu.RLock()
	handlers := es.handlers[eventType]
	q := es.queues[eventType]
	es.mu.RUnlock()

	if len(handlers) == 0 || q == nil {
		return
	}

	for _, h := range handlers {
		handler, payload := h, data
		select {
		case q <- func() { handler(payload) }:
			es.published.Add(1)
		default:
			n := es.dropped.Add(1)
			if n%1000 == 1 { // 采样告警，避免日志风暴
				logger.ZWarn("event queue full, dropping event",
					zap.String("eventType", string(eventType)),
					zap.Int64("dropped_total", n))
			}
		}
	}
}

// Stats 返回事件总线运行指标
func (es *EventService) Stats() map[string]int64 {
	es.mu.RLock()
	queueCount := len(es.queues)
	es.mu.RUnlock()
	return map[string]int64{
		"published": es.published.Load(),
		"handled":   es.handled.Load(),
		"dropped":   es.dropped.Load(),
		"panicked":  es.panicked.Load(),
		"queues":    int64(queueCount),
	}
}

// Close 停止所有 worker 并 drain 队列
func (es *EventService) Close() {
	es.cancel()
	es.wg.Wait()
}

// ShutdownWithTimeout 带超时的关闭（供 app 生命周期调用）
func (es *EventService) ShutdownWithTimeout(d time.Duration) {
	done := make(chan struct{})
	go func() {
		es.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		logger.ZWarn("event service shutdown timeout", zap.Duration("timeout", d))
	}
}
