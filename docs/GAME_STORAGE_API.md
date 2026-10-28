# 游戏通用存储 API

> 21 款游戏共用一套存储。本文说明设计依据、接口与迁移方法。
> 调研过程见 [`GAME_STORAGE_ANALYSIS.md`](./GAME_STORAGE_ANALYSIS.md)。

---

## 1. 为什么是 KV，不是结构化表

调研了 `kid-quest-farm-2d` 的 **21 款游戏**，结论：

**所有游戏都走同一套 `host.storage.get/set/list`，落到同一张 `gameKv` 表。**

实测各游戏用的 key：

| 游戏 | key |
|---|---|
| prizepark / cellguard / doodlederby / guardshift / hoopsneak / redbutton | `save` |
| cloudfarm | `settings` |
| claw3d | `prefs:` + 动态后缀 |
| 其余 13 款 | 动态 key 或封装在闭包里 |

**三个关键事实**：
1. key 名**不统一** —— 服务端不能假设某个固定 key
2. 值是**任意 JSON** —— 服务端无法也不应该理解它的结构
3. 游戏会**持续新增** —— 服务端不能每加一款就改 schema

所以必须用 KV。

---

## 2. 接口

### 2.1 读取

`POST /api/game/kv/get` 🔒

```json
{ "game_id": "cloudfarm", "key": "save" }
```

**响应**

```json
{
  "code": 200,
  "data": {
    "key": "save",
    "value": { "level": 5, "coins": 120, "crops": [{ "id": "radish", "n": 3 }] }
  }
}
```

未存过时 `value` 为 `null`（与前端 `undefined` 语义对齐）。

---

### 2.2 写入

`POST /api/game/kv/set` 🔒

```json
{
  "game_id": "cloudfarm",
  "key": "save",
  "value": { "level": 5, "coins": 120 },
  "expected_version": 0
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| game_id | 是 | 游戏 ID |
| key | 是 | 键名（≤128 字符，不能以 `__` 开头） |
| value | 是 | 任意 JSON（≤1MB） |
| expected_version | 否 | 乐观锁版本号（>0 时启用） |

**响应**

```json
{ "code": 200, "data": { "key": "save", "version": 1 } }
```

**整块覆盖** —— 与前端 `set()` 语义一致，不做局部更新。

---

### 2.3 列出全部

`POST /api/game/kv/list` 🔒

```json
{ "game_id": "cloudfarm" }
```

**响应**

```json
{
  "code": 200,
  "data": {
    "game_id": "cloudfarm",
    "count": 1,
    "data": { "save": { "level": 5 } }
  }
}
```

**自动过滤保留键**（`__` 开头），与前端 `storage.list()` 一致。

---

### 2.4 删除

`POST /api/game/kv/delete` 🔒

**删单键**

```json
{ "game_id": "cloudfarm", "key": "save" }
```

**删整个游戏（卸载）**

```json
{ "game_id": "cloudfarm" }
```

`key` 为空 = 卸载游戏，删除该用户该游戏的全部存档（含保留键）。

---

### 2.5 存储用量

`POST /api/game/kv/usage` 🔒

```json
{}
```

**响应**

```json
{
  "code": 200,
  "data": { "total_bytes": 1240, "max_bytes": 67108864 }
}
```

---

## 3. 设计约束

### 3.1 命名空间

`(uid, game_id, key)` 三元组唯一。

对应前端 `gameKvKey(gameId, key)` = `${gameId}:${key}`，服务端额外加上 `uid` 隔离。

### 3.2 保留键

`__` 开头是**宿主保留键**：

| key | 用途 |
|---|---|
| `__coin` | 游戏币账本（**服务端不存这里**，见 §4） |
| `__bundle` | 插件代码（服务端不存，包体走 CDN） |
| `__assets` | 资源根路径 |

**服务端硬校验**：客户端读写保留键返回 `604 权限不足`。

> 为什么要拦：前端 `storage.set('__coin', [])` 能清空自己的币账本，
> 绕过 `mintGameCoin` 的「只进不出」守卫。服务端不能留这个后门。

### 3.3 配额

| 限制 | 值 | 理由 |
|---|---|---|
| 单值大小 | 1MB | 正常存档几十 KB；`__bundle` 走 CDN 不占 KV |
| 单游戏 key 数 | 64 | 正常游戏用 1-5 个 key |
| 单用户总大小 | 64MB | 21 款游戏 × 平均 3MB 上限 |

### 3.4 并发控制

多设备同时写同一 key 时：

```json
{ "expected_version": 3 }
```

- 版本匹配 → 更新成功，`version` 自增
- 版本不匹配 → 返回 `10040701 存档版本冲突`

不传 `expected_version` 时走「最后写入胜出」（适合单设备场景）。

---

## 4. 游戏币（独立表，不是 KV）

### 4.1 为什么不塞进 KV

前端把币账本塞在 `gameKv` 的 `__coin` 键里（值是 `{delta,memo,at}[]`），
但**服务端不能照抄**：

| 维度 | 前端 gameKv | 服务端需要 |
|---|---|---|
| 并发 | 单用户单设备，Dexie 事务够用 | 5W 并发，需要行锁 |
| 对账 | 无 | `balance_after` 快照 |
| 幂等 | 不需要 | 需要（网络重试） |
| 查询 | 只读自己 | 排行榜 / 统计 |

所以服务端用**两张独立表**：

- `game_coin_ledger` —— 账本（真相）
- `game_wallet` —— 余额快照（加速查询）

### 4.2 经济链（不可逆）

```
积分 ──Buy──▶ 游戏币 ──Redeem──▶ 兑换券
                 ▲
                 └──Mint── 插件铸币（只进不出）
