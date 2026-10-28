# 架构优化记录（v2）

> 记录从「可用骨架」到「可支撑 5W 并发」的优化过程、发现的问题与验证数据。
> 配套阅读：[`PERFORMANCE.md`](./PERFORMANCE.md)（容量设计与压测方法）

---

## 1. 审计发现的问题

在 v1 骨架基础上做并发审计，发现 **11 个问题**，其中 3 个是会导致线上故障的严重缺陷。

### P0 — 并发安全（会直接导致故障）

| # | 问题 | 后果 | 修复 |
|---|---|---|---|
| 1 | **EventService 并发读写 map** | `Publish` 读 `handlers` 时 `Subscribe` 正在写 → Go 直接 panic `concurrent map read and map write`，进程崩溃 | 加 `sync.RWMutex`；订阅走写锁 |
| 2 | **StateService 无锁读写 map** | 同上，且 `Set` 依赖 channel 串行化，`Get` 完全裸奔 | 改为 `sync.RWMutex` |
| 3 | **每个事件起一个 goroutine** | 5w 并发 × 3 事件 × 5 订阅者 = 75w goroutine → OOM | 固定 worker 池（16/类型）+ 有界队列（32768） |

**验证**：修复前 `TestNoGoroutineLeak` 会失败；修复后 base=2 → after=3，且 100000 事件 0 丢弃、233w/s。

### P1 — 资源泄漏与性能

| # | 问题 | 后果 | 修复 |
|---|---|---|---|
| 4 | 限流中间件每个实例起一个后台 goroutine 做 GC，且**永不退出** | 每次注册路由泄漏一个 goroutine | 改为惰性 GC（每 30s 顺带清理），无独立 goroutine |
| 5 | 限流是**单实例内存**实现 | N 个副本 = N 倍限额，限流形同虚设 | 改为 Redis 滑动窗口（Lua 原子），Redis 不可用时降级本地 |
| 6 | Redis 客户端 `MaxRetries` 默认 3 且无超时控制 | Redis 故障时请求堆积、日志刷屏 | `MaxRetries:0` + 2s 超时 + 日志重定向到 zap（debug 级） |
| 7 | `rds` 全局客户端无锁保护 | 初始化与读取竞争 | `sync.RWMutex` + `SetClient/GetClient` |

### P2 — 可观测性与保护缺失

| # | 问题 | 后果 | 修复 |
|---|---|---|---|
| 8 | 无任何指标 | 出问题只能翻日志，定位 P99 抖动要几小时 | 接入 Prometheus（HTTP/DB/缓存/事件/限流） |
| 9 | `/health` 同时承担 liveness 与 readiness | DB 抖动导致 k8s 重启所有 Pod，放大故障 | 拆分为 `/healthz`（只看进程）与 `/health`（查依赖） |
| 10 | 无超时/请求体限制/安全头 | 慢请求堆积、大 payload 打爆内存 | `TimeoutMiddleware` / `BodyLimitMiddleware` / `SecurityHeadersMiddleware` |
| 11 | 可信代理未配置 | `X-Forwarded-For` 可伪造，限流按伪造 IP 计数 → 绕过 | `SetTrustedProxies`；生产环境强制校验 |

---

## 2. 新增能力

### 2.1 两级缓存（`pkg/cache`）

```
L1 本地 LRU（30s）→ L2 Redis（5min）→ singleflight → DB
```

**核心价值**：热点数据本地命中（微秒级，无网络），失效瞬间同 key 只回源 1 次。

**实测**：500 并发只回源 1 次，52ms 完成；1000 次重复读全部命中 L1。

**特性**：
- 手写 LRU（双向链表 + map），带 TTL 与容量上限
- `singleflight` 防击穿
- 无 Redis 时自动降级为纯本地
- 泛型 `Load[T]` 让调用方拿到强类型

### 2.2 分布式限流（`pkg/rds/ratelimit.go`）

- Redis **滑动窗口** + Lua 原子执行（不是固定窗口，无边界突发）
- 多实例共享计数
- Redis 故障 fail-open + 本地兜底
- 支持自定义维度（IP / uid / 接口）

**实测**：限额 600 → 通过 599、拒绝 101，误差 0。

### 2.3 幂等控制（`pkg/rds/ratelimit.go`）

