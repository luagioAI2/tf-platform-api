# 模板下发（Task / Redeem Template）

> 一句话：App 首次启动的内容（每日任务、签到、长期任务、兑换清单）由后台配置，
> 官方一套、商家各一套；用户进来看到哪一套，由**他是被谁推来的**决定。

相关代码：

| 位置 | 干什么 |
| --- | --- |
| `entities/template.go` | `template` / `template_item` 蓝图 + 邀请码推导 |
| `entities/template_apply.go` | `template_apply`（「这个账号铺过没有」的锁） |
| `seed/templates.go` | 官方默认模板种子（16 条任务 + 15 条兑换品） |
| `service/template_srv.go` | 解析（选一个）/ 落地 / 绑邀请码 |
| `service/repository/template_apply.go` | 事务内铺实例 + 条目 → 实例的纯函数 |
| `api/template_api.go` | 4 个接口 |
| tf-admin | 后台配模板的页面（`view/mall/template/index.vue`） |

---

## 1. 两层的区别（这是整个设计的地基）

```
template / template_item      ← 蓝图：**没有 uid**，一份给所有人用
        │  /api/template/apply（每个账号只跑一次）
        ▼
task / redeem_item            ← 实例：**带 uid**，每账号一份
        │  App 的 pull
        ▼
本地 Dexie                    ← 显示端缓存，UI 一行都不用改
```

- 改模板**只影响以后新进来的账号**。已经装过、已经铺过实例的老用户不会变
  —— 这点必须说清楚，否则运营会以为「改了后台老用户立刻变」。
- 想让老用户也换：只能改他的 `task` / `redeem_item`（家长在 App 里改），
  或者删掉 `template_apply` 那一行 + 清空他的任务（**危险，别在生产上手做**）。

## 2. 解析口径：选一个，不叠加

```
用户有 source_merchant_uid
  ├─ 该商家有该 kind 的默认模板 → 用它
  └─ 没有（或没配）            → 回落官方默认
用户没有 source_merchant_uid     → 官方默认
```

**逐 kind 独立回落**：商家只配了任务模板、没配兑换模板时，
任务用商家的、兑换用官方的 —— 不是「一个开关切两套」。

为什么不是叠加：叠加会让「商家只想换掉三条任务」变成「十六条全在」，
而且谁覆盖谁说不清。

## 3. `/api/template/apply`：为什么在服务端铺

在线版的数据源是账号。客户端写本地 Dexie 的那几行服务端不知道，
下一次 `pull` 就被覆盖 —— 表现是「一进来有 16 条任务，过两秒全没了」，
而且**不报错**。

幂等靠 `template_apply.uid` 的唯一键（不是靠「任务表空不空」）：

```sql
CREATE UNIQUE INDEX uk_uid_tpl_apply ON template_apply(uid);
```

| 情形 | 结果 |
| --- | --- |
| 第一次 | 铺任务 + 铺兑换品，`applied=true` |
| 再来一次 | `applied=false`，`reason=already`，一条都不加 |
| 账号本来就有任务 | 任务一条不动，兑换表为空时照补，`reason=has-content` |
| 并发两次 | 第二次 INSERT 撞唯一键 ⇒ 当「已铺过」，不会变成 32 条 |

★ **判据必须是「铺过没有」，不能是「任务表空不空」**：家长把任务全删了，
表同样为空 —— 用「空」当判据，下次启动又把 16 条塞回去，孩子会以为
「删掉的东西自己长回来了」。

## 4. 邀请码

由商家 uid **确定性推导**，不落表：

```
TF<uid 的 36 进制>-<sha256("tf-invite-v1:<uid>") 前 4 位 hex>
例：uid=73 → TF21-9289（uid=255 → TF73-DC44）
```

`tf-platform-api` 与 `tf-admin` 各实现一份，**必须字节一致**（后台要把码显示给商家）。

`bind-invite` 三道闸，一道都不能少：

1. 码的校验位对（`ParseInviteCode`）；
2. 那个 uid 必须是 `merchant_application` 里**已通过**的商家；
3. 已经归属过的账号**不覆盖** —— 第一次算数。

## 5. 客户端顺序（错一步就永久错）

```
ensureGuest()  →  bind-invite（有码时）  →  apply  →  boot()（里面 pull）
```

- **先绑后铺**：铺模板时服务端看的是「这个账号属于哪个商家」。
  反过来铺出来的是官方模板，而 apply 只跑一次 —— 归属后来绑上了，
  模板**永远修不回来**（除非人工改库）。
- **先铺后 boot**：`boot()` 的第一次 `pull` 会把刚铺出来的实例带回本地。
  反过来这一屏是空的，要等下次启动才补上。
- **绑定遇到网络失败 ⇒ 整条不铺**，下次启动重来（`online/template.ts`）。
  宁可晚一轮，也不要先给一套错的。
- 游客账号**不清**暂存的邀请码：注册是**新建一个 uid**，那个号还得再绑一次，
  否则「先玩两把再注册」的推广用户会掉回官方模板。

## 6. 字段映射（Extra → 列）

模板条目里 typed 列是 snake_case，`extra` 里是 **App 原生的 camelCase**。
铺实例时必须拆回列上，否则 App 读不到：

| Extra 键 | `task` 列 |
| --- | --- |
| `allowOvertime` | `allow_overtime` |
| `allowLateNoPenalty` | `allow_late_no_penalty` |
| `qualityRated` | `quality_rated` |
| `checkInEnabled` | `check_in_enabled` |
| `checkInTargetCount` | `check_in_target_count` |
| `remindAt` | `remind_at` |
| `rewardItemIds` | `reward_item_ids` |
| `windowStartMinute` / `windowEndMinute` | 同名列（指针） |

剩下没列的（`checkInTierRatio` / `streakPerDay` / `streakCap`）原样留给
`task.extra_json` —— App 的 `parseTaskExtra` 会捡走。

★ 漏拆一个的症状是**静默的**：签到任务变成普通任务、奖励道具不掉、
提醒时间消失。都不报错。所以 `template_apply_test.go` 逐键断言。

★ 官方种子里「背诵」是 `checkInEnabled:false` + `checkInTargetCount:2`：
开关关着但目标次数要保留，不能当「没填」清成 0。

## 7. 探针

```bash
# 后端（8080）
cd tf-platform-api && ./tf-api.exe web
python scripts/check_template_apply.py      # 43 项：铺/幂等/并发/不覆盖/商家优先
python scripts/check_template.py            # 28 项：解析与邀请码

# App 端（8090 + 4401）
./tf-api.exe web -c ../_goprobe/config.8090.yaml
cd ../kid-quest-farm-online && npm run build && npx vite preview --port 4401 --strictPort
node scripts/_probe-template-online.mjs     # 真浏览器：新账号首启拿到 16 条（本地脚本，scripts/_* 不入库）
```

★ 两个探针里的 HTTP 都**绕开本机代理**（`ProxyHandler({})`）：
沙箱里 `http_proxy` 会把 `127.0.0.1` 也接管，症状是 `upstream connect failed`
或 `JSONDecodeError` —— 看着像「服务没起」。
