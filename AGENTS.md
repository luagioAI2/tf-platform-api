# AGENTS.md — tf-api 项目规则

> 本文件是给 AI 编码助手的**强制约束**。每次开始写代码前必须先读本文件。
> 与本文冲突的任何"历史习惯"以本文为准。

---

## 1. 技术栈

| 层 | 选型 |
|---|---|
| 语言 | Go 1.24 |
| Web | Gin v1.11 |
| ORM | GORM v1.31（MySQL；本地可用 SQLite） |
| 缓存/锁 | Redis（go-redis v9） |
| 消息队列 | Asynq（基于 Redis） |
| 定时任务 | robfig/cron v3 |
| 依赖注入 | Google Wire（编译期生成） |
| 日志 | Uber Zap + lumberjack |
| 配置 | YAML + 环境变量覆盖 |
| 文档 | swaggo（注解写在真实 handler 上） |

---

## 2. 目录与依赖方向（单向，禁止反向 import）

```
cmd/tf-api/main.go
        ↓
internal/app/app.go          启动编排
        ↓
internal/app/router/         路由注册（按业务域拆文件）
        ↓
internal/app/api/            HTTP Handler（薄层）
        ↓
internal/app/service/        业务逻辑（厚层）
        ↓
internal/app/service/repository/   数据访问
        ↓
        GORM → MySQL

横切：
  internal/app/entities/     模型 + DTO（全层共享）
  internal/app/ginx/         统一响应/参数绑定
  internal/app/errors/       错误码 + 错误对象
  internal/app/i18n/         多语言
  internal/app/middleware/   鉴权/CORS/Trace/限流
  internal/app/config/       配置
  internal/app/utils/        工具
  pkg/                       与业务无关的基础包
```

**硬性规则**
- `pkg/` **禁止** import `internal/`
- `service/` **禁止** import `gin`
- `repository/` **禁止** import `service/`
- `api/` **禁止** import `repository/`
- `entities/` **禁止** import 其他业务包（只允许标准库 + gorm + decimal）

---

## 3. 新增一个接口的标准动作（8 步，缺一不可）

以「订单」模块为例：

1. **entities**：`internal/app/entities/order.go`
   - 定义 Model（内嵌 `BaseModel`，显式写 `TableName()`）
   - 定义 `XxxReq` / `XxxResp` DTO
2. **repository**：`internal/app/service/repository/order_repo.go`
   - `type OrderRepository struct { DB *gorm.DB; RDS *redis.Client }`
   - `var OrderRepositorySet = wire.NewSet(wire.Struct(new(OrderRepository), "*"))`
   - **所有方法第一个参数必须是 `ctx context.Context`**，查询用 `DB.WithContext(ctx)`
3. **service**：`internal/app/service/order_srv.go`
   - `type OrderService struct { Repo *repository.OrderRepository; ... }`
   - 写 `ProvideOrderService(...)` 构造函数 + `var OrderServiceSet = wire.NewSet(ProvideOrderService)`
4. **api**：`internal/app/api/order_api.go`
   - `type OrderAPI struct { Srv *service.OrderService }`
   - `var OrderAPISet = wire.NewSet(wire.Struct(new(OrderAPI), "*"))`
   - handler 只做三件事：绑定参数 → 调 Service → `ginx.RespSucc` / `ginx.RespErr`
5. **router**：`internal/app/router/router_order.go`
   - 注册路由，**显式写中间件**（不依赖 group 继承）
6. **注册三个 Set**
   - `repository.RepoSet` 加 `OrderRepositorySet`
   - `service.SrvSet` 加 `OrderServiceSet`
   - `api.APISet` 加 `OrderAPISet`
7. **生成 + 编译**
   ```
   make wire && make build
   ```
8. **补 swagger 注解**（写在真实 handler 上方，不要另建 mock 包）

---

## 4. 代码规范（强制）

### 4.1 错误处理

```go
// 正确：业务错误必须带错误码
return errors.WithCode(errors.OrderNotExist)

// 正确：包装底层错误并保留上下文
return errors.WithError(err).Track("OrderService.Get", "query order failed")

// 禁止：裸抛
return fmt.Errorf("order not found")
```

- 错误码必须先在 `internal/app/errors/code.go` 注册
- 新增错误码必须同时在 `configs/i18n/{en,es,zh}.yaml` 加对应 key
- i18n key 与 `ErrCodeMsg` 的 value 必须完全一致

### 4.2 响应

```go
// 成功
ginx.RespSucc(ctx, data)

// 失败
ginx.RespErr(ctx, err)

// 取当前用户（注意：MustMine 返回两个值）
uid, ok := ginx.MustMine(ctx)
if !ok {
    return // MustMine 内部已写响应并 abort
}
```

### 4.3 金额

- **禁止 `float64`** 参与金额计算与存储
- DB 用 `decimal(20,4)`，Go 用 `decimal.Decimal`
- JSON 输出用字符串，避免前端 JS 精度丢失

```go
Amount    decimal.Decimal `gorm:"type:decimal(20,4)" json:"-"`
AmountStr string          `gorm:"-" json:"amount"`
```

### 4.4 Context

- Repository / Service 方法第一个参数是 `context.Context`
- 查询必须 `db.WithContext(ctx)`
- **禁止**在 repository 层用 `context.Background()`

### 4.5 事务

```go
// 推荐
err := s.Repo.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
    if err := s.Repo.CreateWithTx(ctx, tx, order); err != nil {
        return err
    }
    return s.Repo.UpdateStatusWithTx(ctx, tx, order.ID, status)
})
```

- 事务内**禁止**调用外部 HTTP / MQ / 发事件
- 需要分布式锁时用 `pkg/rds.WithLock`，不要自己写 SETNX

