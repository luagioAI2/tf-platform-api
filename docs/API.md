# Kid Quest Farm API 文档

> 为 `kid-quest-farm-2d` 提供线上化后端：用户、登录注册（普通/微信/手机号）、游戏商城、游戏列表。
> 服务地址：`http://127.0.0.1:8080`（本地）｜生产环境见部署文档

---

## 目录

1. [通用约定](#1-通用约定)
2. [认证流程](#2-认证流程)
3. [认证接口](#3-认证接口)
4. [用户接口](#4-用户接口)
5. [钱包接口](#5-钱包接口)
6. [游戏接口](#6-游戏接口)
7. [数据模型](#7-数据模型)
8. [错误码](#8-错误码)
9. [前端接入示例](#9-前端接入示例)
10. [本地存储迁移指南](#10-本地存储迁移指南)

---

## 1. 通用约定

### 1.1 基础

| 项 | 说明 |
|---|---|
| Base URL | `/api` |
| 方法 | **统一 POST + JSON**（除健康检查） |
| 编码 | UTF-8 |
| 时间 | Unix 秒（int64） |
| 金额/积分 | 整数（int64），不用浮点 |

### 1.2 请求头

```http
Content-Type: application/json
Authorization: Bearer <access_token>   # 需登录的接口
X-Device-ID: <设备唯一标识>              # 可选，用于登录设备管理
Accept-Language: zh                     # 可选，zh/en/es
```

### 1.3 响应格式

**成功**

```json
{ "code": 200, "data": { ... } }
```

**失败**

```json
{ "code": 10020301, "error": "余额不足" }
```

> ⚠️ **业务失败也返回 HTTP 200**，前端只看 `code`。
> 例外：401（未登录）、429（限流）、413（请求体过大）、503（依赖故障）。

### 1.4 分页

请求：

```json
{ "page": 1, "pageSize": 20 }
```

响应：

```json
{ "code": 200, "data": { "page": 1, "pageSize": 20, "count": 2, "list": [...] } }
```

---

## 2. 认证流程

### 2.1 手机号注册

```
1. POST /api/auth/send-sms-code   {mobile, scene:"register"}
2. 用户收到 6 位验证码
3. POST /api/auth/register        {mobile, password, code, nickname}
   → 返回 access_token + refresh_token + user
```

### 2.2 密码登录

```
POST /api/auth/login  {mobile, password}
→ access_token + refresh_token + user
```

### 2.3 验证码登录（免密，未注册自动注册）

```
1. POST /api/auth/send-sms-code   {mobile, scene:"login"}
2. POST /api/auth/sms-login       {mobile, code}
```

### 2.4 微信登录

```
1. 小程序端调用 wx.login() 拿到 code
2. POST /api/auth/wechat-login  {code, nickname?, avatar?}
   → 已绑定：直接返回 token
   → 未绑定：自动创建用户并绑定，返回 token（mobile 为空）
3. （可选）POST /api/auth/bind-mobile  {mobile, code, password}
   绑定手机号后可用手机号登录
```

### 2.5 令牌刷新

```
POST /api/auth/refresh  {refresh_token}
→ 新的 access_token + refresh_token（旧的 refresh_token 立即失效）
```

### 2.6 登出

```
POST /api/auth/logout  {refresh_token}   # 需 Authorization
→ refresh_token 撤销 + access_token 加入黑名单
```

---

## 3. 认证接口

### 3.1 发送验证码

`POST /api/auth/send-sms-code`

**请求**

```json
{
  "mobile": "13800138000",
  "scene": "register"
}
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| mobile | string | 是 | 手机号（8-24 位，可带 `+`） |
| scene | string | 是 | `register` / `login` / `reset_password` / `bind_mobile` |

**响应**

```json
{ "code": 200, "data": { "sent": true } }
```

**限制**

- 同一手机号 60 秒内只能发 1 次
- 同一手机号每天最多 10 条
- 同一 IP 每小时最多 20 条
- 验证码 5 分钟有效，最多尝试 5 次

> 开发环境验证码会打到日志：`grep "DEV SMS CODE" logs/app.log`

---

### 3.2 注册

`POST /api/auth/register`

**请求**

```json
{
  "mobile": "13800138000",
  "password": "test123456",
  "code": "123456",
  "nickname": "小明"
}
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| mobile | string | 是 | 手机号 |
| password | string | 是 | 6-64 位 |
| code | string | 是 | 6 位数字验证码 |
| nickname | string | 否 | 默认「宝贝 + 手机后 4 位」 |

**响应**

```json
{
  "code": 200,
  "data": {
    "access_token": "eyJhbGci...",
    "refresh_token": "0947a2a4...",
    "expires_in": 7200,
    "user": {
      "uid": 1,
      "username": "",
      "mobile": "138****8000",
      "mobile_verified": true,
      "nickname": "小明",
      "avatar": "",
      "gender": 0,
      "language": "zh",
      "is_guest": false,
      "has_password": true,
      "has_wechat": false,
      "created_at": 1793176868
    },
    "is_new_user": true
  }
}
```

**副作用**：自动创建钱包并赠送 **100 积分**（注册礼）。

---

### 3.3 密码登录

`POST /api/auth/login`

```json
{ "mobile": "13800138000", "password": "test123456" }
```

响应同注册（`is_new_user: false`）。

---

### 3.4 验证码登录

`POST /api/auth/sms-login`

```json
{ "mobile": "13800138000", "code": "123456" }
```

未注册的手机号会**自动注册**（`is_new_user: true`）。

---

### 3.5 微信登录

`POST /api/auth/wechat-login`

```json
{ "code": "wx_login_code", "nickname": "微信小宝", "avatar": "https://..." }
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| code | string | 是 | `wx.login()` 返回的临时凭证 |
| nickname | string | 否 | 微信不返回时由前端传 |
| avatar | string | 否 | 同上 |

**响应**：同注册。首次登录 `mobile` 为空，需后续绑定。

> 开发环境未配置微信 AppID/Secret 时返回 mock session，便于前端联调。
> 生产环境未配置会直接报错 `wechat-auth-failed`。

---

### 3.6 绑定手机号

`POST /api/auth/bind-mobile` 🔒

```json
{ "mobile": "13800138000", "code": "123456", "password": "optional" }
```

用于微信用户补充手机号（之后可用手机号登录）。

---

### 3.7 刷新令牌

`POST /api/auth/refresh`

```json
{ "refresh_token": "0947a2a4..." }
```

> **令牌轮换**：每次刷新旧 refresh_token 立即失效，防重放。

---

### 3.8 登出

`POST /api/auth/logout` 🔒

```json
{ "refresh_token": "0947a2a4..." }
```

---

### 3.9 重置密码

`POST /api/auth/reset-password`

```json
{ "mobile": "13800138000", "code": "123456", "new_password": "newpass123" }
```

> 重置后该用户**所有设备强制下线**。

---

### 3.10 修改密码

`POST /api/auth/change-password` 🔒

```json
{ "old_password": "test123456", "new_password": "newpass123" }
```

---

## 4. 用户接口

### 4.1 获取资料

`POST /api/user/profile` 🔒

```json
{}
```

响应 `data` 为 `UserInfo`（见 3.2）。

---

### 4.2 更新资料

`POST /api/user/update-profile` 🔒

```json
{
  "nickname": "小红",
  "avatar": "https://cdn.example.com/a.png",
  "gender": 2,
  "language": "zh"
}
```

所有字段可选，只更新传入的字段。

---

## 5. 钱包接口

### 5.1 查询余额

`POST /api/wallet/info` 🔒

```json
{}
```

**响应**

```json
{
  "code": 200,
  "data": { "uid": 1, "points": 600, "coupon": 0, "diamond": 0 }
}
```

| 字段 | 说明 |
|---|---|
| points | 积分 🪙 — 完成任务获得，用于买种子/游戏 |
| coupon | 兑换券 🎫 — 农场产出卖出获得，只能用于兑换商城 |
| diamond | 钻石 💎 — 付费/兑换码获得，用于买游戏 |

> ⚠️ **三套币互不相通**（唯一例外：兑换商城付款时 1 兑换券 ≡ 1 积分）。

---

### 5.2 查询账本

`POST /api/wallet/ledger` 🔒

```json
{ "page": 1, "pageSize": 20, "currency": "points", "source": "" }
```

| 字段 | 必填 | 说明 |
|---|---|---|
| currency | 否 | `points` / `coupon` / `diamond`，不传则全部 |
| source | 否 | 按来源筛选 |

**响应**

```json
{
  "code": 200,
  "data": {
    "page": 1,
    "pageSize": 20,
    "count": 3,
    "list": [
      {
        "id": 3,
        "currency": "points",
        "delta": -500,
        "balance_after": 600,
        "source": "game_purchase",
        "ref_id": "prizepark",
        "memo": "购买游戏：抓奖乐园",
        "created_at": 1793177147
      }
    ]
  }
}
```

---

### 5.3 结转本地版留下的余额

`POST /api/wallet/import` 🔒

从**本地版升级上来**的设备，Dexie 的三张账本里还留着旧行；而客户端的余额是
整表求和，于是界面会显示「服务端 + 本机旧账」—— 孩子看到 1219 分，兑换时却被
服务端拒掉，只说「积分不够」。这个接口把那份旧账一次性搬到账号里。

```json
{ "key": "ins_ab12:1000:7", "points": 1000, "coupons": 7, "counts": { "points": 12 } }
```

| 字段 | 必填 | 说明 |
|---|---|---|
| key | 是 | 幂等键（客户端给：安装 id + 载荷）。≤ 64 字符 |
| points / coupons / diamonds | 否 | 要结转的数量，非负、≤ 1000000 |
| counts | 否 | 各币种笔数，只写进备注（服务端不校验） |

**响应**

```json
{
  "code": 200,
  "data": {
    "imported": true,
    "points": 1000,
    "coupons": 7,
    "diamonds": 0,
    "wallet": { "uid": 3, "points": 1219, "coupon": 43, "diamond": 0 }
  }
}
```

**几条契约**

- **幂等按币种算**：键是 `legacy_import:<uid>:<key>:<currency>`。重试时已经写过
  的币种跳过（`imported=false`、数量为 0），没写过的照常写。
- **只搬余额不搬流水**：每种币最多写一行，来源 `legacy_import`，备注带笔数。
- **全是 0 返回 `10020901`**（结转内容不合法）—— 客户端算错了要吵，不要静默成功。
- 三套币在**一个事务**里，要么全成、要么全不成。

---

## 6. 游戏接口

### 6.1 游戏列表

`POST /api/game/list`（登录可选）

```json
{ "page": 1, "pageSize": 20, "kind": "", "keyword": "", "only_owned": false }
```

| 字段 | 必填 | 说明 |
|---|---|---|
| kind | 否 | 玩法分类筛选 |
| keyword | 否 | 名称/简介模糊搜索 |
| only_owned | 否 | 只看已拥有 |

**响应**

```json
{
  "code": 200,
  "data": {
    "page": 1,
    "pageSize": 20,
    "count": 2,
    "list": [
      {
        "game_id": "cloudfarm",
        "name": "云边小农场",
        "emoji": "🌾",
        "tagline": "种菜、养动物、把收成拿到市场上卖掉。",
        "kind": "think",
        "tint": "grass",
        "status": "available",
        "cover_url": "",
        "age_hint": "6 岁以上",
        "session_hint": "一次 5~10 分钟",
        "price_points": 0,
        "price_diamond": 0,
        "owned": true,
        "can_afford": true
      },
      {
        "game_id": "prizepark",
        "name": "抓奖乐园",
        "emoji": "🧸",
        "tagline": "3D 机台上抓奖品，凑成一套再换成兑换券。",
        "kind": "think",
        "tint": "berry",
        "status": "available",
        "price_points": 500,
        "owned": false,
        "can_afford": true
      }
    ]
  }
}
```

> 未登录时 `owned` 全为 `false`（除 `default_owned` 的游戏）。

---

### 6.2 游戏详情

`POST /api/game/detail`（登录可选）

```json
{ "game_id": "cloudfarm" }
```

**响应**：在列表项基础上增加

```json
{
  "learn": ["买种子要先花钱，收成要等 —— 投入在前，回报在后", "..."],
  "about": ["一块地、一把种子、一个市场。...", "..."],
  "shots": [
    { "src": "/store/cloudfarm/1.webp", "caption": "田里一共 12 块地，种下去等它长大" }
  ],
  "entry_url": ""
}
```

---

### 6.3 购买游戏

`POST /api/game/purchase` 🔒

```json
{
  "game_id": "prizepark",
  "currency": "points",
  "client_request_id": "uuid-here"
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| game_id | 是 | 游戏 ID |
| currency | 否 | `points` / `diamond`，不传则自动选择（优先钻石） |
| client_request_id | 否 | 幂等键，建议传 UUID |

**响应**

```json
{
  "code": 200,
  "data": {
    "order_id": "G179317714700017100",
    "game_id": "prizepark",
    "currency": "points",
    "amount": 500,
    "wallet": { "uid": 1, "points": 600, "coupon": 0, "diamond": 0 },
    "already_owned": false
  }
}
```

**幂等行为**

- 已拥有该游戏 → `already_owned: true`，不扣款
- 相同 `client_request_id` 重复提交 → 返回首次结果，不重复扣款

**事务保证**：扣款 + 授予 + 订单在**同一事务**内完成，任一失败全部回滚。

---

### 6.4 我的游戏

`POST /api/game/my-games` 🔒

```json
{}
```

**响应**

```json
{
  "code": 200,
  "data": {
    "count": 2,
    "list": [ /* GameItem 数组，含默认拥有的游戏 */ ]
  }
}
```

---

## 7. 数据模型

### 7.1 User

| 字段 | 类型 | 说明 |
|---|---|---|
| uid | uint | 用户 ID |
| username | string | 用户名（可选） |
| mobile | string | 手机号（脱敏返回） |
| mobile_verified | bool | 手机号已验证 |
| nickname | string | 昵称 |
| avatar | string | 头像 URL |
| gender | uint8 | 0 未知 1 男 2 女 |
| language | string | zh / en / es |
| is_guest | bool | 是否游客 |
| has_password | bool | 是否设置过密码 |
| has_wechat | bool | 是否绑定微信 |
| created_at | int64 | 注册时间（Unix 秒） |

### 7.2 Game

| 字段 | 说明 |
|---|---|
| game_id | 业务 ID（cloudfarm / prizepark） |
| name | 游戏名 |
| emoji | 图标 |
| tagline | 一句话介绍 |
| kind | 玩法分类 |
| tint | 图标底色 |
| status | available / coming_soon / offline |
| price_points | 积分售价（0 = 免费） |
| price_diamond | 钻石售价 |
| owned | 当前用户是否拥有 |
| can_afford | 余额是否够买 |

### 7.3 LedgerEntry

| 字段 | 说明 |
|---|---|
| currency | points / coupon / diamond |
| delta | 变动量（正收入负支出） |
| balance_after | 变动后余额 |
| source | 来源（见下表） |
| ref_id | 关联对象 ID |
| memo | 备注 |

---

## 8. 错误码

### 8.1 通用

| 码 | 含义 |
|---|---|
| 200 | 成功 |
| 500 | 系统错误 |
| 501 | 服务器错误 |
| 601 | 服务不存在 |
| 602 | 资源不存在 |
| 604 | 权限不足 |
| 605 | 未登录 |
| 606 | 参数错误 |
| 607 | 参数校验失败 |
| 608 | 请求过于频繁 |
| 610 | 重复操作 |
| 612 | 请求超时 |
| 613 | 服务不可用 |

### 8.2 用户域 `1001xxxx`

| 码 | 含义 |
|---|---|
| 10010101 | 用户不存在 |
| 10010201 | 用户已存在 |
| 10010202 | 手机号已注册 |
| 10010301 | 密码错误 |
| 10010302 | 两次密码不一致 |
| 10010303 | 尚未设置密码 |
| 10010401 | 账号已禁用 |
| 10010501 | 令牌无效 |
| 10010502 | 令牌已过期 |
| 10010601 | 验证码错误 |
| 10010602 | 验证码已过期 |
| 10010603 | 验证码尝试次数过多 |
| 10010701 | 短信发送过于频繁 |
| 10010702 | 短信发送失败 |
| 10010801 | 未绑定手机号 |
| 10010901 | 微信授权失败 |
| 10010902 | 微信已绑定其他账号 |
| 10010903 | 账号未绑定微信 |

### 8.3 钱包域 `1002xxxx`

| 码 | 含义 |
|---|---|
| 10020101 | 订单不存在 |
| 10020301 | 余额不足 |
| 10020401 | 金额不合法 |
| 10020501 | 钱包不存在 |
| 10020601 | 币种不支持 |
| 10020701 | 余额并发冲突 |
| 10020801 | 重复记账 |

### 8.4 游戏域 `1004xxxx`

| 码 | 含义 |
|---|---|
| 10040101 | 游戏不存在 |
| 10040201 | 游戏已下架 |
| 10040301 | 已拥有该游戏 |
| 10040401 | 未拥有该游戏 |
| 10040501 | 该游戏不可购买 |

---

## 9. 前端接入示例

### 9.1 请求封装（TypeScript）

```ts
const BASE_URL = 'http://127.0.0.1:8080'

let accessToken = localStorage.getItem('access_token') || ''
let refreshToken = localStorage.getItem('refresh_token') || ''

async function api<T>(path: string, body: unknown = {}): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(accessToken ? { Authorization: `Bearer ${accessToken}` } : {}),
    },
    body: JSON.stringify(body),
  })

  // 401：尝试刷新令牌
  if (res.status === 401 && refreshToken) {
    const ok = await doRefresh()
    if (ok) return api<T>(path, body)
    clearTokens()
    throw new Error('登录已过期')
  }

  const json = await res.json()
  if (json.code !== 200) {
    throw new ApiError(json.code, json.error)
  }
  return json.data as T
}

async function doRefresh(): Promise<boolean> {
  try {
    const res = await fetch(`${BASE_URL}/api/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    })
    const json = await res.json()
    if (json.code !== 200) return false
    setTokens(json.data.access_token, json.data.refresh_token)
    return true
  } catch {
    return false
  }
}
```

### 9.2 典型调用

```ts
// 发验证码
await api('/api/auth/send-sms-code', { mobile: '13800138000', scene: 'register' })

// 注册
const login = await api<LoginResp>('/api/auth/register', {
  mobile: '13800138000',
  password: 'test123456',
  code: '123456',
  nickname: '小明',
})
setTokens(login.access_token, login.refresh_token)

// 游戏列表
const games = await api<{ list: GameItem[] }>('/api/game/list', { page: 1, pageSize: 20 })

// 购买
const result = await api<PurchaseResp>('/api/game/purchase', {
  game_id: 'prizepark',
  client_request_id: crypto.randomUUID(),
})
```

---

## 10. 本地存储迁移指南

`kid-quest-farm-2d` 目前用 IndexedDB（Dexie）存全部数据。迁移到线上 API 时：

### 10.1 不变的部分

- **游戏本体逻辑**（`src/domain/*` 的纯函数）不用改 —— 它们本来就是纯计算
- **UI 层**只改数据来源：`db.ts` → `api.ts`

### 10.2 需要改的部分

| 原 Dexie 表 | 对应 API |
|---|---|
| `meta`（设置） | `POST /api/user/update-profile` + 本地缓存 |
| `ledger` / `harvestLedger` / `mallLedger` | `POST /api/wallet/ledger`（统一用 currency 区分） |
| — 余额计算 | `POST /api/wallet/info`（服务端算好，不用前端 SUM） |
| `tasks` / `taskInstances` | **本轮未实现**，需后续扩展 |
| `inventory` / `animals` | **本轮未实现**，需后续扩展 |
| `redeemItems` / `redeemRecords` | **本轮未实现**，需后续扩展 |

### 10.3 三套币的映射

原项目三张表 → 服务端一张表 + `currency` 字段：

```ts
// 原：分别读三张表算余额
const points = sum(ledger)
const coupon = sum(harvestLedger)
const diamond = sum(mallLedger)

// 新：一次接口拿三个余额
const { points, coupon, diamond } = await api('/api/wallet/info')
```

### 10.4 离线优先的妥协

原项目是**单机优先**（无网也能用）。改成线上后有两条路：

**方案 A：在线优先（简单）**

所有操作直接调 API，失败提示重试。适合有网环境。

**方案 B：离线队列（复杂但体验好）**

```
本地 Dexie 保留为缓存 + 待同步队列
  ↓ 有网时
批量 POST 到服务端
  ↓
服务端返回权威余额，覆盖本地
```

> 建议先做方案 A，验证业务跑通后再加离线队列。

---

## 附：健康检查与监控

| 端点 | 用途 |
|---|---|
| `GET /healthz` | 存活探针（只看进程） |
| `GET /health` | 就绪探针（查 DB/Redis） |
| `GET /metrics` | Prometheus 指标 |
| `GET /swagger/index.html` | Swagger UI |

### 关键指标

```
tf_http_requests_total{method,path,status}
tf_http_request_duration_seconds{method,path}
tf_db_query_duration_seconds
tf_cache_hits_total{cache,layer}
```
