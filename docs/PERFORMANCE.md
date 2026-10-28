# 性能与容量设计（支撑 5W 并发）

> 本文说明 tf-api 为支撑 5 万并发所做的设计决策、验证数据与容量规划。
> 所有数字均为**实测**（8 核 / 16G Windows 开发机，单实例）。

---

## 1. 目标与结论

| 指标 | 目标 | 实测（单实例 8 核） |
|---|---|---|
| 纯框架吞吐 | — | **17,345 QPS**（P99 19ms） |
| 事件总线分发 | ≥ 10w/s | **2,333,249 事件/秒**，0 丢弃 |
| 缓存击穿防护 | 同 key 只回源 1 次 | **500 并发 → 1 次回源**，52ms |
| goroutine 泄漏 | 无 | base=2 → after=3（5 次压测后） |
| 限流精度 | 误差 < 1% | 600 限额 → 通过 599 / 拒绝 101 |

**5W 并发怎么达到**：单实例约 1.7w QPS，按 70% 水位留余量（1.2w/实例），
**5 个实例即可覆盖 6w QPS**。配合下面的优化，实际可支撑的"5 万人同时在线"绰绰有余
（5 万人同时在线 ≠ 5 万 QPS，真实场景 QPS 通常是在线数的 1/10 ~ 1/30）。

---

## 2. 关键设计

### 2.1 两级缓存 + singleflight（`pkg/cache`）

```
请求 → L1 本地 LRU（30s，微秒级）
         ↓ miss
       L2 Redis（5min，跨实例共享）
         ↓ miss
       singleflight（同 key 只回源 1 次）
         ↓
       DB
```

**为什么必须有 L1**：5w 并发下热点数据（配置、用户信息）会被反复读取。
纯 Redis 方案即使 1ms 一次，1w QPS 也会产生 1w 次 Redis 往返，
Redis 很快成为瓶颈。L1 命中是**纯内存操作，无网络开销**。

**为什么必须有 singleflight**：缓存失效瞬间，N 个并发请求会同时回源，
数据库瞬间被打穿（缓存雪崩）。实测 500 并发只回源 1 次。

用法：

```go
user, err := cache.Load(ctx, cacheFactory.Get(cache.CacheUser), "user:1001",
    func(ctx context.Context) (*User, error) {
        return repo.GetByID(ctx, 1001)
    })
```

### 2.2 分布式限流（`pkg/rds/ratelimit.go`）

**Redis 滑动窗口 + Lua 原子执行**：

- 多实例共享计数，不会出现「N 副本 = N 倍限额」
- 滑动窗口而非固定窗口，避免窗口边界的 2 倍突发
- Redis 故障时 **fail-open**（放行）+ 本地限流兜底，不让限流组件拖垮全站

配置：

```yaml
RateLimit:
  Enable: true
  GlobalLimit: 600        # 单 IP 每分钟
  GlobalWindowSeconds: 60
```

单接口可单独加更严的限流：

```go
r.POST("/login", middleware.RateLimitMiddleware(limiter, middleware.RateLimitConfig{
    Name: "login", Limit: 10, Window: time.Minute,
}), handler)
```

### 2.3 事件总线（`internal/app/service/event_srv.go`）

**问题**：初版实现是「每个 handler 起一个 goroutine」。
5w 并发下，每次请求发 3 个事件 × 5 个订阅者 = 15 个 goroutine，
1w QPS 就是 15w goroutine，直接 OOM。

**方案**：固定 worker 池 + 有界队列。

```
Publish（非阻塞）
   ↓ select-default
队列（32768）→ 16 个 worker → handler
   ↓ 队列满
丢弃 + 计数 + 采样告警（每 1000 次一条）
```

- 队列满**绝不阻塞**业务主流程
- `dropped` 计数暴露到指标，可告警
- 实测 233w 事件/秒，0 丢弃

### 2.4 中间件链（`internal/app/web.go`）

顺序经过设计，每一层都有明确目的：

