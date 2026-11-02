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
    "value": { "level": 5, "coins": 120, "crops": [{ "id": "radish", "n": 3 }] },
    "version": 3,
    "exists": true,
    "size": 51,
    "updated_at": 1793184189
  }
}
```

| 字段 | 说明 |
|---|---|
| value | 存档内容，未存过时为 `null`（与前端 `undefined` 语义对齐） |
| **version** | **当前版本号。下次乐观锁写入必须回传它** |
| exists | 是否存过（`false` 时 `version=0`、`value=null`） |
| size | 内容字节数 |
| updated_at | 最后更新时间（Unix 秒） |

> ⚠️ **一定要保存 `version`**。不传 `expected_version` 就往回写，
> 等于放弃多设备冲突检测（两台设备同时玩，后写的会静默覆盖先写的）。

---

### 2.2 写入

`POST /api/game/kv/set` 🔒

```json
{
  "game_id": "cloudfarm",
  "key": "save",
  "value": { "level": 5, "coins": 120 },
  "expected_version": 3
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| game_id | 是 | 游戏 ID |
| key | 是 | 键名（≤128 字符，不能以 `__` 开头） |
| value | 是 | 任意 JSON（≤1MB） |
| expected_version | 否 | 乐观锁版本号（**>0 时启用**，回传 `get` 拿到的 `version`） |

**响应**

```json
{ "code": 200, "data": { "key": "save", "version": 4 } }
```

**整块覆盖** —— 与前端 `set()` 语义一致，不做局部更新。

**版本冲突**：`expected_version` 与当前不符时返回 `10040701`，
客户端应重新 `get` 拿最新值后再决定是否覆盖。

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
    "data": { "save": { "level": 5 }, "bag": [{ "item": "radish", "n": 3 }] },
    "versions": { "save": 3, "bag": 0 },
    "count": 2
  }
}
```

> **`data` 的形状与前端 `storage.list()` 完全一致**（`Record<string, unknown>`），
> 前端替换实现时不用改插件契约。`versions` 是服务端附加的能力。

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

### 2.6 存档快照与回档

**单独一篇**：[`SAVE_SNAPSHOT.md`](./SAVE_SNAPSHOT.md)

KV 是整块覆盖，旧值一写就没了。快照提供三件事：

| 能力 | 接口 |
|---|---|
| 查看存档历史 | `POST /api/game/snapshot/list` |
| 看某一刻的存档内容 | `POST /api/game/snapshot/get` |
| 手动存一个还原点 | `POST /api/game/snapshot/create` |
| 回档到某个快照 | `POST /api/game/snapshot/restore` |
| 按时间点回档 | `POST /api/game/snapshot/restore-by-time` |
| 回档审计日志 | `POST /api/game/snapshot/restore-logs` |

关键设计：**回档本身也可回档** —— 回档前会自动把当前值存成
`before_restore` 快照，所以回错了还能撤回来。

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
| `POST /api/game/coin/voucher` | **发兑换券**（插件抓到奖品，不经过币） |
| `POST /api/game/coin/ledger` | 查币账本 |
| `POST /api/wallet/spend` | 插件扣积分（开局费），来源白名单只有 `game_spend` |

> ⚠️ `ledger` 的分页参数是 **`page_size`（下划线）**，和走 `entities.Paginator`
> 的那几个模块（`pageSize` 驼峰）**相反**。传错不报错，只会永远回 30 条 ——
> 和 §7.1 那个 `LIKE` 坑同一类：看起来能用。
>
> `voucher` 和 `redeem` 是两条路，别混：`redeem` 要**先有币**（币是入场券），
> `voucher` 是「抓到了就直接给券」。用「铸币 + 换券」凑会在账本上留一对
> `+n / -n`，而且中间崩一下就会留下**真实的币**（币能直接开局）。

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
| `mintGameVouchers(...)` | `POST /api/game/coin/voucher` |
| `spendPoints(...)` | `POST /api/wallet/spend` |

**关键差异**：前端 `spendGameCoin` 是本地事务，服务端是**行锁 + 版本号**。
并发安全性更强，但要注意**网络重试要带 `client_request_id`**（幂等键）。

> 客户端已经按这张表接完了（`kid-quest-farm-online` 的 `src/online/coin.ts`）。
> 三件在客户端必须**先挡掉**再发请求的事（服务端不知道本地那几条语义）：
> 数量 ≤ 0（服务端 `min=1` 会报参数错误）、买币单价 ≤ 0（服务端会**兜底 10**）、
> `voucherPerCoin = 0`（服务端读不到 manifest，会照换不误）。

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
| 10041001 | 存档快照不存在 |
| 10041101 | 快照数量超出配额 |
| 604 | 权限不足（写保留键 / 未拥有游戏） |

---

## 7. 已知问题与修复记录

### 7.0 事务内用外层 DB 句柄导致死锁（已修复，详见 SAVE_SNAPSHOT.md §8）

**现象**：回档测试挂死 600 秒超时。

**原因**：`Transaction(func(tx))` 里调用了用 `r.DB` 的仓库方法，
向连接池申请第二条连接，而第一条已被事务占用。

**修复**：加 `GetWithTx` / `PutWithTx`，事务内一律用 `tx`。
**测试防线**：测试库连接池固定为 1 条，让这类 bug 确定性失败。

---

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

---

### 7.2 幂等键拼成常量 ⇒ 第二次开局永远失败（已修复）

**现象**：第一次能玩，第二次开始一直返回「该笔记录已存在」（610）。
只在**不传 `client_request_id`** 的客户端上出现。

**原因**：`"game_coin_spend:" + req.ClientRequestID` 在客户端不传时
拼出来是 `"game_coin_spend:"` —— 一个**常量**。第二次调用撞上账本唯一索引，
被 `mapErr` 翻成 `DuplicateOperation`。

**修复**：加 `idemKey(prefix, reqID)`，`reqID == ""` 时返回**空串**
（`SetIdempotencyKey` 会把空串落成 `NULL`，唯一索引就不拦了）。
五处调用点（`buy` 两条、`spend`、`redeem` 两条）加 `MintVoucher` 一起改。

**教训**：`if key != ""` 这种守卫，写在**拼好之后**是错的 —— 拼出来的常量
永远非空。守卫要写在**拼之前**。

### 7.3 软删的尸体把 upsert 吞掉 ⇒ 写成功但谁也读不出来（已修复）

**现象**：卸载游戏（`/game/kv/delete`）→ 重装 → 插件第一次 `storage.set`
返回 `code: 200` 且 `version` 往上走，但紧接着 `get` / `list` 永远返回空。
客户端毫不知情，换台设备进度全丢。

**原因**：`BaseModel` 带 `gorm.DeletedAt`，`Delete` / `DeleteGame` 默认是**软删**
（只打一个 `deleted_at`）；而 `Get` / `List` 都会自动追加 `deleted_at IS NULL`。
重装后的第一笔写没有版本令牌（`expected_version = 0`）⇒ 走无锁 upsert
⇒ 命中那条软删的行 ⇒ `DoUpdates` 只改 `value` / `size` / `version`，
`deleted_at` 原样留着 ⇒ 这一行对所有读路径永久不可见。

**修复**（两处，缺一不可）：

- `Delete` / `DeleteGame` 改 `Unscoped()` **硬删** —— 「卸载」的语义就是数据没了；
  软删只会留下一条读不到、却仍占着唯一索引 `(uid, game_id, kv_key)` 的尸体。
- upsert 的 `DoUpdates` 加 `"deleted_at": nil` —— 历史数据 / 后台标记仍可能留下
  软删的行，只要 upsert 不清它，那一行就永远写不进去。

回归用例：`TestKvReviveAfterUninstall`、`TestKvUpsertRevivesSoftDeletedRow`
（后者变异验证过：去掉 `deleted_at: nil` 就红）。

**顺带修掉同一处的哑巴 bug**：`DoUpdates` 原来写的是
`"updated_at": gorm.Expr("updated_at")` —— 把列赋给它自己，等于**永远不刷新**。
upsert 走原生 clause、不经过 GORM 钩子，必须手写 `time.Now().Unix()`。

**教训**：「软删」和「upsert」放在一起，就是这个形状：
写成功、版本号在涨、读出来是空、没有任何报错。
凡是 upsert 命中软删过的行，都要问一句 `deleted_at` 谁来清。

### 7.4 新行落 `version: 0` ⇒ 前两次写没有冲突检测（已修复）

**现象**：客户端第一笔写拿到的 `version` 是 0，第二笔写仍然不带令牌。

**原因**：`Version` 是 `default:0`，GORM 会把结构体里的 0 原样写进库；
而无锁 upsert 的插入路径没有「从 1 起算」的约束。客户端把
`expected_version = 0` 当作「我手里没有令牌」的哨兵 ⇒ 缓存到 0 ⇒
下一次写**又走无锁路径** ⇒ 一个键的前两次写之间完全没有冲突检测，
两台设备互盖一句话都不报。

**修复**：插入前 `if kv.Version < 1 { kv.Version = 1 }`
（回档路径 `game_snapshot_srv` 本来就传 `toVersion = 0 + 1`，与它对齐）。
现在 `version: 0` 只有一个含义：**这一行不存在**。

回归用例：`TestKvVersionStartsAtOne`、`TestKvNoLockUpsertStillBumps`。

**教训**：一个字段同时表示「不存在」和「存在但值是 0」时，任何拿它当哨兵的
协议都会静默失效。哨兵值必须在写入侧就排除掉。
