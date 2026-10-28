# 接口约定（API CONVENTIONS）

> 所有接口必须遵守本约定。AI 生成新接口时以此为准。

---

## 1. 路径规范

```
/api/{模块}/{动作}
```

- 全部小写，单词用 `-` 连接（kebab-case）
- 模块名用单数：`/api/order/create`（不是 `/api/orders/create`）
- 动作放在最后：`create` / `get` / `list` / `update` / `delete` / `export`
- 后台接口统一前缀 `/api/admin/`
- 版本化（如需要）：`/api/v1/...`

示例：

```
POST /api/user/login
POST /api/user/get-profile
POST /api/order/create
POST /api/order/list
POST /api/admin/order/approve
```

---

## 2. HTTP 方法

**本项目统一用 POST 传 JSON**（与前端约定，避免 GET 参数长度与缓存问题）。

例外：
- `GET /health`、`GET /healthz` — 健康检查
- `GET /public/*` — 静态资源
- `GET /swagger/*` — API 文档
- 文件下载可用 GET

---

## 3. 请求格式

### 3.1 Header

| Header | 必填 | 说明 |
|---|---|---|
| `Content-Type` | ✓ | `application/json` |
| `Authorization` | 登录接口必填 | `Bearer <access_token>` |
| `X-Api-Token` | 后台接口必填 | HMAC 后台令牌 |
| `Accept-Language` | ✗ | `en` / `es` / `zh`，缺省用配置默认语言 |
| `X-Request-ID` | ✗ | 链路追踪 ID，缺省服务端生成并回传 |

### 3.2 分页请求

```json
{
  "page": 1,
  "pageSize": 30,
  "keyword": "可选筛选条件"
}
```

- `page` 从 1 开始，缺省 1
- `pageSize` 缺省 30，最大 200
- 内嵌 `entities.Paginator` 即可复用

---

## 4. 响应格式

### 4.1 统一结构

```go
type Resp struct {
    Code int         `json:"code"`            // 200 = 成功
    Msg  string      `json:"error,omitempty"` // 失败时的提示
    Data interface{} `json:"data,omitempty"`
}
```

成功：

```json
{ "code": 200, "data": { "id": 1, "order_no": "A20260101001" } }
```

失败：

```json
{ "code": 10020101, "error": "订单不存在" }
```

### 4.2 HTTP 状态码

| 场景 | HTTP | body.code |
|---|---|---|
| 业务成功 | 200 | 200 |
| 业务失败 | 200 | 业务错误码 |
| 未登录 / 令牌无效 | 401 | 10010501 等 |
| 限流 | 429 | 608 |
| 依赖不可用（仅 /health） | 503 | — |
| 服务端未捕获异常 | 200 | 500 |

**关键约定**：业务失败也返回 HTTP 200，前端只看 `code`。

### 4.3 分页响应

```json
{
  "code": 200,
  "data": {
    "page": 1,
    "pageSize": 30,
    "count": 128,
    "list": [ ... ]
  }
}
```

---

## 5. 错误码规范

### 5.1 分段

```
200            成功
500 / 501      系统级
601 ~ 611      通用（参数/权限/资源/状态/限流）
10 01 xx xx    用户域
10 02 xx xx    订单域
10 03 xx xx    文件域
10 09 xx xx    后台/运维域
```

格式：`10` + `域(2位)` + `序号(4位)`，共 8 位。

### 5.2 新增错误码流程

1. `internal/app/errors/code.go` 加常量
2. 同一个文件 `ErrCodeMsg` 加映射（value 是 i18n key）
3. `configs/i18n/en.yaml`、`es.yaml`、`zh.yaml` 三处都加 key
4. 前端同步

**自检命令**（CI 可加）：

```bash
# 检查 ErrCodeMsg 的每个 key 是否都存在于 3 个 yaml
grep -oP '"[a-z-]+"' internal/app/errors/code.go | tr -d '"' | sort -u | while read k; do
  for f in configs/i18n/*.yaml; do
    grep -q "^$k:" "$f" || echo "MISSING $k in $f"
  done
done
```

---

## 6. 字段命名

| 类型 | 规范 | 示例 |
|---|---|---|
| JSON 字段 | `snake_case` | `order_no`、`created_at` |
| Go 字段 | `CamelCase` | `OrderNo`、`CreatedAt` |
| DB 列 | `snake_case` | `order_no` |
| 时间 | Unix 秒（int64） | `created_at: 1767225600` |
| 金额 | 字符串（避免精度丢失） | `"amount": "100.0000"` |
| 布尔 | `is_xxx` / `has_xxx` | `is_active`、`has_paid` |

---

## 7. 幂等

写接口需支持幂等时：

- 请求体带 `request_id`（客户端生成 UUID）
- 服务端用 Redis `SETNX idem:{request_id}` 去重，TTL 24h
- 重复请求返回首次结果，或 `errors.DuplicateOperation`

---

## 8. 文件上传

```
POST /api/file/upload
Content-Type: multipart/form-data
字段名：file
```

响应：

```json
{ "code": 200, "data": { "url": "https://...", "name": "a1b2c3.png" } }
```

限制见 `UploadSettings`（类型、大小、扩展名）。

---

## 9. 示例：一个完整接口

**请求**

```http
POST /api/order/create
Content-Type: application/json
Authorization: Bearer eyJhbGci...
Accept-Language: zh

{
  "amount": "100.0000",
  "remark": "测试订单"
}
```

**成功响应**

```http
HTTP/1.1 200 OK
X-Request-ID: 1c752af7-42ac-4dba-95f9-e26c40b468fd

{
  "code": 200,
  "data": { "id": 1, "order_no": "A20260101001", "status": 0 }
}
```

**失败响应**

```http
HTTP/1.1 200 OK

{ "code": 10020301, "error": "余额不足" }
```
