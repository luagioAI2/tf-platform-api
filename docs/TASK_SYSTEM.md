# 任务系统 API

> 对齐 `kid-quest-farm-2d` 的 `src/domain/` 领域逻辑，提供线上化后端。

---

## 1. 概念模型

```
Task（任务定义）          家长创建：「完成语文作业，30 分钟，10 分」
    │
    │ 按周期自动生成
    ▼
TaskInstance（任务实例）   每天一条：「今天的语文作业」
    │
    │ 状态机
    ▼
pending → submitted → completed / failed
   ↓          ↓
expired    rejected（打回重做）
   ↓
skipped
```

### 1.1 周期

| cycle | periodKey 示例 | 说明 |
|---|---|---|
| `once` | `once` | 一次性，不随时间变化 |
| `daily` | `2026-10-28` | 每天一条 |
| `weekly` | `2026-W44` | 每周一条 |
| `monthly` | `2026-10` | 每月一条 |
| `yearly` | `2026` | 每年一条 |

### 1.2 状态机（严格）

```
pending    → submitted | expired | skipped
submitted  → completed | failed | rejected
rejected   → submitted | skipped
completed / failed / expired / skipped → 终态
```

**非法跃迁返回 `10050301 任务状态不允许该操作`**。

---

## 2. 结算规则（核心）

这是整个 App 的「心脏」—— 不是「做完就给满分」，而是「什么时候做完，决定拿多少分」。

| 情形 | 结果 |
|---|---|
| 实际用时 ≤ 计划时长 | **全额** |
| 计划时长 < 实际用时 < 计划时长×2 | **按比例线性衰减** |
| 实际用时 ≥ 计划时长×2 | **0 分** |
| 勾了「超期不扣分」 | 超时多久都**全额** |
| 勾了「严格限时」 | 只要超时**立刻 0 分** |
| 质量达到门槛 | 在基础分之上**额外加成** |

### 衰减公式

```
得分 = round(基础分 × (2 - 实际用时 ÷ 计划用时))
```

**保底**：只要还没到归零阈值，至少保留 **1 分**（避免「差几秒就颗粒无收」挫伤积极性）。

### 质量评级

| 评级 | 系数 | 说明 |
|---|---|---|
| `awful` | 0.0 | 很差 |
| `poor` | 0.5 | 一般 |
| `ok` | 1.0 | 良好（中间档） |
| `good` | 1.1 | 很好 |
| `great` | 1.2 | 特别棒 |

> 质量评级仅在任务开启 `quality_rated` 时生效。

### 实测验证

| 场景 | 计划 | 实际 | 基础分 | 结果 |
|---|---|---|---|---|
| 按时 | 30 | 30 | 10 | **10** |
| 提前 | 30 | 10 | 10 | **10** |
| 超时 50% | 30 | 45 | 10 | **5** |
| 超时 2 倍 | 30 | 60 | 10 | **0** |
| 严格限时超时 | 30 | 31 | 10 | **0** |
| 免扣分超时 | 30 | 300 | 10 | **10** |
| great 评级 | 30 | 20 | 10 | **12** |
| awful 评级 | 30 | 20 | 10 | **0** |

---

## 3. 接口

### 3.1 创建任务

`POST /api/task/create` 🔒

