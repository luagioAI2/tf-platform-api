# 游戏存储现状分析

> 调研对象：`E:\plan\test25\kid-quest-farm-2d`
> 目的：在做服务端通用存储前，先搞清楚**现在到底是怎么存的**。

---

## 1. 游戏清单（21 款）

来源：`public/games/index.json`（20 款插件）+ `src/games/registry.ts`（1 款内置）。

| # | id | 名称 | 版本 | 形态 |
|---|---|---|---|---|
| 1 | prizepark | 抓奖乐园 | 1.6.2 | 插件 |
| 2 | claw3d | 抓娃娃 | 1.0.1 | 插件 |
| 3 | cloudfarm | 云边小农场 | 1.1.4 | 插件 |
| 4 | sugar | 甜点魔法学院 | 1.0.0 | 插件 |
| 5 | anthill | 蚂蚁搬家 | 1.0.0 | 插件 |
| 6 | moestrike | 少女战记 | 1.0.1 | 插件 |
| 7 | melonjar | 爆汁瓜瓜罐 | 1.0.0 | 插件 |
| 8 | minergirl | 挖矿少女 | 1.0.1 | 插件 |
| 9 | ropeland | 跳绳羊群 | 1.0.0 | 插件 |
| 10 | blockfit | 方块填空 | 1.0.0 | 插件 |
| 11 | deckrush | 一分钟卡组 | 1.0.0 | 插件 |
| 12 | corgi | 柯基屁屁 | 1.0.0 | 插件 |
| 13 | screamchick | 尖叫鸡 | 1.0.0 | 插件 |
| 14 | sweetspin | 旋转灯泡 | 1.0.0 | 插件 |
| 15 | renttower | 收租公寓 | 1.0.0 | 插件 |
| 16 | stackmeow | 堆呀堆 | 1.0.0 | 插件 |
| 17 | cellguard | 细胞守卫 | 1.0.0 | 插件 |
| 18 | redbutton | 别按红按钮 | 1.0.0 | 插件 |
| 19 | guardshift | 轮班守卫 | 1.0.0 | 插件 |
| 20 | hoopsneak | 偷偷投篮 | 1.0.0 | 插件 |
| 21 | doodlederby | 画马赛跑 | 1.0.0 | 插件 |

> 注：`registry.ts` 里只声明了 `cloudfarm` 一款内置游戏（`defaultOwned: true`），
> 其余 20 款都是 `public/games/` 下的插件包。

---

## 2. 核心结论：游戏存储只有**一套**通用机制

**所有 21 款游戏的存档，都走同一个接口 `host.storage`，落到同一张 Dexie 表 `gameKv`。**

这是本次调研最重要的发现 —— 不存在「每个游戏一套存储」的情况。

### 2.1 存储接口（`src/games/plugin.ts`）

```ts
storage: {
  get<T>(key: string): Promise<T | undefined>
  set(key: string, value: unknown): Promise<void>
  list(): Promise<Record<string, unknown>>
}
```

三个方法，**没有 delete、没有局部更新、没有查询**。整个存档是「整块读、整块写」。

### 2.2 落库（`src/db/db.ts`）

```ts
export interface GameKvRow {
  key: string    // `${gameId}:${游戏自己的键}`
  gameId: string // 冗余一列，用于「卸载游戏时一次删干净」
  value: unknown // 任意 JSON
}

export function gameKvKey(gameId: string, key: string): string {
  return `${gameId}:${key}`
}
```

**命名空间由宿主拼**：插件只给「自己的键」，`${gameId}:` 前缀由 `db.gameKvKey` 拼。
这样插件既不可能读到别的游戏的数据，也不可能因为自己拼错前缀而静默写到别处。

### 2.3 为什么是「一张通用表」而不是「每个游戏一张表」

Dexie 的 schema 是**编译期固定**的：

```ts
this.version(1).stores({ tasks: 'id, ...', ledger: 'id, ...' })
```

而游戏（尤其是**运行时下载**的）是运行期才出现的，**没法给自己建表**。
所以宿主提供一张通用表，用 `${gameId}:${key}` 做命名空间。

代价：没有索引查询，只能整块读写（小游戏够用）。
好处：`exportBackup` / `importBackup` / `wipeAll` **天然覆盖**它 —— 不用为每个新游戏改备份逻辑。

---

## 3. 各游戏实际用的 key（实测）

