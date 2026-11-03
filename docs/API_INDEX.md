# Kid Quest Farm API 接口总表

> **本文件由 `scripts/gen-api-index.py` 从 `internal/app/router/router.go` 生成，不要手改。**
> 改了路由就重跑：`python scripts/gen-api-index.py`；想校验有没有漂：`python scripts/gen-api-index.py --check`。
>
> 逐字段的请求/响应示例见 `docs/API.md`（只覆盖认证 / 用户 / 钱包 / 游戏商城四块），
> 各专题见 `GAME_STORAGE_API.md` / `SAVE_SNAPSHOT.md` / `TASK_SYSTEM.md` / `MERCHANT.md` / `MALL_AND_DISTRIBUTION.md`。

## 0. 三条全局约定

| 项 | 说明 |
|---|---|
| 方法 | **一律 `POST` + JSON**，只有健康检查、指标和游戏目录是 `GET` |
| 成功判据 | `body.code === 200`。**业务失败也是 HTTP 200**，只看 `res.ok` 会误判成功 |
| 鉴权 | `Authorization: Bearer <access_token>`；`登录` = 缺 token 直接 401 |

`鉴权` 列：`否` 公开 · `可选` 带 token 就认、不带也能用（`owned` 全 false） · `登录` 必须登录 · `后台令牌` 走 `AdminMiddleware`（和 App 的 JWT 不是一套）

## 1. 接口总表

### 运维端点（不挂业务中间件） `ops`（3 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `GET` | `/healthz` | 否 | 存活探针 |
| `GET` | `/health` | 否 | 就绪探针 |
| `GET` | `/metrics` | 否 | `metrics.Handler` |

### 公开（无鉴权） `/api`（3 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `POST` | `/api/ping` | 否 | Ping |
| `GET` | `/api/game/catalog` | 否 | 游戏目录（catalog.json，Ed25519 已签名） |
| `GET` | `/api/game/catalog.sig` | 否 | 游戏目录签名（单行 base64） |

### 认证 `/api/auth`（10 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `POST` | `/api/auth/send-sms-code` | 否 | 发送短信验证码 |
| `POST` | `/api/auth/register` | 否 | 手机号注册 |
| `POST` | `/api/auth/login` | 否 | 手机号 + 密码登录 |
| `POST` | `/api/auth/sms-login` | 否 | 手机号 + 验证码登录（未注册自动注册） |
| `POST` | `/api/auth/wechat-login` | 否 | 微信登录（未绑定则自动注册） |
| `POST` | `/api/auth/refresh` | 否 | 刷新 access token |
| `POST` | `/api/auth/reset-password` | 否 | 重置密码（验证码） |
| `POST` | `/api/auth/logout` | 登录 | 登出 |
| `POST` | `/api/auth/bind-mobile` | 登录 | 绑定手机号（微信用户） |
| `POST` | `/api/auth/change-password` | 登录 | 修改密码（需旧密码） |

### 用户资料 / 家长设置 `/api/user`（4 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `POST` | `/api/user/profile` | 登录 | 获取用户资料 |
| `POST` | `/api/user/settings/get` | 登录 | 读家长设置（整份 JSON） |
| `POST` | `/api/user/settings/save` | 登录 | 保存家长设置（整份覆盖） |
| `POST` | `/api/user/update-profile` | 登录 | 更新用户资料 |

### 钱包（积分 / 券 / 钻石） `/api/wallet`（3 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `POST` | `/api/wallet/info` | 登录 | 获取钱包余额（三套币） |
| `POST` | `/api/wallet/ledger` | 登录 | 查询账本流水 |
| `POST` | `/api/wallet/spend` | 登录 | 扣积分 |

### 游戏商城 / 分发 `/api/game`（6 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `POST` | `/api/game/list` | 可选 | 游戏列表 |
| `POST` | `/api/game/detail` | 可选 | 游戏详情 |
| `POST` | `/api/game/purchase` | 登录 | 购买游戏 |
| `POST` | `/api/game/my-games` | 登录 | 我拥有的游戏 |
| `POST` | `/api/game/update-check` | 登录 | 检查更新 |
| `POST` | `/api/game/download` | 登录 | 获取游戏包下载地址（校验是否已拥有） |

### 游戏存档（通用 KV） `/api/game/kv`（5 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `POST` | `/api/game/kv/get` | 登录 | 读取单个键 |
| `POST` | `/api/game/kv/set` | 登录 | 整块写入 |
| `POST` | `/api/game/kv/list` | 登录 | 列出该游戏全部存档 |
| `POST` | `/api/game/kv/delete` | 登录 | 删除单个键或整个游戏存档 |
| `POST` | `/api/game/kv/usage` | 登录 | 查询存储用量 |

