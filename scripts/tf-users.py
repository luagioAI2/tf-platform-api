#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""列出 tf.db 里的 App 用户（只读）。

用法：
    python scripts/tf-users.py                # 最近登录在前，最多 50 条
    python scripts/tf-users.py --limit 200
    python scripts/tf-users.py --q 138        # 按手机号 / 昵称 / uid 过滤
    python scripts/tf-users.py --db E:/plan/test25/tf-platform-api/data/tf.db

注意：这是「直接读库」，不是后台接口 —— tf-admin 目前没有用户列表页。
"""
import argparse
import os
import sqlite3
import sys
import time

DEFAULT_DB = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
    "data", "tf.db",
)


def fmt_ts(v):
    if not v:
        return "-"
    try:
        return time.strftime("%Y-%m-%d %H:%M", time.localtime(int(v)))
    except (ValueError, OSError):
        return str(v)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=DEFAULT_DB)
    ap.add_argument("--limit", type=int, default=50)
    ap.add_argument("--q", default="")
    args = ap.parse_args()

    if not os.path.exists(args.db):
        sys.exit("找不到库文件：%s" % args.db)

    conn = sqlite3.connect("file:%s?mode=ro" % args.db, uri=True)
    conn.row_factory = sqlite3.Row

    where = ["u.deleted_at IS NULL"]
    params = []
    if args.q:
        where.append("(u.mobile LIKE ? OR u.nickname LIKE ? OR CAST(u.id AS TEXT) = ?)")
        params += ["%" + args.q + "%", "%" + args.q + "%", args.q]

    sql = """
        SELECT u.id, u.mobile, u.nickname, u.status, u.is_guest, u.language,
               u.created_at, u.last_login_at,
               w.points, w.coupon, w.diamond,
               (SELECT COUNT(*) FROM user_game ug
                 WHERE ug.uid = u.id AND ug.deleted_at IS NULL) AS games,
               (SELECT COUNT(*) FROM game_wallet gw
                 WHERE gw.uid = u.id AND gw.deleted_at IS NULL) AS game_wallets,
               (SELECT COUNT(*) FROM farm_state f
                 WHERE f.uid = u.id AND f.deleted_at IS NULL) AS farms
          FROM user u
          LEFT JOIN wallet w ON w.uid = u.id AND w.deleted_at IS NULL
         WHERE %s
      ORDER BY COALESCE(u.last_login_at, u.created_at) DESC
         LIMIT ?
    """ % " AND ".join(where)
    params.append(args.limit)

    rows = conn.execute(sql, params).fetchall()
    total = conn.execute(
        "SELECT COUNT(*) FROM user WHERE deleted_at IS NULL").fetchone()[0]

    if not rows:
        print("没有匹配的用户。")
        return

    head = ("uid", "手机号", "昵称", "状态", "积分", "券", "钻石",
            "游戏", "币包", "农场", "注册", "最近登录")
    widths = [5, 14, 12, 4, 6, 5, 6, 4, 4, 4, 16, 16]

    def line(cells):
        out = []
        for i, c in enumerate(cells):
            s = str(c)
            # 中文按两格宽估算，避免表格错位
            w = sum(2 if ord(ch) > 127 else 1 for ch in s)
            out.append(s + " " * max(0, widths[i] - w))
        return "  ".join(out).rstrip()

    print(line(head))
    print("-" * 108)
    for r in rows:
        status = {0: "禁用", 1: "正常", 2: "封禁"}.get(r["status"], r["status"])
        if r["is_guest"]:
            status += "/游客"
        print(line([
            r["id"], r["mobile"] or "-", r["nickname"] or "-", status,
            r["points"] if r["points"] is not None else "-",
            r["coupon"] if r["coupon"] is not None else "-",
            r["diamond"] if r["diamond"] is not None else "-",
            r["games"], r["game_wallets"], r["farms"],
            fmt_ts(r["created_at"]), fmt_ts(r["last_login_at"]),
        ]))

    print("-" * 108)
    print("共 %d 个用户（未删除），本次显示 %d 条。库：%s" % (total, len(rows), args.db))


if __name__ == "__main__":
    main()
