#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""从 router.go + api/*.go 生成 docs/API_INDEX.md（接口总表）。

为什么生成而不是手写：手写表一定会漂。路由加了、改了、删了，
手写表还在那儿，而且看起来很像真的。这个脚本每次跑都对着源码重算。

    python scripts/gen-api-index.py            # 写文件
    python scripts/gen-api-index.py --check    # 只比对，不写；有差异退出码 1

判据：`--check` 退出码 0 = 文档与源码一致。

⚠️ 路由注册里有**跨行**写法（`send-sms-code` 的 handler 在下一行），
所以按「括号配平」取整条语句，不能按行切 —— 按行切会静默漏掉一条，
而且总数看起来仍然像个正常数字。
"""
import argparse
import io
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
API_DIR = ROOT
ROUTER = os.path.join(API_DIR, "internal", "app", "router", "router.go")
API_PKG = os.path.join(API_DIR, "internal", "app", "api")
OUT = os.path.join(API_DIR, "docs", "API_INDEX.md")

GROUP_TITLES = {
    "ops": "运维端点（不挂业务中间件）",
    "": "公开",
    "/api": "公开（无鉴权）",
    "/api/auth": "认证",
    "/api/user": "用户资料 / 家长设置",
    "/api/wallet": "钱包（积分 / 券 / 钻石）",
    "/api/game": "游戏商城 / 分发",
    "/api/game/kv": "游戏存档（通用 KV）",
    "/api/game/snapshot": "存档快照与回档",
    "/api/game/coin": "游戏内货币",
    "/api/task": "任务系统",
    "/api/system": "系统信息",
    "/api/farm": "农场状态",
    "/api/redeem": "兑换商城",
    "/api/wish": "心愿商城",
    "/api/merchant": "招商",
    "/api/admin": "后台（需后台令牌）",
}


def read(path):
    with io.open(path, "r", encoding="utf-8") as f:
        return f.read()


def balanced(text, start):
    """text[start] == '('，返回配平后的下一个位置。跳过字符串字面量。"""
    depth = 0
    i = start
    quote = None
    while i < len(text):
        ch = text[i]
        if quote:
            if ch == "\\":
                i += 2
                continue
            if ch == quote:
                quote = None
        elif ch in "\"'`":
            quote = ch
        elif ch == "(":
            depth += 1
        elif ch == ")":
            depth -= 1
            if depth == 0:
                return i + 1
        i += 1
    return -1


def collect_summaries():
    """(接收者, 方法) -> @Summary 文案。"""
    out = {}
    for name in sorted(os.listdir(API_PKG)):
        if not name.endswith(".go") or name.endswith("_test.go"):
            continue
        lines = read(os.path.join(API_PKG, name)).splitlines()
        for i, ln in enumerate(lines):
            m = re.match(r"func \(c \*(\w+)\) (\w+)\(", ln)
            if not m:
                continue
            recv, fn = m.group(1), m.group(2)
            summary = ""
            for j in range(i - 1, max(-1, i - 40), -1):
                s = lines[j].strip()
                if s.startswith("func "):
                    break
                mm = re.match(r"//\s*@Summary\s+(.+)$", s)
                if mm:
                    summary = mm.group(1).strip()
                    break
            out[(recv, fn)] = summary
    return out


def parse_router():
    """返回 [(group_key, method, path, auth, recv, fn)]，保持源码顺序。"""
    text = read(ROUTER)

    # ---- 分组定义 ----
    # r := app.Group("/api") 的前缀本身就是 /api，别再拼一次
    groups = {"app": ("", "否"), "r": ("/api", "否")}
    for m in re.finditer(r'(\w+)\s*:=\s*(?:r|app)\.Group\("([^"]*)"\)', text):
        var, prefix = m.group(1), m.group(2)
        nl = text.find("\n", m.end())
        tail = text[m.end(): nl if nl != -1 else len(text)]
        auth = "否"
        if "AdminMiddleware" in tail:
            auth = "后台令牌"
        elif "JWTMiddleware" in tail:
            auth = "登录"
        groups[var] = (prefix if var == "r" else "/api" + prefix, auth)

    # ---- 两个注册函数的作用域 ----
    def block_end(start):
        # 找不到下一个 func（这个函数是文件里最后一个）⇒ 到文件尾。
        # 忘了这一步会让整块被跳过，而总数仍然是个正常数字（3 条）。
        if start == -1:
            return -1
        nxt = text.find("\nfunc ", start + 1)
        return len(text) if nxt == -1 else nxt

    ops_start = text.find("func (a *Router) registerOps")
    ops_end = block_end(ops_start)
    api_start = text.find("func (a *Router) registerAPI")
    api_end = block_end(api_start)

    def region(off):
        if ops_start != -1 and ops_start <= off < ops_end:
            return "ops"
        if api_start != -1 and api_start <= off < api_end:
            return "api"
        return None

    rows = []
    for m in re.finditer(r'(?:(\w+)\.)?(GET|POST)\(', text):
        where = region(m.start())
        if where is None:
            continue
        var, method = m.group(1), m.group(2)
        open_paren = m.end() - 1
        close = balanced(text, open_paren)
        if close == -1:
            sys.exit("括号配平失败，位置 %d" % open_paren)
        stmt = text[open_paren:close]

        pm = re.match(r'\(\s*"([^"]*)"', stmt)
        if not pm:
            sys.exit("取不到路径：%r" % stmt[:80])
        path = pm.group(1)

        hm = re.findall(r"a\.(\w+API)\.(\w+)", stmt)
        if hm:
            recv, fn = hm[-1]
        else:
            # 非 API 类型的 handler（/metrics）
            fm = re.findall(r"(\w+)\.(\w+)\(", stmt)
            recv, fn = fm[-1] if fm else ("?", "?")

        prefix, gauth = groups.get(var, ("", "否"))
        auth = gauth
        if "AdminMiddleware" in stmt:
            auth = "后台令牌"
        elif "OptionalJWTMiddleware" in stmt:
            auth = "可选"
        elif "JWTMiddleware" in stmt:
            auth = "登录"

        key = "ops" if where == "ops" else prefix
        rows.append((key, method, prefix + path, auth, recv, fn))
    return rows


def build():
    summaries = collect_summaries()
    rows = parse_router()

    order, buckets = [], {}
    for key, method, path, auth, recv, fn in rows:
        if key not in buckets:
            buckets[key] = []
            order.append(key)
        buckets[key].append((method, path, auth, recv, fn))

    buf = []
    w = buf.append
    w("# Kid Quest Farm API 接口总表")
    w("")
    w("> **本文件由 `scripts/gen-api-index.py` 从 `internal/app/router/router.go` 生成，不要手改。**")
    w("> 改了路由就重跑：`python scripts/gen-api-index.py`；想校验有没有漂：`python scripts/gen-api-index.py --check`。")
    w(">")
    w("> 逐字段的请求/响应示例见 `docs/API.md`（只覆盖认证 / 用户 / 钱包 / 游戏商城四块），")
    w("> 各专题见 `GAME_STORAGE_API.md` / `SAVE_SNAPSHOT.md` / `TASK_SYSTEM.md` / `MERCHANT.md` / `MALL_AND_DISTRIBUTION.md`。")
    w("")
    w("## 0. 三条全局约定")
    w("")
    w("| 项 | 说明 |")
    w("|---|---|")
    w("| 方法 | **一律 `POST` + JSON**，只有健康检查、指标和游戏目录是 `GET` |")
    w("| 成功判据 | `body.code === 200`。**业务失败也是 HTTP 200**，只看 `res.ok` 会误判成功 |")
    w("| 鉴权 | `Authorization: Bearer <access_token>`；`登录` = 缺 token 直接 401 |")
    w("")
    w("`鉴权` 列：`否` 公开 · `可选` 带 token 就认、不带也能用（`owned` 全 false） · "
      "`登录` 必须登录 · `后台令牌` 走 `AdminMiddleware`（和 App 的 JWT 不是一套）")
    w("")
    w("## 1. 接口总表")
    w("")
    total = 0
    for key in order:
        items = buckets[key]
        total += len(items)
        title = GROUP_TITLES.get(key, key or "其他")
        w("### %s `%s`（%d 个）" % (title, key, len(items)))
        w("")
        w("| 方法 | 路径 | 鉴权 | 作用 |")
        w("|---|---|---|---|")
        for method, path, auth, recv, fn in items:
            s = summaries.get((recv, fn)) or "`%s.%s`" % (recv, fn)
            w("| `%s` | `%s` | %s | %s |" % (method, path, auth, s))
        w("")

    w("## 2. 统计")
    w("")
    w("| 项 | 数 |")
    w("|---|---|")
    w("| 路由总数 | **%d** |" % total)
    w("| GET | %d |" % sum(1 for r in rows if r[1] == "GET"))
    w("| POST | %d |" % sum(1 for r in rows if r[1] == "POST"))
    w("| 需登录 | %d |" % sum(1 for r in rows if r[3] == "登录"))
    w("| 后台令牌 | %d |" % sum(1 for r in rows if r[3] == "后台令牌"))
    w("| 公开 / 可选 | %d |" % sum(1 for r in rows if r[3] in ("否", "可选")))
    w("")
    w("## 3. 用户列表在后台怎么看")
    w("")
    w("**结论：后台目前没有用户列表页，也没有 `user/list` 接口。**")
    w("")
    w("`tf-admin` 是 GVA 2.9.2 分支，和 `tf-platform-api` 共用 `data/tf.db`，")
    w("但它只定制了两个业务页：**游戏版本发布**（`/api/admin/game/release/*`）和")
    w("**招商审核**（`/api/admin/merchant/*`）。菜单里那个「用户管理」是 GVA 自带的")
    w("`sys_users` 表 —— 那是**后台管理员账号**，不是 App 的家长 / 孩子。")
    w("")
    w("`/api/admin/*` 下现有全部接口：")
    w("")
    w("| 路径 | 作用 |")
    w("|---|---|")
    for key, method, path, auth, recv, fn in rows:
        if key == "/api/admin":
            w("| `%s` | %s |" % (path, summaries.get((recv, fn)) or fn))
    w("")
    w("### 现在就能看：直接读库（只读，不动数据）")
    w("")
    w("```bash")
    w("python scripts/tf-users.py                 # 最近登录在前，带积分 / 券 / 游戏数 / 农场数")
    w("python scripts/tf-users.py --q 138         # 按手机号 / 昵称 / uid 过滤")
    w("python scripts/tf-users.py --limit 200     # 多看点")
    w("```")
    w("")
    w("读 `user` + `wallet` + `user_game` + `game_wallet` + `farm_state`，")
    w("`deleted_at IS NULL` 过滤软删；连接用 `mode=ro`，不会写坏库。")
    w("")
    w("> ⚠️ 服务在跑的时候也能读（SQLite 允许多读者），但**别在写入高峰做全表统计**。")
    w("")
    w("### 想要一个后台页面，要补三样")
    w("")
    w("| 层 | 要加的东西 |")
    w("|---|---|")
    w("| 接口 | `POST /api/admin/user/list`（分页 + 手机号/昵称模糊 + 状态过滤），挂在 `admin` 组 |")
    w("| 服务 | `UserAdminService.List`：一次 join 出 `user` + `wallet`，别在循环里查余额 |")
    w("| 前端 | `tf-admin/web/src/view/mall/` 旁边加 `user/`，菜单走 GVA 的 `sys_base_menus` |")
    w("")
    w("⚠️ 这个接口**不能**放在 `/api/user` 下 —— 那个组是 App 的 JWT，")
    w("家长账号之间能互相看见。必须走 `AdminMiddleware`。")
    w("")
    return "\n".join(buf) + "\n", total


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--check", action="store_true")
    args = ap.parse_args()

    content, total = build()

    if args.check:
        old = read(OUT) if os.path.exists(OUT) else ""
        if old == content:
            print("OK 文档与源码一致（%d 条路由）" % total)
            return 0
        print("DRIFT 文档与源码不一致，重跑不带 --check 即可更新")
        return 1

    with io.open(OUT, "w", encoding="utf-8", newline="\n") as f:
        f.write(content)
    print("写入 %s（%d 条路由）" % (os.path.relpath(OUT, ROOT), total))
    return 0


if __name__ == "__main__":
    sys.exit(main())
