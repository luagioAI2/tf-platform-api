# tf-api

Go + Gin + GORM + Wire 的 API 服务骨架，从 `szw-platform-api` 中提炼出**可复用的工程底座**，
剥离全部业务逻辑，并按**支撑 5 万并发**的目标做了并发、缓存、限流、可观测性优化。

| 能力 | 实测（单实例 8 核） |
|---|---|
| 框架吞吐 | **17,345 QPS**（P99 19ms） |
| 事件总线 | **233 万事件/秒**，0 丢弃 |
| 缓存防击穿 | 500 并发 → **1 次回源**（52ms） |
| 限流精度 | 限额 600 → 通过 599 / 拒绝 101 |

---

## 快速开始（3 分钟）

```bash
# 1. 准备配置（默认用 SQLite，无需安装 MySQL/Redis）
cp configs/config.example.yaml configs/config.yaml
# 把 DBSettings.Driver 改成 'sqlite'，DataSource 改成 './data/tf.db'，
# EnableAutoMigrate 改成 true

# 2. 生成依赖注入代码（首次或改依赖后）
make wire

# 3. 运行
make run
```

验证：

```bash
curl http://127.0.0.1:8080/healthz          # liveness，只看进程
curl http://127.0.0.1:8080/health           # readiness，查 DB/Redis
curl -X POST http://127.0.0.1:8080/api/ping # {"code":200,"data":"pong"}
curl http://127.0.0.1:8080/metrics          # Prometheus 指标
```

Swagger：<http://127.0.0.1:8080/swagger/index.html>

### 用 Docker 起全套（MySQL + Redis + API）

```bash
docker compose up -d --build
curl http://127.0.0.1:8080/health
```

---

## 目录结构

```
.
├── AGENTS.md                      # ★ AI 编码规则（写代码前必读）
├── cmd/
│   ├── tf-api/main.go             # HTTP 服务入口
│   ├── worker/main.go             # ★ MQ + Cron 独立进程（解决多副本重复执行）
│   └── bench/main.go              # ★ 内置压测工具
├── internal/app/
│   ├── app.go                     # 启动编排 + 优雅关闭
│   ├── web.go                     # Gin 装配（中间件链/pprof/swagger/静态）
│   ├── providers.go               # MySQL / Redis / 缓存 / 限流的 wire provider
│   ├── injector.go / wire.go      # 依赖注入
│   ├── buildinfo/                 # 构建期版本信息
│   ├── config/                    # 配置结构 + 校验 + 环境变量覆盖
│   ├── constant/                  # 常量（header/redis key/分页）
│   ├── entities/                  # ★ 模型 + DTO + BaseModel + Paginator
│   ├── errors/                    # ★ 错误码 + 错误对象（带 i18n）
│   ├── ginx/                      # ★ 统一响应 / 参数绑定 / 取用户
│   ├── i18n/                      # ★ 多语言
│   ├── middleware/                # ★ JWT / Admin / CORS / Trace / 限流 / 超时
│   ├── utils/                     # JWT / 校验 / 随机 / HTTP
│   ├── mq/                        # Asynq 队列（骨架 + 示例 handler）
│   ├── task/                      # Cron 定时任务（骨架 + 示例 job）
│   ├── router/                    # ★ 路由注册（运维端点 + 业务路由）
│   ├── api/                       # ★ HTTP Handler（薄层）
│   ├── service/                   # ★ 业务逻辑（厚层）
│   │   ├── repository/            # ★ 数据访问 + SQL 指标
│   │   ├── cache/                 # ★ 缓存工厂（两级 + singleflight）
│   │   └── async/                 # 异步任务门面接口
│   ├── upload/                    # 存储抽象（local/minio/s3/r2）
│   └── swagger/                   # 生成物
├── pkg/                           # 与业务无关的基础包
│   ├── logger/                    # zap 封装（惰性初始化，无 nil panic）
│   ├── rds/                       # ★ Redis + 分布式锁 + 限流 + 幂等
│   ├── cache/                     # ★ 两级缓存（L1 LRU + L2 Redis + singleflight）
│   ├── metrics/                   # ★ Prometheus 指标
│   ├── http/ cjson/ math/ structure/
│   ├── captcha/ rsa_ext/
│   └── logger/hook/
├── configs/
│   ├── config.example.yaml        # ★ 配置模板（可入库，只留占位值）
│   ├── config.yaml                # 本地配置（已 gitignore）
│   └── i18n/{en,es,zh}.yaml       # ★ 多语言文案
├── docs/
│   ├── DOMAIN.md                  # ★ 领域模型（AI 必读）
│   ├── API_CONVENTIONS.md         # ★ 接口约定（AI 必读）
│   ├── PERFORMANCE.md             # ★ 容量设计与压测方法
│   └── OPTIMIZATION.md            # ★ 优化记录（问题→修复→验证）
├── migrations/                    # 版本化 SQL（生产用）
├── scripts/                       # 运维脚本
├── public/                        # 静态资源
├── Makefile  Dockerfile  docker-compose.yml  .golangci.yml
```

带 ★ 的是**日常开发主要改动的文件**。

---

## 开发流程

### 新增一个接口

严格按 `AGENTS.md` §3 的 8 步：

```
entities → repository → service → api → router → 注册 Set → make wire → 补 swagger
```

### 常用命令

