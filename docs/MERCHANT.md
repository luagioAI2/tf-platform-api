# 招商（Merchant）API

> 上游 `kid-quest-farm-2d` 在 2026-10-29 加了「招商」这一屏，
> 并在类型注释里**明确把后端那一半留了出来**。本文说清那半是什么。

---

## 1. 上游把哪半留给后端

`src/domain/types.ts` 的原话：

> 两路人马共用一套申请单：
>   · `parent` —— 家长邀请商家入驻，**拿商家收益的分成**
>   · `merchant` —— 商家自己把东西挂进心愿商城
>
> ⚠️ 现在**没有后端**，所以这里只落「谁、什么时候、申请了什么」，
> 审核结果和后台地址都是**留给后端回填**的字段（见 `adminUrl`）。
> 别在客户端自己把状态推成 `approved` —— 那等于给所有用户
> 凭空发一个后台入口。

`src/features/redeem/MerchantView.tsx` 的文件头再强调一遍：

> · 客户端只做「提交」这一半：写一条 `status: 'pending'` 进…
> · `approved` + `adminUrl` **只能由后端回填**。界面上那两个分支是**真的**
> · …别加「模拟审核通过」这种按钮

**所以服务端要做的就是被明确留下的两件事：**

1. **审核**（`pending` → `approved` / `rejected`）
2. **回填后台地址**（`admin_url`）—— 只有审核通过才有值

---

## 2. 提交语义：同角色重复提交 = 改申请

客户端原话：

> 同一个角色重复提交 = 改申请，不是提第二份 —— **后端那边也要按这个语义**

所以唯一索引建在 **`(uid, role)`** 上，不是 `(uid, role, 提交次数)`。

### ⚠️ 改申请**不能**把审核结果冲掉

upsert 只更新 `name` / `contact` / `note`；
`status` / `admin_url` / `reviewed_at` / `reviewed_by` **一律不碰**。

**反例**（这是实现时最容易写错的一处）：

```
家长提交 → 审核通过 → 拿到后台地址
过几天回来改联系电话
  ↓ 如果 upsert 把 status 重置成 pending、admin_url 清空
他就**莫名其妙失去了后台入口**，而且不报错
```

**实测**：

```
已通过后再改申请（换电话）
  status   = approved        ← 仍是 approved ✓
  admin_url= https://...     ← 还在 ✓
  contact  = 13911112222     ← 已更新 ✓
```

---

## 3. 接口

### 3.1 客户端

| 接口 | 说明 |
|---|---|
| `POST /api/merchant/apply` | 提交 / 改申请（同角色幂等） |
| `POST /api/merchant/my` | 我的申请（含审核结果与后台地址） |
| `POST /api/merchant/invite-stats` | 家长侧的邀请统计 |

**`apply` 请求**

```json
{
  "role": "parent"
}
```

> ⚠️ **2026-10-29 晚改口**：上游客户端第二版把表单整个删了 ——
> 用户原话「点申请即可。不需要填 称呼 这些。**因为会根据当前账号，申请**。」
> 所以 `name` / `contact` 现在是**可选**的，不传就由服务端从账号取：
>
> | 字段 | 不传时取什么 |
> |---|---|
> | `name` | 昵称 → 用户名 → `家长<uid>` |
> | `contact` | 账号手机号 |
>
> **手机号只能服务端填**：登录响应里 `mobile` 是 `138****8000`（脱敏），
> 客户端手上没有明文。让客户端补，等于给审核的人一个假号码。
>
> 显式传了就用传的（商家可能想写店铺名），只补**缺的那一半**。

**`my` 响应**

```json
{
  "code": 200,
  "data": {
    "applications": [
      {
        "id": 2,
        "role": "parent",
        "status": "approved",
        "status_text": "已通过",
        "name": "小明妈妈",
        "contact": "13911112222",
        "note": "换了个电话",
        "admin_url": "https://merchant.example.com/dash?inviter=1",
        "created_at": 1793280763,
        "reviewed_at": 1793280763
      }
    ]
  }
}
```

> ⚠️ **`admin_url` 只在 `approved` 时返回** —— 服务端负责抑制，
> 不让客户端去判断「这个状态该不该渲染那个链接」。
> 客户端那边 `pending` 时那个区域写的是「审核通过后，这里会出现一个后台链接」。