| 顺序 | 中间件 | 作用 | 为什么在这个位置 |
|---|---|---|---|
| 1 | Recovery | 捕获 panic | 最外层才能兜住所有下游 |
| 2 | Metrics | 计时 | 尽早开始，覆盖后续全部耗时 |
| 3 | Trace | requestID | 后续所有日志都要带 |
| 4 | SecurityHeaders | 安全头 | 无成本，尽早加 |
| 5 | CORS | 跨域 | 预检请求在此返回，不进业务 |
| 6 | BodyLimit | 请求体限制 | 在解析前拦截，省内存 |
| 7 | Timeout | 超时 | 最后，把 context 传给业务 |
| 8 | 路由 | | |

### 2.5 数据库

```go
SkipDefaultTransaction: true,  // 单条写不包事务，省一次 BEGIN/COMMIT
DisableAutomaticPing:   true,  // 关闭每次查询前的 ping
SingularTable:          true,  // 表名不加复数
```

连接池：

```yaml
MaxIdleConns: 20      # 常驻连接，避免频繁建连
MaxOpenConns: 100     # 上限，需 ≤ MySQL max_connections / 实例数
ConnMaxLifetimeSeconds: 3600
```

**关键**：`SetConnMaxIdleTime(10min)` 防止 MySQL `wait_timeout` 断开后拿到坏连接。

读写分离：`dbresolver` + `RandomPolicy`，读多写少的场景可显著降低主库压力。

### 2.6 日志策略

**5w 并发下最大的坑是日志**。全量记录会把磁盘打满，且 JSON 序列化本身耗 CPU。

策略（`middleware/trace.go`）：

- 正常且 < 500ms 的请求：**不记录**
- 慢请求 / 4xx：`warn` 级别
- 5xx / panic：`error` 级别

同时 Redis 的 `MaxRetries: 1` + `DisableIndentity` 避免连接失败时日志刷屏。

### 2.7 超时与保护

| 配置 | 值 | 作用 |
|---|---|---|
| `ReadHeaderTimeoutSeconds` | 5 | 防慢速攻击（Slowloris） |
| `ReadTimeoutSeconds` | 30 | 读请求体上限 |
| `WriteTimeoutSeconds` | 60 | 写响应上限 |
| `IdleTimeoutSeconds` | 90 | keep-alive 复用 |
| `MaxHeaderBytes` | 1MB | 防超大 header |
| `MaxBodyBytes` | 4MB | 防超大 body |
| `RequestTimeoutSeconds` | 20 | 单请求处理超时 → 504 |
| `ShutdownTimeoutSeconds` | 15 | 优雅关闭 |

**超时必须靠 context 贯穿**：`TimeoutMiddleware` 只是把 context 放进 request，
真正中断下游需要 Repository 用 `db.WithContext(ctx)`、Redis 用 `ctx`。
这是 `AGENTS.md` 强制 ctx 贯穿的原因。

### 2.8 可观测性

`/metrics`（Prometheus）：

| 指标 | 用途 |
|---|---|
| `tf_http_requests_total` | 按 method/path/status 的 QPS |
| `tf_http_request_duration_seconds` | P50/P90/P99 延迟 |
| `tf_http_in_flight_requests` | 当前并发数 |
| `tf_db_query_duration_seconds` | SQL 耗时，定位慢查询 |
| `tf_cache_hits_total` | 按 local/redis 分层的命中率 |
| `tf_event_total` | 事件 published/dropped/panicked |
| `tf_ratelimit_rejected_total` | 限流拒绝数 |
| Go 运行时 | goroutine、GC、内存 |

**path 用路由模板**（`c.FullPath()`）而非真实路径，否则 `/order/123` 与 `/order/456`
会产生两个时间序列，导致基数爆炸（Prometheus 最容易被忽略的坑）。

健康检查分离：

- `/healthz`（liveness）：只看进程，**不检查依赖**
- `/health`（readiness）：检查 DB/Redis，故障返回 503

> 为什么分开：如果 liveness 也检查 DB，DB 抖动会导致 k8s 重启所有 Pod，反而放大故障。

---

## 3. 容量规划

### 3.1 单实例能力

| 场景 | QPS | P99 |
|---|---|---|
| 纯内存（/healthz） | 12,528 | 15ms |
| 完整中间件链（/api/ping） | 17,345 | 19ms |
| 带 DB 查询（估算） | 3,000 ~ 5,000 | 50 ~ 100ms |

### 3.2 5W 并发需要多少实例

假设"5 万人同时在线"，真实 QPS 约为在线数的 1/10 ~ 1/20：