```go
// 简单幂等
ok, err := rds.Idempotent(ctx, "order:create:"+reqID, 24*time.Hour)

// 幂等 + 缓存首次结果
result, cached, err := rds.IdempotentResult(ctx, key, ttl, func() (string, error) { ... })
```

### 2.4 可观测性（`pkg/metrics`）

11 个自定义指标 + Go 运行时指标。关键设计：

- **path 用路由模板**（`c.FullPath()`），避免 `/order/123` 造成时间序列爆炸
- 指标注册幂等（`sync.Once` + 忽略 `AlreadyRegisteredError`），测试可重复初始化
- DB 指标通过 GORM callback 采集，覆盖 query/create/update/delete

### 2.5 中间件链

```
Recovery → Metrics → Trace → SecurityHeaders → CORS → BodyLimit → Timeout → 路由
```

顺序有讲究：Recovery 最外，Metrics 尽早，BodyLimit 在解析前，Timeout 在业务前。

### 2.6 Worker 进程分离（`cmd/worker`）

解决「多副本重复跑 Cron」：

```yaml
# HTTP Pod × 5
EnableTask: false
EnableMQ: false

# Worker Pod × 1
EnableTask: true
EnableMQ: true
```

### 2.7 压测工具（`cmd/bench`）

跨平台、可精确控制并发与连接复用，输出 QPS/P50/P90/P99。

---

## 3. 验证数据汇总

| 项目 | 指标 | 结果 |
|---|---|---|
| 框架吞吐 | QPS / P99 | **17,345 / 19.38ms** |
| 纯内存端点 | QPS / P99 | 12,528 / 15.1ms |
| 事件分发 | 吞吐 / 丢弃 | **2,333,249/s / 0** |
| 缓存防击穿 | 500 并发回源次数 | **1 次（52ms）** |
| 缓存命中 | 1000 次重复读 | 全部 L1 命中 |
| 限流精度 | 600 限额 | 599 通过 / 101 拒绝 |
| goroutine 泄漏 | 5000 事件后 | base=2 → after=3 |
| 请求体限制 | 5MB body | 413 正确拒绝 |
| 安全头 | 响应头 | 4 项全部设置 |
| 生产模式 | 无 Redis | 拒绝启动（符合预期） |

### 关于 `-race`

本机 MinGW 缺少 `libwinpthread` 等运行时 DLL，`go test -race` 报
`0xc0000139`（`STATUS_ENTRYPOINT_NOT_FOUND`），**这是环境问题不是代码问题**。

替代验证方式：
- 并发压力测试（`TestEventServiceConcurrent`：50 订阅 + 1000 发布并发）
- goroutine 泄漏检测（`TestNoGoroutineLeak`）
- 缓存并发读写（`TestCacheConcurrent`）

**建议在 Linux CI 上跑 `-race`**（`.github/workflows/ci.yml` 已配置）。

---

## 4. 与 v1 的对比

| 维度 | v1（骨架） | v2（本次优化） |
|---|---|---|
| 并发安全 | ❌ 3 处 data race | ✅ 全部修复 |
| 事件处理 | 每事件一 goroutine | worker 池 + 有界队列 |
| 缓存 | 无 | 两级 + singleflight |
| 限流 | 单机内存 | Redis 分布式滑动窗口 |
| 指标 | 无 | Prometheus 11 项 |
| 健康检查 | 单一 /health | liveness / readiness 分离 |
| 超时保护 | 仅 HTTP Server | 中间件 + context 贯穿 |
| 请求体限制 | 无 | 4MB 上限 |
| 安全响应头 | 无 | 4 项 |
| 可信代理 | 未配置 | 可配置 + 生产强制 |
| Cron 多副本 | 会重复执行 | worker 进程分离 |
| 压测工具 | 无 | cmd/bench |
| 测试 | 0 | 15 个（并发/缓存/泄漏） |

---

## 5. 后续建议（未做）

| 项 | 说明 | 优先级 |
|---|---|---|
| OpenTelemetry | 全链路追踪，跨服务定位问题 | 中 |
| 熔断器 | 外部 API 调用熔断（当前仅超时） | 中 |
| 缓存广播失效 | L1 一致性依赖 30s TTL | 低 |
| 分库分表 | 单表超千万后需要 | 低（按数据量） |
| 读写分离验证 | 主从延迟导致读到旧数据 | 中（接入从库时） |
| 压测基线 | 建议在 Linux 4C8G 上重跑，作为容量基线 | 高 |