```

**三条不变量**：
1. 游戏币**不能**换回积分（否则「坐着玩」就能刷积分）
2. 兑换券**不能**换回积分
3. 一局期望产出 × 汇率 **必须 <** 一局成本

### 4.3 接口

| 接口 | 说明 |
|---|---|
| `POST /api/game/coin/info` | 查单个游戏的币余额 |
| `POST /api/game/coin/list` | 查所有游戏的币余额 |
| `POST /api/game/coin/buy` | 用积分买币（**同一事务**扣分 + 记币） |
| `POST /api/game/coin/spend` | 消费币（玩一局） |
| `POST /api/game/coin/mint` | 铸币（插件调用，只进不出） |
| `POST /api/game/coin/redeem` | 币换兑换券（**同一事务**烧币 + 发券） |
| `POST /api/game/coin/ledger` | 查币账本 |

### 4.4 实测验证

```
110 积分
  → 买 10 币（5 积分/币，花 50）→ 剩 60 积分，10 币
  → 消费 3 币                    → 剩 7 币
  → 兑换 5 币                    → 剩 2 币，得 5 兑换券
最终：60 积分 + 5 兑换券 + 2 币 ✓
```

---

## 5. 前端迁移指南

### 5.1 替换 `host.storage`

**原实现**（`src/games/hostApi.ts`）：

```ts
storage: {
  get: async <T,>(key: string) => {
    assertNotReserved(key)
    return getGameKv<T>(gameId, key)
  },
  set: async (key: string, value: unknown) => {
    assertNotReserved(key)
    await setGameKv(gameId, key, value)
  },
  list: async () => {
    const all = await listGameKv(gameId)
    // 过滤保留键...
  },
}
```

**新实现**：

```ts
storage: {
  get: async <T,>(key: string) => {
    const r = await api('/api/game/kv/get', { game_id: gameId, key })
    return r.value as T | undefined
  },
  set: async (key: string, value: unknown) => {
    await api('/api/game/kv/set', { game_id: gameId, key, value })
  },
  list: async () => {
    const r = await api('/api/game/kv/list', { game_id: gameId })
    return r.data
  },
}
```

**注意**：`assertNotReserved` 可以删掉 —— 服务端会拦（返回 604）。

### 5.2 替换游戏币

| 原函数 | 新接口 |
|---|---|
| `gameCoinBalanceOf(gameId)` | `POST /api/game/coin/info` |
| `buyGameCoins(...)` | `POST /api/game/coin/buy` |
| `spendGameCoin(...)` | `POST /api/game/coin/spend` |
| `addGameCoin(...)` | `POST /api/game/coin/mint` |
| `redeemGameCoins(...)` | `POST /api/game/coin/redeem` |

**关键差异**：前端 `spendGameCoin` 是本地事务，服务端是**行锁 + 版本号**。
并发安全性更强，但要注意**网络重试要带 `client_request_id`**（幂等键）。

### 5.3 离线优先的妥协

原项目是**单机优先**（无网也能玩）。改成线上后有两条路：

**方案 A：在线优先（简单）**

所有存档操作直接调 API，失败提示重试。

**方案 B：离线队列（复杂但体验好）**

```
本地 Dexie 保留为缓存 + 待同步队列
  ↓ 有网时
批量 PUT 到服务端
  ↓
服务端返回权威数据，覆盖本地
```

> 建议先做方案 A。游戏存档是「整块覆盖」，离线队列的冲突合并很复杂
> （两个设备都玩了 10 分钟，谁的数据对？）。

---

## 6. 错误码

| 码 | 含义 |
|---|---|
| 10040101 | 游戏不存在 |
| 10040201 | 游戏已下架 |
| 10040401 | 未拥有该游戏 |
| 10040601 | 存档过大（>1MB） |
| 10040701 | 存档版本冲突（多设备） |
| 10040801 | 保留键不可写 |
| 10040901 | 游戏币不足 |
| 604 | 权限不足（写保留键 / 未拥有游戏） |

---

## 7. 已知问题与修复记录

### 7.1 SQLite `LIKE` 通配符陷阱（已修复）

**现象**：写入成功，但 `list` 返回空。

**原因**：SQL 的 `LIKE` 里 `_` 是**单字符通配符**，不是字面下划线。
直接写 `NOT LIKE '__%'` 会匹配「任意 2 个字符 + 任意内容」，
把 `save` 这类正常 key 也排除了。

**修复**：

```go
// ❌ 错误
Where("kv_key NOT LIKE ?", "__%")

// ✅ 正确：用 ESCAPE 把下划线转义成字面量
Where(`kv_key NOT LIKE ? ESCAPE '\'`, `\_\_%`)
```

**教训**：这个 bug 不会报错，只会静默返回空结果 —— 典型的「看起来能用」的坑。
