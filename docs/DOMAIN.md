# 领域模型（DOMAIN）

> **这是 AI 写业务代码时最需要的文件。**
> 每次新增业务前，先在这里把实体、字段、状态机、权限写清楚，再让 AI 生成代码。
> 本文件是模板，请按新项目实际业务替换下面的「示例」部分。

---

## 1. 实体清单

| 实体 | 表名 | 说明 | 状态 |
|---|---|---|---|
| User | `user` | 用户 | 待建 |
| Order | `order` | 订单（示例） | 待建 |

> 新增实体时，同步在 `internal/app/service/repository/repo.go` 的 `Models()` 里注册。

---

## 2. 实体定义

### 2.1 User（示例，按需替换）

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| id | uint | PK, 自增 | 用户 ID |
| username | string(32) | unique, not null | 用户名 |
| password | string(64) | not null | bcrypt 哈希 |
| nickname | string(32) | | 昵称 |
| mobile | string(20) | index | 手机号 |
| email | string(64) | index | 邮箱 |
| status | uint8 | default 1 | 0=禁用 1=正常 |
| language | string(8) | default 'en' | 语言偏好 |

### 2.2 Order（示例，按需替换）

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| id | uint | PK | 订单 ID |
| order_no | string(32) | unique | 业务单号 |
| uid | uint | index, not null | 下单用户 |
| amount | decimal(20,4) | not null | 金额 |
| status | uint8 | index, default 0 | 见状态机 |
| remark | string(255) | | 备注 |

---

## 3. 状态机（AI 生成代码时必须严格实现）

### Order.status

```
0 draft（草稿）
    │ submit
    ▼
1 submitted（已提交）
    │ approve            │ reject
    ▼                    ▼
2 approved（已通过）   3 rejected（已驳回）
    │ close
    ▼
4 closed（已关闭）
```

**合法跃迁表**

| 当前 | 允许的下一状态 | 触发动作 |
|---|---|---|
| 0 draft | 1 submitted | submit |
| 1 submitted | 2 approved / 3 rejected | approve / reject |
| 2 approved | 4 closed | close |
| 3 rejected | 4 closed | close |
| 4 closed | — | 终态 |

**实现要求**
- 非法跃迁必须返回 `errors.WithCode(errors.OrderStatusNotAllowed)`
- 跃迁必须在一个事务内完成，并写状态变更日志
- 跃迁成功后 `EventSrv.Publish(OrderStatusChangedEvent, order)`

---

## 4. 权限矩阵

| 操作 | 游客 | 登录用户 | 管理员 |
|---|---|---|---|
| 查看自己的订单 | ✗ | ✓ | ✓ |
| 查看他人订单 | ✗ | ✗ | ✓ |
| 创建订单 | ✗ | ✓ | ✓ |
| 审核订单 | ✗ | ✗ | ✓ |
| 删除订单 | ✗ | ✗ | ✓ |

**实现要求**
- 越权返回 `errors.WithCode(errors.PermissionsForbidden)`
- 管理员接口必须挂 `middleware.AdminMiddleware()`

---

## 5. 业务规则（示例）

1. **金额精度**：一律 `decimal(20,4)`，计算用 `decimal.Decimal`，禁止 float64
2. **幂等**：创建订单以 `order_no` 唯一索引兜底，重复插入返回 `errors.DuplicateOperation`
3. **并发**：订单状态变更需加分布式锁 `order:{id}`
4. **软删除**：`BaseModel.DeletedAt`，查询自动过滤
5. **时间**：统一 Unix 秒（int64），展示时按 `ServiceSettings.Timezone` 转换

---

## 6. 术语表

| 术语 | 英文 | 含义 |
|---|---|---|
| 用户 | User | 系统使用者 |
| 订单 | Order | 业务单据（示例） |
| 管理员 | Admin | 后台运营人员，走 AdminMiddleware |

> 新增业务术语时在这里登记，避免 AI 用不同的词指代同一个概念。