### 存档快照与回档 `/api/game/snapshot`（6 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `POST` | `/api/game/snapshot/list` | 登录 | 列出存档快照 |
| `POST` | `/api/game/snapshot/get` | 登录 | 查看快照内容 |
| `POST` | `/api/game/snapshot/create` | 登录 | 手动保存快照 |
| `POST` | `/api/game/snapshot/restore` | 登录 | 回档到指定快照 |
| `POST` | `/api/game/snapshot/restore-by-time` | 登录 | 按时间点回档（恢复到该时刻之前最近的快照） |
| `POST` | `/api/game/snapshot/restore-logs` | 登录 | 回档操作日志 |

### 游戏内货币 `/api/game/coin`（8 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `POST` | `/api/game/coin/info` | 登录 | 查询游戏币余额 |
| `POST` | `/api/game/coin/list` | 登录 | 查询所有游戏币余额 |
| `POST` | `/api/game/coin/buy` | 登录 | 用积分买游戏币 |
| `POST` | `/api/game/coin/spend` | 登录 | 消费游戏币（玩一局） |
| `POST` | `/api/game/coin/mint` | 登录 | 铸币（插件调用，只进不出） |
| `POST` | `/api/game/coin/redeem` | 登录 | 游戏币换兑换券 |
| `POST` | `/api/game/coin/voucher` | 登录 | 发兑换券（插件奖品，不经过游戏币） |
| `POST` | `/api/game/coin/ledger` | 登录 | 查询游戏币账本 |

### 任务系统 `/api/task`（16 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `POST` | `/api/task/create` | 登录 | 创建任务 |
| `POST` | `/api/task/update` | 登录 | 更新任务 |
| `POST` | `/api/task/delete` | 登录 | 删除任务 |
| `POST` | `/api/task/list` | 登录 | 任务定义列表 |
| `POST` | `/api/task/board` | 登录 | 今日任务板（孩子端首页） |
| `POST` | `/api/task/instances` | 登录 | 查询任务实例列表 |
| `POST` | `/api/task/start-timer` | 登录 | 开始计时 |
| `POST` | `/api/task/pause-timer` | 登录 | 暂停计时 |
| `POST` | `/api/task/resume-timer` | 登录 | 恢复计时 |
| `POST` | `/api/task/submit` | 登录 | 提交任务（孩子点"做完了"） |
| `POST` | `/api/task/review` | 登录 | 家长审核（通过/打回） |
| `POST` | `/api/task/skip` | 登录 | 跳过任务 |
| `POST` | `/api/task/pending-review` | 登录 | 待审核列表（家长端） |
| `POST` | `/api/task/stats` | 登录 | 任务统计（家长看板） |
| `POST` | `/api/task/check-in` | 登录 | 签到 |
| `POST` | `/api/task/check-in-status` | 登录 | 签到状态 |

### 系统信息 `/api/system`（1 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `POST` | `/api/system/info` | 登录 | 运行时信息 |

### 农场状态 `/api/farm`（2 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `POST` | `/api/farm/state/get` | 登录 | 读取农场状态 |
| `POST` | `/api/farm/state/save` | 登录 | 保存农场状态 |

### 兑换商城 `/api/redeem`（10 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `POST` | `/api/redeem/item/create` | 登录 | 创建兑换商品（家长） |
| `POST` | `/api/redeem/item/update` | 登录 | 更新兑换商品（家长） |
| `POST` | `/api/redeem/item/delete` | 登录 | 删除兑换商品（家长） |
| `POST` | `/api/redeem/list` | 登录 | 可兑换商品列表（家里的兑换品 + 已上架的心愿商品，混排） |
| `POST` | `/api/redeem/redeem` | 登录 | 兑换（券优先 + 积分补差） |
| `POST` | `/api/redeem/history` | 登录 | 兑换记录：家里换的 + 心愿换的，合成一条时间线 |
| `POST` | `/api/redeem/records` | 登录 | 兑换记录 |
| `POST` | `/api/redeem/fulfill` | 登录 | 标记已兑现（家长） |
| `POST` | `/api/redeem/cancel` | 登录 | 取消兑换并退款（家长） |
| `POST` | `/api/redeem/stats` | 登录 | 兑换统计（家长看板） |

### 心愿商城 `/api/wish`（12 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `POST` | `/api/wish/list` | 登录 | 心愿商品列表 |
| `POST` | `/api/wish/detail` | 登录 | 心愿商品详情（家长端含外链） |
| `POST` | `/api/wish/set-offer` | 登录 | 家长定价（0 = 关闭该商品） |
| `POST` | `/api/wish/wish` | 登录 | 想要这个（不扣券） |
| `POST` | `/api/wish/redeem` | 登录 | 确定兑换（扣券，数字商品当场交付） |
| `POST` | `/api/wish/confirm` | 登录 | 家长确认 |
| `POST` | `/api/wish/done` | 登录 | 家长收尾（已买到 / 已收到） |
| `POST` | `/api/wish/cancel` | 登录 | 取消心愿（退券；数字商品同时收回游戏） |
| `POST` | `/api/wish/orders` | 登录 | 心愿单列表 |
| `POST` | `/api/wish/order-detail` | 登录 | 心愿单详情 |
| `POST` | `/api/wish/pending` | 登录 | 家长待办（wished 与 committed 分开计数） |
| `POST` | `/api/wish/redeem-code` | 登录 | 用兑换码解锁 |

