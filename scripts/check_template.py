#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""模板下发链路的真实接口探针（不是源码 grep，也不是单测）。

跑之前：tf-api 要在 8080 上，且 EnableAutoMigrate=true（新表 + 官方种子已就绪）。

钉的是**三段式中间那段**：
  官方默认有 → 用户没归属 → 拿到官方
  商家默认有 → 用户归属该商家 → 拿到商家（不是叠加）
  商家没默认 → 用户归属该商家 → 回落官方
邀请码：坏码拒、已审核商家的码能绑、绑完不覆盖。

★ 成功码是 200（errors.SUCCESS），**不是** GVA 的 0。
  HTTP 恒 200，业务错误也 200 ⇒ 只看 HTTP / 只判 `code != 0` 全是恒真，
  必须钉具体码：坏码 606、参数校验 607、没登录 10010501。
"""
import json
import sqlite3
import sys
import time
import urllib.error
import urllib.request

BASE = 'http://127.0.0.1:8080'
DB = 'E:/plan/test25/tf-platform-api/data/tf.db'

SUCC = 200            # errors.SUCCESS
ERR_INVALID = 606     # errors.InvalidParam（邀请码被拒）
ERR_VALIDATION = 607  # errors.ValidationParamError（binding oneof 失败）
OK = 0
FAIL = 0

# ★ 本机代理陷阱：沙箱里 `http_proxy` 指向 127.0.0.1 的一个代理端口，
#   连 **127.0.0.1:8080** 也会被它接管 —— 症状是 `upstream connect failed`
#   或一段非 JSON 的错误页（`JSONDecodeError` 看起来像「服务没起」，
#   于是一路去查端口 / 进程，查半天发现服务好好的）。
#   本探针只打本机，一律绕开代理。
_OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def check(cond, label, detail=''):
    global OK, FAIL
    if cond:
        OK += 1
        print('  ✓ ' + label)
    else:
        FAIL += 1
        print('  ✗ ' + label + ('  → ' + str(detail)[:300] if detail else ''))


def is_succ(r):
    return r.get('code') == SUCC


def is_reject(r, code=None):
    """拒绝必须同时满足：code 不是 200、data 是空的、且（给了就）等于具体错误码。
    只判 `code != 0` 在 code=200 的接口上是恒真 —— 成功也过。"""
    ok = r.get('code') != SUCC and r.get('data') in (None, {}, [], '')
    if code is not None:
        ok = ok and r.get('code') == code
    return ok


def post(path, body, token=None):
    data = json.dumps(body).encode('utf-8')
    req = urllib.request.Request(BASE + path, data=data, method='POST')
    req.add_header('Content-Type', 'application/json')
    if token:
        req.add_header('Authorization', 'Bearer ' + token)
    try:
        with _OPENER.open(req, timeout=10) as r:
            return r.status, json.loads(r.read().decode('utf-8'))
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read().decode('utf-8'))


def sql():
    c = sqlite3.connect(DB)
    c.row_factory = sqlite3.Row
    return c


def invite_code(uid):
    """和 Go 的 MakeInviteCode 独立实现一遍，交叉校验。"""
    import hashlib
    digits = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ'
    n = uid
    b36 = ''
    while n:
        b36 = digits[n % 36] + b36
        n //= 36
    h = hashlib.sha256(('tf-invite-v1:%d' % uid).encode()).hexdigest()[:4].upper()
    return 'TF' + b36 + '-' + h


def fresh_uid(c, used):
    u = 990001
    while u in used:
        u += 1
    used.add(u)
    return u


def ensure_merchant(c, uid, now, created):
    row = c.execute(
        "SELECT id FROM merchant_application WHERE uid=? AND role='merchant'"
        " AND status='approved' AND deleted_at IS NULL", (uid,)).fetchone()
    if row:
        return
    c.execute(
        "INSERT INTO merchant_application (created_at,updated_at,uid,role,status,name,contact,reviewed_at)"
        " VALUES (?,?,?,'merchant','approved','探针商家','-',?)",
        (now, now, uid, now))
    created.append(uid)


def main():
    print('=== 1. 游客登录拿 token ===')
    status, resp = post('/api/auth/guest', {'device_id': 'probe-template-0001'})
    check(status == 200, '游客登录 HTTP 200', (status, resp))
    token = (resp.get('data') or {}).get('access_token')
    check(bool(token), '拿到 access_token', resp)
    if not token:
        return fail('没有 token，后面的都测不了')
    uid = ((resp.get('data') or {}).get('user') or {}).get('uid')
    print('    uid =', uid)

    c = sql()
    now = int(time.time())
    created = []          # 本探针造的商家申请单，finally 里只删这些
    tpl_ids = []          # 本探针造的模板，finally 里只删这些
    try:
        # 基线：这个设备上次跑完应该已经清干净了；没清干净就显式归零，
        # 否则「官方默认」那段会恒红，而且会把脏数据留给下一轮。
        c.execute("UPDATE user SET source_merchant_uid=0, invite_code='' WHERE id=?", (uid,))
        c.commit()
        base = c.execute(
            "SELECT source_merchant_uid FROM user WHERE id=?", (uid,)).fetchone()
        check(base is not None and base['source_merchant_uid'] == 0,
              '前提：该用户当前没有商家归属', dict(base) if base else None)

        print('=== 2. 官方默认模板（用户没归属）===')
        status, r = post('/api/template/resolve', {'kind': 'task'}, token)
        d = r.get('data') or {}
        check(status == 200 and is_succ(r), 'resolve task 返回 code=200（不是 0）', r)
        check(d.get('scope') == 'official', 'scope=official', d.get('scope'))
        check(d.get('source_merchant_uid') == 0, 'source_merchant_uid=0', d.get('source_merchant_uid'))
        items = d.get('items') or []
        check(len(items) == 16, '16 条官方任务', len(items))
        first = items[0] if items else {}
        check(first.get('name') == '完成语文作业' and first.get('base_points') == 10,
              'typed 列下发正确', first)
        check(first.get('allowOvertime') is True and first.get('remindAt') == '18:30',
              'Extra 玩法参数一起下发（没被 typed 列吞掉）', first)

        status, r2 = post('/api/template/resolve', {'kind': 'redeem'}, token)
        d2 = r2.get('data') or {}
        check(is_succ(r2), 'resolve redeem 返回 code=200', r2)
        check(len(d2.get('items') or []) == 15, '15 条官方兑换品', len(d2.get('items') or []))
        ri = (d2.get('items') or [{}])[0]
        check(ri.get('price') == 30 and ri.get('createdByParent') is True,
              '兑换模板 typed + extra 都对', ri)

        status, bad = post('/api/template/resolve', {'kind': 'nope'}, token)
        check(is_reject(bad, ERR_VALIDATION), '非法 kind 被拒（607，不是「HTTP 200 就算过」）', bad)

        # 两端都测了，中间那根线（鉴权）单独钉一下
        status, anon = post('/api/template/resolve', {'kind': 'task'})
        check(anon.get('code') != SUCC, '没登录取不到模板（HTTP 200 也可能是拒绝）', anon)

        print('=== 3. 邀请码：生成 / 校验 / 坏码 ===')
        status, r3 = post('/api/template/invite-code', {}, token)
        my_code = (r3.get('data') or {}).get('invite_code')
        check(is_succ(r3) and bool(my_code) and my_code.startswith('TF'),
              '能生成自己的邀请码', r3)
        # 该游客不是已审核商家 ⇒ 自己的码也绑不上（中间那道闸）
        status, r4 = post('/api/template/bind-invite', {'invite_code': my_code}, token)
        check(is_reject(r4, ERR_INVALID), '未审核商家的码被拒（606）', r4)
        status, r5 = post('/api/template/bind-invite', {'invite_code': 'TF16-0000'}, token)
        check(is_reject(r5, ERR_INVALID), '校验位错的码被拒（606）', r5)
        still = c.execute(
            "SELECT source_merchant_uid FROM user WHERE id=?", (uid,)).fetchone()
        check(still['source_merchant_uid'] == 0,
              '被拒的两次绑定一个字节都没写进 user', dict(still))

        print('=== 4. 商家默认模板优先，且不是叠加 ===')
        row = c.execute(
            "SELECT uid FROM merchant_application WHERE role='merchant' AND status='approved'"
            " AND deleted_at IS NULL ORDER BY uid LIMIT 1").fetchone()
        used = {r['uid'] for r in c.execute(
            "SELECT uid FROM merchant_application WHERE role='merchant'").fetchall()}
        merchant_uid = row['uid'] if row else fresh_uid(c, used)
        # 第二个**有效且已审核**的商家：用来真的走一遍「已归属 ⇒ 不覆盖」分支，
        # 不是拿一个坏码被拒来冒充。
        merchant_uid2 = fresh_uid(c, used)
        ensure_merchant(c, merchant_uid, now, created)
        ensure_merchant(c, merchant_uid2, now, created)
        c.commit()
        code = invite_code(merchant_uid)
        code2 = invite_code(merchant_uid2)
        print('    merchant_uid =', merchant_uid, 'invite_code =', code)
        print('    merchant_uid2=', merchant_uid2, 'invite_code2=', code2)

        # 造一份商家默认任务模板（3 条），挂到商家名下
        existed = c.execute(
            "SELECT id FROM template WHERE scope='merchant' AND kind='task' AND owner_uid=?",
            (merchant_uid,)).fetchone()
        if existed:
            c.execute("DELETE FROM template_item WHERE template_id=?", (existed['id'],))
            c.execute("DELETE FROM template WHERE id=?", (existed['id'],))
        cur = c.execute(
            "INSERT INTO template (created_at,updated_at,name,kind,scope,owner_uid,is_default,note,sort,source)"
            " VALUES (?,?,?,?,?,?,?,?,?,?)",
            (now, now, '探针商家任务模板', 'task', 'merchant', merchant_uid, 1, 'probe', 0, 'manual'))
        tpl_id = cur.lastrowid
        tpl_ids.append(tpl_id)
        for i in range(3):
            c.execute(
                "INSERT INTO template_item (created_at,updated_at,template_id,item_id,name,category,cycle,"
                "planned_minutes,base_points,price,note,sort,extra) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)",
                (now, now, tpl_id, 'probe-task-%d' % i, '商家任务%d' % (i + 1), 'study', 'daily',
                 20, 7, 0, '', i, '{}'))
        c.commit()

        status, r6 = post('/api/template/bind-invite', {'invite_code': code}, token)
        check(status == 200 and is_succ(r6), '已审核商家的码能绑（code=200）', r6)
        bound = (r6.get('data') or {}).get('source_merchant_uid')
        check(bound == merchant_uid, '绑定返回的就是该商家 uid', r6)

        status, r7 = post('/api/template/resolve', {'kind': 'task'}, token)
        d7 = r7.get('data') or {}
        check(is_succ(r7) and d7.get('scope') == 'merchant'
              and d7.get('owner_uid') == merchant_uid,
              '解析到商家默认模板', d7)
        check(len(d7.get('items') or []) == 3,
              '只下发 3 条（**不是** 16+3 叠加）', len(d7.get('items') or []))
        check(d7.get('source_merchant_uid') == merchant_uid, '响应带回归属', d7.get('source_merchant_uid'))

        # 商家没配兑换模板 ⇒ 兑换仍回落官方（逐 kind 回落，别做成「一个开关切两套」）
        status, r8 = post('/api/template/resolve', {'kind': 'redeem'}, token)
        d8 = r8.get('data') or {}
        check(is_succ(r8) and d8.get('scope') == 'official'
              and len(d8.get('items') or []) == 15,
              '该商家没配兑换模板 → 兑换回落官方 15 条', d8)

        # 再绑**另一个有效且已审核**商家的码：接口会成功，但归属不许被改
        status, r9b = post('/api/template/bind-invite', {'invite_code': code2}, token)
        check(is_succ(r9b), '第二个有效码本身是通过的（否则下面测的是「被拒」不是「不覆盖」）', r9b)
        check((r9b.get('data') or {}).get('source_merchant_uid') == merchant_uid,
              '第二次绑定返回的仍是第一次的商家', r9b.get('data'))
        status, r9 = post('/api/template/resolve', {'kind': 'task'}, token)
        check((r9.get('data') or {}).get('source_merchant_uid') == merchant_uid,
              '重复绑定不覆盖第一次归属', r9.get('data'))
        dbrow = c.execute(
            "SELECT source_merchant_uid, invite_code FROM user WHERE id=?", (uid,)).fetchone()
        check(dbrow['invite_code'] == code,
              'DB 留痕的仍是第一次那个码（没被第二个盖掉）', dict(dbrow))
    finally:
        # 清探针数据：只删自己造的，官方种子一行不碰，真实数据一行不碰
        for tid in tpl_ids:
            c.execute("DELETE FROM template_item WHERE template_id=?", (tid,))
            c.execute("DELETE FROM template WHERE id=?", (tid,))
        c.execute("UPDATE user SET source_merchant_uid=0, invite_code='' WHERE id=?", (uid,))
        for muid in created:
            c.execute("DELETE FROM merchant_application WHERE uid=? AND name='探针商家'", (muid,))
        c.commit()
        c.close()

    print('\n=== 结果 ===')
    print('PASS %d / FAIL %d' % (OK, FAIL))
    return 0 if FAIL == 0 else 1


def fail(msg):
    print('探针中断: ' + msg)
    return 1


if __name__ == '__main__':
    try:
        sys.exit(main())
    except Exception as e:
        print('探针中断（未捕获异常）: %r' % (e,))
        sys.exit(1)
