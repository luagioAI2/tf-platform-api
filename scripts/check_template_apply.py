#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""模板落地（`/api/template/apply`）的真实接口探针。

跑之前：tf-api 要在 8080 上，且 EnableAutoMigrate=true（`template_apply` 表
和官方种子要已就绪）。

## 为什么这条链必须单独钉一遍

后台配模板（tf-admin）和 App 显示任务（kid-quest-farm-online）各自都能测绿，
中间这根线是 `/api/template/apply`：

    template_item（蓝图）
        → apply 在**服务端**铺出 task / redeem_item（实例）
            → App 的 pull 把实例搬进 Dexie

只测「模板里有 16 条」和「App 能显示任务」，中间铺错了照样全绿：
铺成 0 条、铺了但签到开关丢了、铺两次变 32 条 —— 这三种都不报错。

## 钉的判据

  · 新账号铺 16 任务 + 15 兑换品，且**看板真的看得到这 16 条**（不是只写库）
  · 第二次 apply 幂等：applied=false，库里的条数不变
  · 并发两次 apply 只铺一遍（uid 唯一键当锁）
  · 账号本来就有任务 ⇒ 一条都不动（reason=has-content），但兑换照补
  · 商家推广来的账号铺的是**商家的**模板，不是官方 16 条
  · 玩法参数没丢：签到开关 / 目标次数 / 奖励道具 / 提醒时间