```bash
make wire      # 改依赖后必须执行
make run       # 本地运行
make build     # 编译
make check     # fmt + vet + build + test 全套
make swag      # 生成 swagger
make lint      # golangci-lint

# 压测
go build -o bench ./cmd/bench
./bench -url http://127.0.0.1:8080/api/ping -m POST -c 200 -n 50000
```

---

## 配置

优先级：**环境变量 > configs/config.yaml**

| 环境变量 | 说明 |
|---|---|
| `TF_ENV` | development / test / production |
| `TF_LISTEN` | 监听地址，如 `0.0.0.0:8080` |
| `TF_DB_DSN` | MySQL DSN |
| `TF_REDIS_ADDR` | Redis `host:port` |
| `TF_REDIS_PASSWORD` | Redis 密码 |
| `TF_JWT_KEY` | C 端 JWT 签名密钥（≥16 字符） |
| `TF_ADMIN_KEY` | 后台令牌密钥（必须与 JWT key 不同） |
| `TF_CORS_ORIGINS` | CORS 白名单，逗号分隔 |
| `TF_TRUSTED_PROXIES` | 可信代理网段，逗号分隔（**生产必填**） |
| `TF_METRICS` | 是否开启 /metrics（1/0） |
| `TF_RATE_LIMIT` | 是否开启限流（1/0） |
| `TF_RATE_LIMIT_GLOBAL` | 单 IP 限额（次/窗口） |

**安全红线**
- `configs/config.yaml` 已 gitignore，**绝不入库**
- 生产环境所有密钥必须走环境变量
- 启动时校验：密钥缺失/过短/两 key 相同/无可信代理 → **直接失败**

---

## 环境行为差异

| 项 | development | production |
|---|---|---|
| Gin 模式 | debug | release |
| Redis 不可用 | 降级（缓存/锁/限流失效） | **拒绝启动** |
| AutoMigrate | 按配置生效 | 强制忽略（用 migrations/） |
| 可信代理 | 可为空 | **必填**（否则拒绝启动） |
| Swagger / pprof | 默认开 | 建议关 |

---

## 部署

### 单机

```bash
make docker-build VERSION=v1.0.0
docker run -d -p 8080:8080 \
  -e TF_ENV=production \
  -e TF_DB_DSN="user:pass@tcp(mysql:3306)/db?charset=utf8mb4&parseTime=True&loc=Local" \
  -e TF_REDIS_ADDR="redis:6379" \
  -e TF_JWT_KEY="..." \
  -e TF_ADMIN_KEY="..." \
  -e TF_TRUSTED_PROXIES="10.0.0.0/8" \
  tf-api:v1.0.0
```

### 多副本（推荐）

```yaml
# HTTP Deployment（replicas: 5）
EnableTask: false    # Cron 只跑一份
EnableMQ: false      # MQ 消费交给 worker

# Worker Deployment（replicas: 1）
# 启动命令：./tf-api worker -c configs/config.yaml
EnableTask: true
EnableMQ: true
```

---

## 从 szw-platform-api 继承并修正的内容

| 继承 | 修正 |
|---|---|
| 4 层分层 + Wire DI | `wire_gen.go` cleanup 不再为空（DB/Redis 正常关闭） |
| 统一响应 + 错误码 + i18n | `Error.Error()` 不再修改自身字段 |
| JWT / Admin 中间件 | 不再支持 `?token=`；access token 2 小时；jti 黑名单登出 |
| Redis 分布式锁 | 统一用 `pkg/rds`，废弃忽略 SetNX 返回值的实现 |
| Asynq + Cron + 事件总线 | 日志初始化顺序（原会在配置前调用导致 nil panic） |
| 上传抽象（4 种存储） | 保留 |
| 配置管理 | 新增启动校验 + 环境变量覆盖 + 日志脱敏 |
| CORS | `AllowAllOrigins` 与 `AllowCredentials` 不再同时生效 |
| AutoMigrate | 生产环境强制忽略 |
| Swagger 注解 | 写在真实 handler 上 |
| 金额 | 约定用 decimal，禁止 float64 |

### v2 新增（本次优化）

| 能力 | 解决的问题 |
|---|---|
| 两级缓存 + singleflight | 热点数据打爆 Redis、缓存失效打穿 DB |
| Redis 分布式限流 | 多副本限流失效 |
| 事件总线 worker 池 | 每事件一 goroutine 导致 OOM |
| Prometheus 指标 | 无观测手段 |
| liveness/readiness 分离 | DB 抖动导致 Pod 全部重启 |
| 超时/请求体/安全头中间件 | 慢请求堆积、大 payload、XSS |
| 可信代理校验 | 伪造 IP 绕过限流 |
| worker 进程分离 | 多副本重复执行 Cron |

详见 [`docs/OPTIMIZATION.md`](./docs/OPTIMIZATION.md)。

---

## 相关文档

- [`AGENTS.md`](./AGENTS.md) — AI 编码规则（**必读**）
- [`docs/DOMAIN.md`](./docs/DOMAIN.md) — 领域模型模板
- [`docs/API_CONVENTIONS.md`](./docs/API_CONVENTIONS.md) — 接口约定
- [`docs/PERFORMANCE.md`](./docs/PERFORMANCE.md) — 容量设计与压测
- [`docs/OPTIMIZATION.md`](./docs/OPTIMIZATION.md) — 优化记录