从 21 个 `.plugin.mjs` 里扫描 `storage.get/set` 调用：

| 游戏 | key | 说明 |
|---|---|---|
| prizepark | `save` | 整份存档（`var gy = "save"`） |
| cloudfarm | `settings` | 设置 + 状态混在一起 |
| claw3d | `prefs:` + 动态后缀 | `prefs:` 前缀 + 具体项名 |
| cellguard | `save` | 整份存档 |
| doodlederby | `save` | 整份存档 |
| guardshift | `save` | 整份存档 |
| hoopsneak | `save` | 整份存档 |
| redbutton | `save` | 整份存档 |
| anthill / sugar / blockfit / corgi / deckrush / melonjar / minergirl / moestrike / renttower / ropeland / screamchick / stackmeow / sweetspin | （动态 key 或封装在闭包里） | 静态扫描未捕获，但都通过 `host.storage` |

**观察**：
- 主流是**单 key 整档**（`save` / `settings`），值是一个大 JSON
- 少数用**多 key**（`claw3d` 的 `prefs:` 前缀）
- **key 名不统一**，服务端不能假设某个固定 key

---

## 4. 保留键（`__` 前缀）

`gameKv` 表同时装着两种东西：

| 前缀 | 归属 | 内容 |
|---|---|---|
| `__` | **宿主保留** | `__coin`（游戏币账本）、`__bundle`（插件代码）、`__assets`（资源根） |
| 其它 | 插件自己的 | `save` / `settings` / `prefs:xxx` … |

插件的 `storage.get/set` 碰到 `__` 开头**直接抛异常**（不静默忽略，因为那是插件的 bug）。
`storage.list()` 会把保留键**过滤掉**。

> 设计理由：不隔离的话，插件 `storage.set('__coin', [])` 就能清空自己的币账本，
> 绕过 `mintGameCoin` 的「只进不出」守卫。

---

## 5. 游戏币（`__coin`）

### 5.1 结构

```ts
export const GAME_COIN_KEY = '__coin'

export interface GameCoinEntry {
  delta: number   // 正数收入，负数消费
  memo: string
  at: number      // 时间戳
}
```

存在 `gameKv` 的 `__coin` 键里，值是 `GameCoinEntry[]`。

### 5.2 关键约束：余额是**算出来的**

```ts
export async function gameCoinBalanceOf(gameId: string): Promise<number> {
  const rows = await readGameCoinLedger(gameId)
  return rows.reduce((sum, r) => sum + r.delta, 0)
}
```

**不存 `balance` 字段** —— 理由和宿主三套币一字不差：存余额就会出现「余额和流水对不上」，而且不报错。

### 5.3 经济链（不可逆）

```
积分 ──buyGameCoin──▶ 游戏币 ──redeemGameCoin──▶ 兑换券
                         ▲
                         └──mintVoucher── 抓到奖品（直接发券，不经过币）
```

**三条不变量**：
1. 游戏币**不能**换回积分（否则「坐着玩」就能刷积分）
2. 兑换券**不能**换回积分
3. 一局期望产出 × 汇率 **必须 <** 一局成本（`expectedPayout × voucherPerCoin < playCost`）

### 5.4 事务边界

| 操作 | 涉及表 | 必须原子 |
|---|---|---|
| `spendGameCoin` | `gameKv` | 守卫（余额够不够）+ 扣款 |
| `buyGameCoins` | `ledger` + `gameKv` | 扣积分 + 记币 |
| `redeemGameCoins` | `gameKv` + `harvestLedger` | 烧币 + 发券 |
| `mintGameVouchers` | `harvestLedger` | 只发券（不扣币） |

**为什么必须原子**：拆开就会出现「积分扣了、币没到」/「币扣了、券没发」，而且**不报错**。

---

## 6. 宿主业务数据（非游戏）

除了游戏的 `gameKv`，宿主自己还有这些表：