★ 成功码是 200（errors.SUCCESS），不是 0；HTTP 恒 200，业务错误也 200。
"""
import json
import sqlite3
import sys
import threading
import time
import urllib.error
import urllib.request

BASE = 'http://127.0.0.1:8080'
DB = 'E:/plan/test25/tf-platform-api/data/tf.db'

SUCC = 200  # errors.SUCCESS
ERR_INVALID = 606  # errors.InvalidParam

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
        print('  ✗ ' + label + ('  → ' + str(detail)[:400] if detail else ''))


def is_succ(r):
    return r.get('code') == SUCC


def post(path, body, token=None, timeout=15):
    data = json.dumps(body).encode('utf-8')
    req = urllib.request.Request(BASE + path, data=data, method='POST')
    req.add_header('Content-Type', 'application/json')
    if token:
        req.add_header('Authorization', 'Bearer ' + token)
    try:
        with _OPENER.open(req, timeout=timeout) as r:
            return r.status, json.loads(r.read().decode('utf-8'))
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read().decode('utf-8'))


def sql():
    c = sqlite3.connect(DB, timeout=20)
    c.row_factory = sqlite3.Row
    return c


def guest(device_id):
    """拿一个游客账号（同 device_id 返回同一个 uid，重复跑不会堆积）。"""
    status, r = post('/api/auth/guest', {'device_id': device_id})
    d = r.get('data') or {}
    return d.get('access_token'), (d.get('user') or {}).get('uid')


def wipe(c, uid):
    """只清这个 uid 的业务数据 —— 官方模板（template / template_item）一行不碰。"""
    for table in ('task', 'task_instance', 'redeem_item', 'template_apply'):
        c.execute('DELETE FROM %s WHERE uid=?' % table, (uid,))
    c.execute("UPDATE user SET source_merchant_uid=0, invite_code='' WHERE id=?", (uid,))
    c.commit()


def counts(c, uid):
    t = c.execute('SELECT COUNT(*) n FROM task WHERE uid=? AND deleted_at IS NULL', (uid,)).fetchone()['n']
    r = c.execute(
        'SELECT COUNT(*) n FROM redeem_item WHERE uid=? AND deleted_at IS NULL', (uid,)).fetchone()['n']
    return t, r


def main():
    print('=== 0. 准备两个干净的游客账号 ===')
    token, uid = guest('probe-tpl-apply-0001')
    check(bool(token) and bool(uid), '游客 A 登录', uid)
    token2, uid2 = guest('probe-tpl-apply-0002')
    check(bool(token2) and bool(uid2) and uid2 != uid, '游客 B 登录（另一个号）', uid2)
    if not token or not token2:
        return fail('拿不到 token')

    c = sql()
    now = int(time.time())
    created_merchants = []
    tpl_ids = []
    try:
        wipe(c, uid)
        wipe(c, uid2)
        t0, r0 = counts(c, uid)
        check((t0, r0) == (0, 0), '前提：游客 A 的任务/兑换都是空的', (t0, r0))

        print('=== 1. 第一次 apply：铺 16 任务 + 15 兑换 ===')
        status, r = post('/api/template/apply', {}, token)
        d = r.get('data') or {}
        check(status == 200 and is_succ(r), 'apply 返回 code=200（不是 0）', r)
        check(d.get('applied') is True, 'applied=true', d)
        check(d.get('task_count') == 16, '铺了 16 条任务', d.get('task_count'))
        check(d.get('redeem_count') == 15, '铺了 15 条兑换品', d.get('redeem_count'))
        check(d.get('source_merchant_uid') == 0, '没归属商家', d.get('source_merchant_uid'))

        t1, r1 = counts(c, uid)
        check((t1, r1) == (16, 15), 'DB 里真的有 16 / 15 条', (t1, r1))

        row = c.execute('SELECT * FROM template_apply WHERE uid=?', (uid,)).fetchone()
        check(row is not None, 'template_apply 有记录（这是「只铺一次」的锁）', None)
        if row:
            check(row['task_count'] == 16 and row['redeem_count'] == 15,
                  '记录里写着铺了 16 / 15', dict(row))
            check(row['task_template_id'] > 0 and row['redeem_template_id'] > 0,
                  '记录里写着用的是哪份模板', dict(row))

        print('=== 2. 玩法参数没丢（Extra 拆列）===')
        def task_by_title(title):
            r_ = c.execute(
                'SELECT * FROM task WHERE uid=? AND title=? AND deleted_at IS NULL',
                (uid, title)).fetchone()
            return dict(r_) if r_ else {}

        sign = task_by_title('练字签到')
        check(bool(sign), '有「练字签到」这条', sign)
        check(sign.get('check_in_enabled') == 1, '签到开关落成 1（不是躺在 Extra 里）', sign)
        check(sign.get('check_in_target_count') == 5, '目标次数 5', sign.get('check_in_target_count'))
        check(sign.get('cycle') == 'weekly', '周期 weekly', sign.get('cycle'))

        rec = task_by_title('背诵')
        check(rec.get('check_in_enabled') == 0 and rec.get('check_in_target_count') == 2,
              '「背诵」开关关着但目标次数 2 保留（别被当成没填清成 0）', rec)

        cn = task_by_title('完成语文作业')
        check(cn.get('remind_at') == '18:30', '提醒时间 18:30', cn.get('remind_at'))
        check(cn.get('allow_overtime') == 1 and cn.get('quality_rated') == 1,
              '宽容超时 / 质量加分落列', cn)
        check(cn.get('planned_minutes') == 45 and cn.get('base_points') == 10,
              '时长 45 / 积分 10', cn)

        book = task_by_title('读一本完整的故事书')
        check('sticker' in (book.get('reward_item_ids') or ''),
              '奖励道具 sticker 落列', book.get('reward_item_ids'))

        print('=== 3. 两端之间那根线：看板真的看得到这 16 条 ===')
        status, rb = post('/api/task/board', {}, token)
        bd = rb.get('data') or {}
        check(is_succ(rb), 'board 返回 code=200', rb)
        check(bd.get('total') == 16, '看板 total=16（实例真的生成了）', bd.get('total'))
        titles = [i.get('title') for i in (bd.get('instances') or [])]
        check('练字签到' in titles, '签到任务出现在今天的看板上', titles)

        print('=== 4. 幂等：第二次 apply 一条都不加 ===')
        status, r2 = post('/api/template/apply', {}, token)
        d2 = r2.get('data') or {}
        check(is_succ(r2) and d2.get('applied') is False, '第二次 applied=false', d2)
        check(d2.get('reason') == 'already', 'reason=already', d2.get('reason'))
        t2, r2c = counts(c, uid)
        check((t2, r2c) == (16, 15), '库里的条数没变（没铺成 32）', (t2, r2c))

        print('=== 5. 并发两次 apply 只铺一遍 ===')
        results = []

        def fire():
            # ★ 必须是 token2：拿 A 的 token 去打，两次都会是 already，
            #   然后「条数还是 0」会被误读成「并发把数据弄丢了」。
            results.append(post('/api/template/apply', {}, token2)[1].get('data') or {})

        # 换个账号测并发，免得被上面那次 already 短路掉
        wipe(c, uid2)
        threads = [threading.Thread(target=fire) for _ in range(2)]
        for th in threads:
            th.start()
        for th in threads:
            th.join()
        applied = [x for x in results if x.get('applied')]
        t3, r3 = counts(c, uid2)
        check(len(results) == 2, '两个请求都回来了', results)
        check(len(applied) <= 1, '最多只有一次真的铺（另一次走 already）', results)
        check((t3, r3) == (16, 15), '并发之后仍然是 16 / 15（不是 32 / 30）', (t3, r3))

        print('=== 6. 账号本来就有任务 ⇒ 一条都不动 ===')
        # uid2 已经有 16 条了；这里再造「家长自己建了一条」的场景：
        # 清空后只留一条手工任务，再 apply
        wipe(c, uid2)
        status, rc = post('/api/task/create', {
            'title': '家长自己建的任务', 'category': 'study', 'cycle': 'daily',
            'planned_minutes': 30, 'base_points': 12,
        }, token2)
        check(is_succ(rc), '先建一条家长自己的任务', rc)
        status, r4 = post('/api/template/apply', {}, token2)
        d4 = r4.get('data') or {}
        # applied 的口径是「本次有没有写东西」：任务一条没动，但兑换补了 15 条
        check(d4.get('task_count') == 0, '一条任务都没铺（task_count=0）', d4)
        check(d4.get('redeem_count') == 15, '兑换补了 15 条（逐类判空）', d4)
        check(d4.get('reason') == 'has-content', 'reason=has-content', d4.get('reason'))
        t4, r4c = counts(c, uid2)
        check(t4 == 1, '任务还是只有那 1 条（没被灌成 17 条）', t4)
        check(r4c == 15, '兑换清单照补 15 条（逐类判空，不是一刀切）', r4c)
        kept = c.execute('SELECT title FROM task WHERE uid=?', (uid2,)).fetchall()
        check([k['title'] for k in kept] == ['家长自己建的任务'], '家长那条原样还在', kept)

        print('=== 7. 商家推广来的账号铺商家的模板 ===')
        # 造一个已审核商家 + 一份 2 条任务的默认模板
        mrow = c.execute(
            "SELECT uid FROM merchant_application WHERE role='merchant' AND status='approved'"
            " AND deleted_at IS NULL AND name='探针商家' LIMIT 1").fetchone()
        if mrow:
            merchant_uid = mrow['uid']
        else:
            merchant_uid = 995001
            c.execute(
                "INSERT INTO merchant_application (created_at,updated_at,uid,role,status,name,contact,reviewed_at)"
                " VALUES (?,?,?,'merchant','approved','探针商家','-',?)",
                (now, now, merchant_uid, now))
            created_merchants.append(merchant_uid)

        old = c.execute(
            "SELECT id FROM template WHERE scope='merchant' AND kind='task' AND owner_uid=?",
            (merchant_uid,)).fetchone()
        if old:
            c.execute('DELETE FROM template_item WHERE template_id=?', (old['id'],))
            c.execute('DELETE FROM template WHERE id=?', (old['id'],))
        cur = c.execute(
            "INSERT INTO template (created_at,updated_at,name,kind,scope,owner_uid,is_default,note,sort,source)"
            " VALUES (?,?,?,?,?,?,?,?,?,?)",
            (now, now, '探针商家任务模板', 'task', 'merchant', merchant_uid, 1, 'probe', 0, 'manual'))
        tpl_id = cur.lastrowid
        tpl_ids.append(tpl_id)
        for i, (nm, pts) in enumerate([('商家任务一', 8), ('商家任务二', 9)]):
            c.execute(
                "INSERT INTO template_item (created_at,updated_at,template_id,item_id,name,category,cycle,"
                "planned_minutes,base_points,price,note,sort,extra) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)",
                (now, now, tpl_id, 'probe-m-task-%d' % i, nm, 'study', 'daily',
                 20, pts, 0, '', i, '{"allowOvertime":true}'))
        c.commit()

        # 邀请码：和 Go 的 MakeInviteCode 独立实现一遍
        import hashlib
        digits = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ'
        n, b36 = merchant_uid, ''
        while n:
            b36 = digits[n % 36] + b36
            n //= 36
        code = 'TF' + b36 + '-' + hashlib.sha256(
            ('tf-invite-v1:%d' % merchant_uid).encode()).hexdigest()[:4].upper()

        status, r5 = post('/api/template/bind-invite', {'invite_code': code}, token2)
        check(is_succ(r5), '绑定商家邀请码成功', r5)
        wipe_tasks_only(c, uid2)  # 只清任务/兑换/apply，保留商家归属
        status, r6 = post('/api/template/apply', {}, token2)
        d6 = r6.get('data') or {}
        check(d6.get('applied') is True, '商家账号铺成功', d6)
        check(d6.get('source_merchant_uid') == merchant_uid, '响应带回归属', d6)
        names = [x['title'] for x in c.execute(
            'SELECT title FROM task WHERE uid=? ORDER BY sort, id', (uid2,)).fetchall()]
        check(names == ['商家任务一', '商家任务二'],
              '铺的是商家那 2 条（不是官方 16 条，也不是叠加）', names)

        print('=== 8. 没登录不许 apply ===')
        status, anon = post('/api/template/apply', {})
        check(anon.get('code') != SUCC, '没登录取不到（HTTP 200 也可能是拒绝）', anon)
        check(anon.get('data') in (None, {}, []), '拒绝时 data 是空的', anon.get('data'))
    finally:
        for tid in tpl_ids:
            c.execute('DELETE FROM template_item WHERE template_id=?', (tid,))
            c.execute('DELETE FROM template WHERE id=?', (tid,))
        for uid_ in (uid, uid2):
            if uid_:
                c.execute('DELETE FROM task_instance WHERE uid=?', (uid_,))
                c.execute('DELETE FROM task WHERE uid=?', (uid_,))
                c.execute('DELETE FROM redeem_item WHERE uid=?', (uid_,))
                c.execute('DELETE FROM template_apply WHERE uid=?', (uid_,))
                c.execute(
                    "UPDATE user SET source_merchant_uid=0, invite_code='' WHERE id=?", (uid_,))
        for muid in created_merchants:
            c.execute("DELETE FROM merchant_application WHERE uid=? AND name='探针商家'", (muid,))
        c.commit()
        c.close()

    print('\n=== 结果 ===')
    print('PASS %d / FAIL %d' % (OK, FAIL))
    return 0 if FAIL == 0 else 1


def wipe_tasks_only(c, uid):
    """清任务/兑换/落地记录，但**保留**商家归属（测商家模板要用）。"""
    for table in ('task', 'task_instance', 'redeem_item', 'template_apply'):
        c.execute('DELETE FROM %s WHERE uid=?' % table, (uid,))
    c.commit()


def fail(msg):
    print('探针中断: ' + msg)
    return 1


if __name__ == '__main__':
    try:
        sys.exit(main())
    except Exception as e:
        print('探针中断（未捕获异常）: %r' % (e,))
        sys.exit(1)