### 4.6 日志

```go
logger.ZInfo("order created", zap.Uint("order_id", order.ID), zap.Uint("uid", uid))
logger.ZError("create order failed", zap.Error(err))
```

- 用结构化字段，不要拼字符串
- **禁止打印**密码、token、完整手机号、身份证
- handler 层不需要打访问日志（TraceMiddleware 已覆盖）

### 4.7 命名

- 包名小写单词，不加下划线
- 文件名：`snake_case.go`，业务文件带后缀 `_repo.go` / `_srv.go` / `_api.go`
- 接口名以 `I` 前缀仅用于跨层契约（如 `IRouter`、`IAsyncService`），其余用 `-er` 后缀

---

## 5. 中间件使用约定

| 中间件 | 用途 | 挂载位置 |
|---|---|---|
| `JWTMiddleware(rds)` | 强制登录 | 需登录的路由 |
| `OptionalJWTMiddleware(rds)` | 可选登录 | 登录后展示更多信息 |
| `AdminMiddleware()` | 后台接口 | **所有** `/api/admin/*` |
| `RateLimitMiddleware(limiter, cfg)` | 限流 | 登录、注册、发码等 |

**全局中间件链已在 `web.go` 装配**，业务路由不要重复添加：
`Recovery → Metrics → Trace → SecurityHeaders → CORS → BodyLimit → Timeout`

**安全红线**
- 任何 `/api/admin/*` 路由**必须**挂 `AdminMiddleware()`
- 令牌只从 `Authorization: Bearer` / `X-Api-Token` 读取，**禁止**支持 `?token=`
- 新增后台接口时，在 router 里加注释 `// ADMIN ONLY` 便于 review

---

## 6. 性能红线（5W 并发约束）

违反以下任一条都会在压测中暴露，**提交前必须自检**。

### 6.1 禁止无界并发

```go
// ❌ 禁止：每个请求/事件起一个 goroutine
for _, h := range handlers {
    go h(data)
}

// ✅ 正确：固定 worker 池 + 有界队列（参考 service/event_srv.go）
// ✅ 正确：用 singleflight 合并同 key 并发（参考 pkg/cache）
```

### 6.2 缓存必须走两级

```go
// ❌ 禁止：每次请求直查 DB
user, _ := repo.GetByID(ctx, uid)

// ❌ 禁止：只加 Redis（热点 key 会打爆 Redis）
b, _ := rds.Get(ctx, key)

// ✅ 正确：L1 本地 + L2 Redis + singleflight
user, err := cache.Load(ctx, cacheFactory.Get(cache.CacheUser), "user:1001",
    func(ctx context.Context) (*User, error) {
        return repo.GetByID(ctx, 1001)
    })
```

### 6.3 禁止全量日志

```go
// ❌ 禁止：每个请求都打日志（5w QPS 会把磁盘打满）
logger.ZInfo("request", ...)

// ✅ 正确：只记慢请求/错误（TraceMiddleware 已实现）
// ✅ 正确：采样（每 1000 次一条）
```

### 6.4 禁止阻塞主流程

```go
// ❌ 禁止：同步发通知/打点
sendNotification(uid)   // 外部 IO

// ✅ 正确：发事件（异步、非阻塞、有界队列）
eventSrv.Publish(UserRegisteredEvent, user)
```

### 6.5 外部调用必须带超时

```go
// ❌ 禁止：无超时的 HTTP 调用
resp, _ := http.Get(url)

// ✅ 正确：context 超时
ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
defer cancel()
req = req.WithContext(ctx)
```

### 6.6 SQL 必须有索引

- 所有 `WHERE` / `ORDER BY` / `JOIN` 字段必须建索引
- 禁止 `SELECT *`（只取需要的列）
- 分页必须用 `LIMIT`，禁止全表扫描
- 单次查询必须带 `ctx`（可被超时中断）

---

## 7. 配置约定

- 新增配置项：`config/config.go` 加字段 + `configs/config.example.yaml` 加示例
- 敏感项（密钥/密码/DSN）**必须**支持环境变量覆盖（见 `config.applyEnvOverrides`）
- 生产环境禁止 `EnableAutoMigrate: true`
- 配置校验在 `ServiceSettings.Validate()`，宁可启动失败也不要带错误配置运行
- **生产环境必填**：`TrustedProxies`（否则限流可被伪造 IP 绕过）

---

## 8. 测试约定

- 单测放同包 `*_test.go`，集成测试放 `test/integration/`
- Service 层测试用 mock Repository（手写 fake 或接口）
- 至少覆盖：状态机跃迁、权限校验、金额计算、幂等、**并发安全**
- 并发相关改动必须补：goroutine 泄漏测试（参考 `concurrency_test.go`）
- `make check` 必须全绿才能提交
- Linux CI 上跑 `go test -race ./...`（Windows 需 MinGW 运行时 DLL）

---

## 9. 提交前检查清单

**功能**
- [ ] `make wire` 已执行（改了依赖）
- [ ] `make fmt vet build test` 全绿
- [ ] 新增错误码已加 i18n（3 种语言）
- [ ] swagger 注解已补

**安全**
- [ ] 新增 `/admin/*` 路由已挂 `AdminMiddleware`
- [ ] 没有硬编码密钥
- [ ] 令牌只从 Header 读取（无 `?token=`）

**性能**
- [ ] 没有 `float64` 金额
- [ ] 没有无界 goroutine
- [ ] 热点数据走两级缓存
- [ ] Repository 方法都有 `ctx`
- [ ] 外部调用都有超时
- [ ] SQL 字段有索引
- [ ] 没有全量日志