| 表 | 主键 | 索引 | 用途 |
|---|---|---|---|
| `tasks` | id | cycle, category, archived, createdAt | 任务定义 |
| `taskInstances` | id | taskId, periodKey, date, status, `&[taskId+periodKey+date]` | 任务实例（每周期一条） |
| `ledger` | id | source, createdAt, refId | 积分账本 |
| `harvestLedger` | id | source, createdAt, refId | 兑换券账本 |
| `mallLedger` | id | source, createdAt, refId | 钻石账本 |
| `inventory` | itemId | — | 背包（道具 → 数量） |
| `animals` | id | animalId, bornAt | 动物 |
| `checkIns` | id | taskId, date, periodKey, `&[taskId+date]` | 签到记录 |
| `checkInProgress` | id | taskId, periodKey, `&[taskId+periodKey]` | 签到进度 |
| `redeemItems` | id | category, archived, cost, createdAt | 兑换商品 |
| `redeemRecords` | id | itemId, createdAt, fulfilled | 兑换记录 |
| `farmEvents` | id | kind, createdAt, refId | 农场事件日志 |
| `meta` | key | — | 通用键值（设置等） |

**注意**：`tasks` / `taskInstances` / `inventory` / `animals` / `redeemItems` / `redeemRecords`
是**宿主业务数据**，不是游戏数据。用户说「背包和动物是默认游戏的东西」—— 实际上它们在
Dexie 里是**独立的表**（不是 `gameKv`），是农场这个内置游戏专用的。

---

## 7. 对服务端设计的启示

### 7.1 游戏存档：直接用 KV 模型，不要试图结构化

因为：
- 21 款游戏 key 名不统一（`save` / `settings` / `prefs:xxx`）
- 值是**任意 JSON**，服务端无法也不应该理解它的结构
- 游戏会持续新增，服务端不能每加一款就改 schema

**服务端应该提供**：

```
PUT  /api/game/{gameId}/kv/{key}     # 整块写入
GET  /api/game/{gameId}/kv/{key}     # 整块读取
GET  /api/game/{gameId}/kv           # 列出全部（过滤保留键）
DELETE /api/game/{gameId}/kv/{key}   # 删除单键
DELETE /api/game/{gameId}/kv         # 卸载游戏：删全部
```

**必须保留的语义**：
- 命名空间隔离：`(uid, gameId, key)` 三元组唯一
- 保留键 `__` 前缀：客户端不能读写（服务端校验）
- 整块覆盖（不做局部更新，与前端语义一致）

### 7.2 游戏币：独立表，不要塞进 KV

虽然前端把 `__coin` 塞在 `gameKv` 里，**服务端不应该照抄**，因为：

| 维度 | 前端 gameKv | 服务端应该 |
|---|---|---|
| 并发 | 单用户单设备，Dexie 事务够用 | 5W 并发，需要行锁 |
| 查询 | 只读自己 | 可能要做排行榜/统计 |
| 对账 | 无 | 需要 `balance_after` 快照 |
| 幂等 | 不需要 | 需要（网络重试） |

所以服务端：**`game_coin_ledger` 独立表**，字段对齐宿主三套币的账本设计
（`uid, game_id, delta, balance_after, memo, idempotency_key`）。

### 7.3 任务系统：需要真正的关系表

任务不能用 KV，因为：
- 有**状态机**（pending → submitted → completed/rejected/expired）
- 有**周期**（daily/weekly/monthly/yearly）
- 有**唯一约束**（同 taskId + periodKey + date 只能一条实例）
- 需要按状态、日期、周期查询（家长端「待审核」列表）

### 7.4 背包/动物：属于「内置游戏」的业务数据

用户说「背包和动物是默认游戏的东西」，但它们在 Dexie 里是独立表而非 `gameKv`。
两种服务端方案：

**方案 A：也用 KV**（把农场当普通游戏）
- 简单，但农场的背包/动物需要跨设备一致、需要防作弊，KV 做不到

**方案 B：独立表**（推荐）
- `user_inventory`、`user_animal` 独立表
- 农场变成「有服务端专属支持的内置游戏」

> 建议 B。农场是默认游戏、用户量最大，值得专门建模。

---

## 8. 迁移风险清单

| 风险 | 说明 | 缓解 |
|---|---|---|
| 存档体积 | 单游戏存档可能几百 KB（`__bundle` 甚至几 MB） | KV 值用 `LONGTEXT`/`MEDIUMBLOB`，加体积上限 |
| 写放大 | 整块覆盖，每次存档写全量 | 加 `updated_at`，客户端只在脏时提交 |
| 冲突 | 多设备同时玩同一游戏 | 加 `version` 字段做乐观锁 |
| 保留键越权 | 客户端试图写 `__coin` | 服务端硬校验，返回 403 |
| 游戏卸载 | 删数据 vs 保留存档 | 提供软删（`deleted_at`），30 天后真删 |