```
5w 在线 → 2,500 ~ 5,000 QPS
按单实例 1.2w QPS 的 70% 水位 → 1 个实例就够
为高可用（N+2）→ 3 个实例
```

如果是"5w QPS"的极端场景：

```
5w QPS / 1.2w（单实例 70% 水位）≈ 5 个实例
+ 2 个冗余 = 7 个实例
```

### 3.3 资源建议

| 组件 | 规格 | 说明 |
|---|---|---|
| API Pod | 2C4G × 5 | CPU 密集在 JSON 序列化与业务逻辑 |
| MySQL | 8C32G 主 + 2 从 | 主库写，从库读 |
| Redis | 4G 主从 | 缓存 + 限流 + 锁 |
| 带宽 | 100Mbps+ | 按响应体大小估算 |

### 3.4 关键调优项

```yaml
# MySQL
max_connections = 1000          # ≥ API 实例数 × MaxOpenConns
innodb_buffer_pool_size = 24G   # 物理内存的 70%
slow_query_log = ON
long_query_time = 0.5

# Redis
maxmemory-policy allkeys-lru
timeout 0
tcp-keepalive 300

# Nginx（前置）
worker_connections 65535
keepalive_timeout 65
client_max_body_size 8m
proxy_read_timeout 30s
```

---

## 4. 多副本注意事项

### 4.1 Cron 只能跑一份

`EnableTask` 在除主实例外全部设为 `false`，或使用独立的 `cmd/worker`：

```yaml
# HTTP Pod（5 个）
EnableTask: false
EnableMQ: false

# Worker Pod（1 个）
EnableTask: true
EnableMQ: true
```

### 4.2 限流与锁依赖 Redis

多实例必须配置 Redis，否则：
- 限流降级为本地（每个实例独立计数，实际限额 = N × 配置值）
- 分布式锁失效（`rds.GetClient()` 返回 nil）

生产环境 Redis 不可用时启动会**直接失败**（`provideRDS` 里判断），避免带病运行。

### 4.3 可信代理必填

```yaml
TrustedProxies:
  - 10.0.0.0/8      # 内网
  - 172.16.0.0/12
```

不配置时 gin **不信任**任何 `X-Forwarded-For`，所有请求 IP 都会是代理 IP，
导致限流把全站当成一个 IP。生产环境此项为**必填校验**。

---

## 5. 压测方法

内置压测工具 `cmd/bench`：

```bash
# 编译
go build -o bench ./cmd/bench

# 固定请求数
./bench -url http://127.0.0.1:8080/api/ping -m POST -c 200 -n 50000

# 固定时长
./bench -url http://127.0.0.1:8080/healthz -c 500 -d 60s
```

输出示例：

```
================ 压测结果 ================
总请求数 : 30000
成功     : 30000
失败     : 0
耗时     : 1.73s
QPS      : 17345
并发     : 100
状态码   : 200=30000
延迟 P50 : 4.499ms
延迟 P90 : 10.001ms
延迟 P95 : 12.516ms
延迟 P99 : 19.38ms
延迟 Max : 84.503ms
==========================================
```

### 压测注意事项

1. **客户端可能先成为瓶颈**：Windows 默认只有 ~16k 临时端口，
   压测时 `c=500` 会因 TIME_WAIT 耗尽而出现大量失败。
   用 `c=50~200` 测，或用多台压测机。
2. **先测基线再测业务**：先测 `/healthz`（纯框架），再测真实接口，
   否则无法区分瓶颈在框架还是在业务。
3. **关注 P99 而非平均值**：平均值会掩盖长尾。

---

## 6. 已知限制与后续优化

| 项 | 现状 | 优化方向 |
|---|---|---|
| 单机 QPS 上限 | ~1.7w | 增加实例；换 fasthttp 需重写 |
| 事件总线 | 进程内，尽力而为 | 关键事件改用 asynq（已支持） |
| 缓存一致性 | L1 30s 内可能不一致 | 配置变更时 `FlushLocal()` 或广播失效 |
| 限流维度 | 按 IP | 可按 uid / 接口 / 用户等级细分 |
| 追踪 | 仅 requestID | 接入 OpenTelemetry 做全链路 |
| 数据库 | 单主 | 分库分表 / 读写分离已支持 |