### 招商 `/api/merchant`（3 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `POST` | `/api/merchant/apply` | 登录 | 提交招商申请（同角色重复提交 = 改申请） |
| `POST` | `/api/merchant/my` | 登录 | 我的招商申请（含审核结果与后台地址） |
| `POST` | `/api/merchant/invite-stats` | 登录 | 邀请统计（家长侧） |

### 后台（需后台令牌） `/api/admin`（7 个）

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| `POST` | `/api/admin/game/release/publish` | 后台令牌 | 发布游戏版本（管理端） |
| `POST` | `/api/admin/game/release/list` | 后台令牌 | 版本列表（管理端） |
| `POST` | `/api/admin/game/release/delete` | 后台令牌 | 删除版本（管理端） |
| `POST` | `/api/admin/game/release/self-check` | 后台令牌 | 自检 Ed25519 密钥配置（返回公钥，用于粘到前端） |
| `POST` | `/api/admin/merchant/list` | 后台令牌 | 招商申请列表（后台） |
| `POST` | `/api/admin/merchant/review` | 后台令牌 | 审核招商申请（后台；通过时必须填后台地址） |
| `POST` | `/api/admin/merchant/pending-count` | 后台令牌 | 待审核数（后台红点） |

## 2. 统计

| 项 | 数 |
|---|---|
| 路由总数 | **99** |
| GET | 5 |
| POST | 94 |
| 需登录 | 77 |
| 后台令牌 | 7 |
| 公开 / 可选 | 15 |

## 3. 用户列表在后台怎么看

**结论：在 `tf-admin` 里，菜单「商城运营 → App 用户」。**

先纠正一个容易搞错的点：**家长和孩子不是两种账号。** `user` 表里一人一行，
家长只是在这行上多设了一个 4 位密码（`user_settings.payload` 里的 `parentPin`）
用来进家长区。所以后台也没有「家长列表」—— 只有一张用户表，
最多多一列「有没有设家长锁」。

和 GVA 自带的「用户管理」（`sys_users`，**后台管理员账号**）是两套东西，
别改错：改那边等于把自己的管理员踢掉。

| 功能 | 接口 | 说明 |
|---|---|---|
| 列表 / 搜索 | `POST /mall/user/list` | 手机号 / 昵称片段、状态、分页；一次补齐钱包余额 |
| 详情 | `GET /mall/user/detail` | 单个用户 + 余额 + 有没有设家长锁 |
| 账本 | `GET /mall/user/ledger` | 分页流水，看得清「钱怎么来的」 |
| 调余额 | `POST /mall/user/adjust` | 必须填备注，服务端写一条 `manual_adjust` 流水 |

⚠️ 这几件事在 **`tf-admin`**，不在 `tf-platform-api`。用户列表也不能放在
`/api/user` 下 —— 那是 App 自己的 JWT，用户之间能互相看见；
必须走后台的 `AdminMiddleware`。

### 已经初始化过的库要补菜单和权限

种子（`source/system/{menu,api,casbin}.go`）**只在首次 `initdb` 生效**。
现成的库直接跑：

```bash
cd ../tf-admin
python scripts/seed-user-menu.py --dry-run   # 先看要写哪几行
python scripts/seed-user-menu.py            # 真写（幂等，重复跑不重复插）
```

补完**重新登录**：菜单和权限是登录时读进前端的，刷新不够。

### 不想开后台：直接读库（只读，不动数据）

```bash
python scripts/tf-users.py                 # 最近登录在前，带积分 / 券 / 游戏数 / 农场数
python scripts/tf-users.py --q 138         # 按手机号 / 昵称 / uid 过滤
python scripts/tf-users.py --limit 200     # 多看点
```

读 `user` + `wallet` + `user_game` + `game_wallet` + `farm_state`，
`deleted_at IS NULL` 过滤软删；连接用 `mode=ro`，不会写坏库。

> ⚠️ 服务在跑的时候也能读（SQLite 允许多读者），但**别在写入高峰做全表统计**。

### 附：`/api/admin/*` 现有全部接口

这块只放「后台管理员令牌」才能调的，和上面的 `mall/user` 不是一组：

| 路径 | 作用 |
|---|---|
| `/api/admin/game/release/publish` | 发布游戏版本（管理端） |
| `/api/admin/game/release/list` | 版本列表（管理端） |
| `/api/admin/game/release/delete` | 删除版本（管理端） |
| `/api/admin/game/release/self-check` | 自检 Ed25519 密钥配置（返回公钥，用于粘到前端） |
| `/api/admin/merchant/list` | 招商申请列表（后台） |
| `/api/admin/merchant/review` | 审核招商申请（后台；通过时必须填后台地址） |
| `/api/admin/merchant/pending-count` | 待审核数（后台红点） |

