# 存档快照与回档

> 解决「存档被写坏 / 误操作 / 需要回退进度」的问题。
> 配合 [`GAME_STORAGE_API.md`](./GAME_STORAGE_API.md) 使用。

---

## 1. 为什么需要这个

KV 存档是**整块覆盖**：一次 `set` 就把旧值冲掉了。线上化之后有三个现实问题：

| 场景 | 后果 |
|---|---|
| 孩子误操作（把农场推平了、把背包卖了） | 进度没了，无法恢复 |
| 游戏 bug 写坏存档 | 该用户永久卡死，只能删号 |
| 家长想把进度退回某个时间点 | 无历史可查 |

前端 Dexie 时代没这个问题 —— 数据在本地，「导出备份」就是全量快照。
改成服务端后，**必须自己留历史**。

---

## 2. 快照策略（不存全量历史）

一个活跃玩家一天可能写几百次存档。每次都存快照会让存储爆炸。

### 2.1 触发条件

```
没有历史快照              → 存（第一个必须有）
距上次快照 > 10 分钟      → 存
值变化 > 20%              → 存
其他                      → 跳过
```

> 变化幅度用**字节长度变化率**近似判断。因为 KV 里是任意 JSON，
> 服务端不可能理解语义，只能看「形状变了多少」。

### 2.2 保留策略

```
每个 (uid, game_id, key)：
  · 最近 20 个普通快照（超出淘汰最旧的）
  · 当天第一个快照永久保留（日切基线）
```

**日切基线**是兜底：即使当天疯狂写入，也一定能回到「今天开始时的状态」。

### 2.3 快照原因

| reason | 含义 |
|---|---|
| `auto` | 自动（时间/变化触发） |
| `daily` | 日切基线 |
| `manual` | 手动保存 |
| `before_restore` | **回档前**自动保存 |
| `restore` | 回档动作本身 |

`before_restore` 是关键设计：**回档本身也可回档**。

---

## 3. 接口

### 3.1 列出快照

`POST /api/game/snapshot/list` 🔒

```json
{ "game_id": "cloudfarm", "key": "save", "limit": 20 }
```

`key` 不传 = 列出该游戏所有 key 的快照。

**响应**

```json
{
  "code": 200,
  "data": {
    "count": 3,
    "list": [
      {
        "id": 3,
        "game_id": "cloudfarm",
        "key": "save",
        "size": 27,
        "version": 2,
        "reason": "auto",
        "is_daily_baseline": false,
        "note": "",
        "created_at": 1793183320
      },
      {
        "id": 2,
        "game_id": "cloudfarm",
        "key": "save",
        "size": 51,
        "version": 1,
        "reason": "manual",
        "is_daily_baseline": false,
        "note": "存档前",
        "created_at": 1793183320
      },
      {
        "id": 1,
        "game_id": "cloudfarm",
        "key": "save",
        "size": 51,
        "version": 0,
        "reason": "auto",
        "is_daily_baseline": true,
        "note": "",
        "created_at": 1793183320
      }
    ]
  }
}
```

### 3.2 查看快照内容

`POST /api/game/snapshot/get` 🔒

```json
{ "snapshot_id": 2 }
```

**响应**包含完整 `value`，可用于「预览要回档到什么状态」。

### 3.3 手动保存快照

`POST /api/game/snapshot/create` 🔒

```json
{ "game_id": "cloudfarm", "key": "save", "note": "打boss前" }
```

家长可以在「孩子要挑战难关之前」主动存一个点。

### 3.4 回档到指定快照

`POST /api/game/snapshot/restore` 🔒

```json
{ "snapshot_id": 2, "reason": "误操作恢复" }
```

**响应**

```json
{
  "code": 200,
  "data": { "restored": true, "game_id": "cloudfarm", "key": "save", "version": 2 }
}
```

### 3.5 按时间点回档

`POST /api/game/snapshot/restore-by-time` 🔒

**回档单个 key**

```json
{ "game_id": "cloudfarm", "key": "save", "at": 1793183000, "reason": "回到昨晚" }
```

**回档整个游戏**（`key` 省略）

```json
{ "game_id": "cloudfarm", "at": 1793183000, "reason": "整档回退" }
```

响应：`{ "restored_keys": 5 }`

> 实现：遍历该游戏所有有过快照的 key，各取 `at` 之前最近的快照写回。

### 3.6 回档日志

`POST /api/game/snapshot/restore-logs` 🔒

```json
{ "game_id": "cloudfarm", "limit": 20 }
```

**响应**

```json
{
  "code": 200,
  "data": {
    "count": 1,
    "list": [{
      "id": 1,
      "game_id": "cloudfarm",
      "key": "save",
      "snapshot_id": 2,
      "from_version": 1,
      "to_version": 2,
      "operator_id": 0,
      "reason": "误操作恢复",
      "created_at": 1793183338
    }]
  }
}
```

---

## 4. 回档的安全设计

回档是**高风险、不可逆**的操作。三道保险：

### 4.1 回档前自动保存

```
回档流程（同一事务内）：
  1. 读当前值
  2. 把当前值存为 before_restore 快照   ← 保险
  3. 写入目标快照的值
  4. 存一个 restore 快照（记录动作）
  5. 写审计日志
```