### 3.2 后台（`X-Api-Token`）

| 接口 | 说明 |
|---|---|
| `POST /api/admin/merchant/list` | 申请列表（按 role / status / 关键词筛） |
| `POST /api/admin/merchant/review` | 审核 |
| `POST /api/admin/merchant/pending-count` | 待审核数（红点） |

**`review` 请求**

```json
{
  "application_id": 2,
  "approve": true,
  "admin_url": "https://merchant.example.com/dash?inviter=1",
  "note": "欢迎入驻"
}
```

**通过时 `admin_url` 必填**：

```
{"code":10100301,"error":"通过审核时必须填后台地址，否则对方拿不到入口"}
```

理由：通过了却不给地址的话，客户端那一屏会**永远**停在
「审核通过后，这里会出现后台链接」—— 用户看到「已通过」却拿不到入口，
只能来问客服。

**并发 / 重复审核**：状态 CAS，只允许从 `pending` 审。

```
{"code":10100201,"error":"该申请已经审核过了"}
```

不这样做的话，已通过的单子再点一次会刷新 `reviewed_at`
（审计上看不出谁什么时候审的），甚至被改成拒绝。

---

## 4. 数据模型

### `merchant_application`

| 字段 | 说明 |
|---|---|
| `uid` + `role` | **唯一索引** —— 同角色只有一条（改申请语义） |
| `status` | `pending` / `approved` / `rejected` |
| `name` | 家长昵称 / 店铺名 |
| `contact` | 手机号或微信 |
| `note` | 一句话介绍 |
| `admin_url` | **后台地址，只有审核通过才有值**，只能服务端写 |
| `reviewed_at` / `reviewed_by` / `review_note` | 审核审计 |

### ⚠️ `rejected` 是「服务端有、客户端暂不渲染」的已知不一致

客户端那边只有两档：

> 没有 `rejected` —— 被拒的单子现在不落地（后端接进来之后再加，
> 加了要同步改 `merchantStatusOf`）

但**后台运营总得能拒**，所以服务端保留了 `rejected`。
拒了之后客户端看到的是「还是 pending」。

**这是已知的不一致，不是 bug** —— 写在这里免得当成 bug 查。
客户端补上 `rejected` 那一档之后，两边就一致了。

---

## 5. 分成：先给邀请数，**故意不给金额**

客户端文案：「审核通过后，这里会出现一个后台链接，**能看邀请记录和分成**」。

`invite-stats` 返回：

```json
{
  "invited_total": 1,
  "invited_approved": 1,
  "share_amount": null,
  "share_note": "分成规则确定后开放"
}
```

**`share_amount` 故意留空。** 结算规则还没定，先造一个数字会让家长
以为「这就是我能拿到的钱」—— 而它只是一次除法。

等分成规则定下来（按什么比例、按什么口径算、怎么结算）再填这个字段。

---

## 6. 实测

```
① 客户端提交申请（parent）
   → {"status":"pending","status_text":"待审核"}，无 admin_url ✓

② 客户端查我的（pending，应无 admin_url）
   admin_url='' ✓                       ← 服务端抑制生效

③ 后台列表 + 待审核数
   pending=1，列表能拿到 id ✓             ← id 字段一开始漏了，补上了

④ 后台审核通过（带 admin_url）
   → {"status":"approved","admin_url":"https://..."} ✓

⑤ 客户端再查
   status=approved，admin_url=... ✓

⑥ ★ 已通过后再改申请（换电话）
   status 仍是 approved ✓
   admin_url 仍在 ✓
   contact 已更新 ✓

⑦ 通过时不给 admin_url
   → 10100301「通过审核时必须填后台地址」 ✓

⑧ 重复审核
   → 10100201「该申请已经审核过了」 ✓

⑨ 同角色重复提交
   我的申请 = 2 条（parent + merchant 各一条）✓
```

---

## 7. 错误码

| 码 | 含义 |
|---|---|
| 10100101 | 招商申请不存在 |
| 10100201 | 该申请已审核过 |
| 10100301 | 通过时必须给后台地址 |

> 招商用 `1010xxxx` —— `1009xxxx` 已被「后台/运维域」占用
> （`AdminTokenRequired` 等）。