```json
{
  "title": "完成语文作业",
  "note": "字迹工整",
  "category": "study",
  "cycle": "daily",
  "planned_minutes": 30,
  "base_points": 10,
  "allow_overtime": false,
  "allow_late_no_penalty": false,
  "quality_rated": false,
  "check_in_enabled": false,
  "check_in_target_count": 0,
  "remind_at": "18:00",
  "sort": 0
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| title | 是 | 任务标题（≤64 字） |
| category | 是 | `study` / `chore` / `sport` / `art` / `reading` / `habit` |
| cycle | 是 | `once` / `daily` / `weekly` / `monthly` / `yearly` |
| planned_minutes | 是 | 计划时长（1-1440） |
| base_points | 是 | 基础积分（1-10000） |
| allow_overtime | 否 | 允许超时结算（默认 false = 严格限时） |
| allow_late_no_penalty | 否 | 免扣分（默认 false） |
| quality_rated | 否 | 启用质量评级 |
| check_in_enabled | 否 | 启用签到模式 |
| check_in_target_count | 否 | 签到周期目标次数（默认 7） |
| window_start_minute / window_end_minute | 否 | 允许完成的时间段（分钟数，0-1439） |

**副作用**：立即生成当前周期的实例。

---

### 3.2 今日任务板

`POST /api/task/board` 🔒

```json
{}
```

**响应**

```json
{
  "code": 200,
  "data": {
    "date": "2026-10-28",
    "total": 2,
    "completed": 1,
    "pending": 1,
    "submitted": 0,
    "today_points": 10,
    "balance": 65,
    "instances": [
      {
        "id": 1,
        "task_id": 1,
        "period_key": "2026-10-28",
        "date": "2026-10-28",
        "title": "完成语文作业",
        "category": "study",
        "cycle": "daily",
        "planned_minutes": 30,
        "base_points": 10,
        "status": "completed",
        "actual_minutes": 20,
        "earned_points": 10,
        "quality": "",
        "started_at": 1793179474,
        "paused_at": null,
        "paused_total_ms": 1287,
        "submitted_at": 1793179480,
        "settled_at": 1793179482,
        "completed_at": 1793179482
      }
    ]
  }
}
```

> **懒生成**：调用此接口会自动补齐今天所有任务的实例，不依赖定时任务。
> 即使服务停机几天，用户一进来就能看到正确的实例。

---

### 3.3 计时（开始 / 暂停 / 恢复）

```
POST /api/task/start-timer   {"instance_id": 1}
POST /api/task/pause-timer   {"instance_id": 1}
POST /api/task/resume-timer  {"instance_id": 1}
```

**为什么需要暂停**：孩子做作业中途去吃饭 / 被叫走，这段不该算进用时，否则会被判超时白扣分。

**计时真相源**：`started_at`，暂停只是从里面扣掉一段时间：

```
实际用时 = now - started_at - paused_total_ms - (暂停中 ? now - paused_at : 0)
```

---

### 3.4 提交任务

`POST /api/task/submit` 🔒

```json
{
  "instance_id": 1,
  "actual_minutes": 20,
  "backfilled": false
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| instance_id | 是 | 实例 ID |
| actual_minutes | 否 | 实际用时。不传则按计时器算 |
| backfilled | 否 | 是否当日补记 |

提交后进入 `submitted` 状态，等家长审核。

---

### 3.5 家长审核

`POST /api/task/review` 🔒

```json
{
  "instance_id": 1,
  "approve": true,
  "quality": "great",
  "note": "写得很好"
}
```

| 字段 | 说明 |
|---|---|
| approve | `true` 通过（结算发分）/ `false` 打回 |
| quality | 质量评级（任务开启 `quality_rated` 时有效） |
| note | 审核备注 |

**通过**：按结算规则计算得分，写入 `earned_points`，并发积分。
**打回**：进入 `rejected` 状态，孩子可以重做。

---

### 3.6 待审核列表

`POST /api/task/pending-review` 🔒

```json
{ "page": 1, "page_size": 20 }
```

---

### 3.7 查询实例

`POST /api/task/instances` 🔒

```json
{
  "page": 1,
  "pageSize": 30,
  "task_id": 0,
  "date_from": "2026-10-01",
  "date_to": "2026-10-31",
  "status": ["completed", "pending"]
}
```

---

### 3.8 任务统计

`POST /api/task/stats` 🔒

```json
{ "date_from": "2026-10-01", "date_to": "2026-10-31" }
```

**响应**

```json
{
  "code": 200,
  "data": {
    "date_from": "2026-10-01",
    "date_to": "2026-10-31",
    "total": 25,
    "by_status": { "completed": 20, "expired": 3, "pending": 2 },
    "earned_points": 180,
    "ledger_points": 180
  }
}
```

---

### 3.9 跳过任务

`POST /api/task/skip` 🔒

```json
{ "instance_id": 1 }
```

用于「主动放弃」—— 免扣分任务不扣分。

---

## 4. 签到

### 4.1 签到

`POST /api/task/check-in` 🔒

```json
{ "task_id": 2 }
```

**响应**

```json
{
  "code": 200,
  "data": {
    "task_id": 2,
    "date": "2026-10-28",
    "continuous_days": 1,
    "points": 5,
    "need_review": false,
    "claimed_tiers": []
  }
}
```

**阶梯奖励**：每完成 `check_in_target_count` 天，额外给 `base_points × 0.8 × target` 分。

**防重复**：唯一索引 `(uid, task_id, date)` + 服务端校验，重复签到返回 `10050401`。

---

### 4.2 签到状态

`POST /api/task/check-in-status` 🔒

```json
{ "task_id": 2 }
```

**响应**

```json
{
  "code": 200,
  "data": {
    "task_id": 2,
    "title": "每日练字",
    "period_key": "2026-10-28",
    "checked_days": ["2026-10-28"],
    "pending_days": [],
    "target_count": 7,
    "continuous_days": 1,
    "claimed_tiers": [],
    "today_checked": true
  }
}
```

---

## 5. 数据模型

### 5.1 Task

| 字段 | 类型 | 说明 |
|---|---|---|
| id | uint | 任务 ID |
| uid | uint | 所属用户 |
| title | string | 标题 |
| category | string | 分类 |
| cycle | string | 周期 |
| planned_minutes | int | 计划时长 |
| base_points | int | 基础积分 |
| allow_overtime | bool | 允许超时结算 |
| allow_late_no_penalty | bool | 免扣分 |
| quality_rated | bool | 启用质量评级 |
| check_in_enabled | bool | 签到模式 |
| check_in_target_count | int | 签到目标次数 |
| archived | bool | 已归档 |

### 5.2 TaskInstance

| 字段 | 类型 | 说明 |
|---|---|---|
| id | uint | 实例 ID |
| task_id | uint | 所属任务 |
| period_key | string | 周期键 |
| date | string | 计划日期 |
| status | string | 状态 |
| actual_minutes | int? | 实际用时 |
| earned_points | int? | 获得积分 |
| quality | string | 质量评级 |
| started_at | int64? | 计时开始 |
| paused_at | int64? | 暂停时刻 |
| paused_total_ms | int64 | 累计暂停毫秒 |

**唯一约束**：`(uid, task_id, period_key, date)`

> 这个约束的设计有历史原因：早期用 `(taskId, periodKey)` 唯一索引，
> 导致周期任务（如「一月读 4 本书」）第二次提交必然撞唯一索引、抛异常。
> 加上 `date` 后：日任务仍然一天一条，周期任务跨天可以记满 N 次。

---

## 6. 错误码

| 码 | 含义 |
|---|---|
| 10050101 | 任务不存在 |
| 10050201 | 任务实例不存在 |
| 10050301 | 任务状态不允许该操作 |
| 10050401 | 今天已经签到过了 |
| 10050501 | 该任务不是签到任务 |
| 10050601 | 计时还没开始 |
| 10050701 | 该任务正在等待审核 |

---

## 7. 前端接入示例

```ts
// 1. 今日任务板
const board = await api<TaskBoard>('/api/task/board', {})

// 2. 开始做任务
await api('/api/task/start-timer', { instance_id: 1 })

// 3. 中途暂停（去吃饭）
await api('/api/task/pause-timer', { instance_id: 1 })
await api('/api/task/resume-timer', { instance_id: 1 })

// 4. 做完了
await api('/api/task/submit', { instance_id: 1 })

// 5. 家长审核（家长端）
await api('/api/task/review', {
  instance_id: 1,
  approve: true,
  quality: 'great',
  note: '写得很好',
})
// → 孩子获得 12 分（10 × 1.2）

// 6. 签到
await api('/api/task/check-in', { task_id: 2 })
```

---

## 8. 与 kid-quest-farm 的差异

| 项 | kid-quest-farm | 服务端 |
|---|---|---|
| 存储 | Dexie（IndexedDB） | MySQL |
| 实例生成 | 前端 refresh 时生成 | **懒生成**（访问任务板时补齐） |
| 状态跃迁 | 前端控制 | **服务端 CAS 校验**（防并发重复结算） |
| 结算 | 前端计算 | **服务端计算**（防作弊） |
| 发积分 | 前端写账本 | **服务端事务**（幂等键防重复） |
| 签到审核 | 有 `pendingDays` 机制 | 保留字段，当前直接发分（可扩展） |
| 周期任务唯一约束 | `&[taskId+periodKey+date]` | 同 |