**效果**：即使回档到错误的时间点，也能从 `before_restore` 恢复回来。

实测验证：

```
存档 = A  →  存快照 A  →  改成 B
回档到 A  →  成功（此时自动存了 B 为 before_restore）
再从 before_restore 回档  →  成功回到 B  ✓
```

### 4.2 用户隔离

快照查询和回档都带 `uid` 条件。用户 A 无法访问或回档用户 B 的快照（实测返回错误）。

### 4.3 审计日志

每次回档都写 `game_kv_restore_log`：

| 字段 | 说明 |
|---|---|
| `snapshot_id` | 从哪个快照恢复 |
| `from_version` / `to_version` | 版本变化 |
| `operator_id` | 0 = 用户自己，>0 = 管理员 |
| `reason` | 回档原因 |

---

## 5. 数据模型

### 5.1 game_kv_snapshot

| 字段 | 类型 | 说明 |
|---|---|---|
| id | uint | 快照 ID |
| uid | uint | 用户（索引） |
| game_id | string | 游戏（索引） |
| kv_key | string | 键名（索引） |
| value | LONGTEXT | 快照内容 |
| size | int64 | 字节数 |
| version | int64 | 对应版本号 |
| reason | string | 快照原因 |
| is_daily_baseline | bool | 是否日切基线（永久保留） |
| note | string | 备注 |

索引：`(uid, game_id, kv_key, created_at)`

### 5.2 game_kv_restore_log

| 字段 | 类型 | 说明 |
|---|---|---|
| uid / game_id / kv_key | | 定位 |
| snapshot_id | uint | 恢复源 |
| from_version / to_version | int64 | 版本变化 |
| operator_id | uint | 操作者 |
| reason | string | 原因 |

---

## 6. 前端接入示例

```ts
// 家长端：查看存档历史
const { list } = await api('/api/game/snapshot/list', {
  game_id: 'cloudfarm',
  key: 'save',
})

// 「孩子说农场没了」 → 先看最近哪个快照是对的
const detail = await api('/api/game/snapshot/get', { snapshot_id: list[0].id })
console.log('那一刻的存档：', detail.value)

// 确认后回档
await api('/api/game/snapshot/restore', {
  snapshot_id: list[0].id,
  reason: '孩子误删农场',
})

// 回错了？从回档前自动保存的快照恢复
const after = await api('/api/game/snapshot/list', { game_id: 'cloudfarm', key: 'save' })
const beforeRestore = after.list.find(s => s.reason === 'before_restore')
if (beforeRestore) {
  await api('/api/game/snapshot/restore', { snapshot_id: beforeRestore.id, reason: '撤销回档' })
}
```

---

## 7. 存储估算

| 项 | 估算 |
|---|---|
| 单快照大小 | 约等于存档大小（几十 KB） |
| 每 key 保留 | 20 个普通 + 日切基线 |
| 单游戏（假设 3 个 key） | 约 60 个快照 |
| 21 款游戏全开 | 约 1260 个快照 |
| 单用户 | 约 20-50 MB |

> `SnapshotMaxPerUser = 2000` 是硬上限（常量已定义，可在清理逻辑中启用）。

---

## 8. 已知问题与修复记录

### 8.1 事务内用外层 DB 句柄导致死锁（已修复）

**现象**：回档测试挂死 600 秒超时。

**原因**：`Restore` 在 `s.DB.Transaction(func(tx *gorm.DB))` 里调用了
`s.KvRepo.Get(ctx, ...)` 和 `s.KvRepo.Put(ctx, ...)` —— 这两个方法内部用的是
`r.DB`（**外层句柄**），会向连接池申请**第二条连接**，而第一条已被事务占用。
连接池设小（或 SQLite 共享缓存）时直接死锁。

**修复**：加了 `GetWithTx` / `PutWithTx`，事务内一律用 `tx`。

**测试防线**：`newTestDB` 把连接池设为 **1 条** —— 这样任何「事务内取第二条连接」
的写法都会**确定性死锁**，不会侥幸跑过去。

> 这类 bug 在生产环境表现为「连接池被慢慢耗尽、接口开始超时」，
> 比直接死锁更难查。

### 8.2 upsert 不回写 version（已修复）

**现象**：写入成功，但返回的 `version` 一直是 0。

**原因**：upsert 的 `DoUpdates` 用的是 SQL 表达式 `version = version + 1`，
GORM 不会把计算结果回写到 Go 结构体。

**后果**：客户端拿到的 version 永远是 0 → 乐观锁 `expected_version`
永远匹配不上 → **多设备冲突检测形同虚设**；快照里记录的 version 也是错的。

**修复**：upsert 后重新读一次真实版本号。

### 8.3 每次写入起一个 goroutine（已修复）

**现象**：保留策略清理写成 `go s.enforceRetention(...)`。

**后果**：每次写入都起一个 goroutine，5W 并发下会堆积成千上万个。
违反 `AGENTS.md` 的「禁止无界 goroutine」红线。

**修复**：改成同步执行（只是一条带索引的 DELETE，开销可忽略）。