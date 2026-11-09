# -*- coding: utf-8 -*-
"""给某个账号补铺官方模板（任务 + 兑换品），只加不改（2026-11-08）。

为什么要有它：
  服务端播种的口径是「**这个账号已经有内容就整体跳过**」
  （`template_apply.skipped = 'has-content'`）。测试号 uid=3 在模板第一次
  下发之前就已经被探针建了 2 条任务、3 件兑换品 ⇒ 16 条官方任务一条都没落地，
  界面上看着像「默认模板只有两个」，其实是根本没铺。

  真实用户不会碰到（新账号是空的，一定铺得上）。这里只补测试号的账。

口径：
  · 只**新增**缺失的条目（按标题 / item_id 判重），不覆盖、不删任何现有数据；
  · 映射逐字段对齐 `repository.TaskFromTemplateItem` / `RedeemItemFromTemplateItem`
    （列上有的写列，剩下的原样塞 `extra_json`）；
  · 默认 dry-run，加 `--write` 才落库；写完打印前后条数。

用法：
  python scripts/seed-template-for-uid.py --uid 3            # 只看会补哪些
  python scripts/seed-template-for-uid.py --uid 3 --write    # 真写
"""
import os
import argparse
import json
import sqlite3
import time

DB = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'data', 'tf.db')
OFFICIAL = {'task': 1, 'redeem': 2}  # 官方模板 id（seed 出来的那两份）

TASK_COLUMNS = (
    'created_at,updated_at,uid,title,note,category,cycle,planned_minutes,base_points,'
    'allow_overtime,allow_late_no_penalty,quality_rated,reward_item_ids,'
    'window_start_minute,window_end_minute,check_in_enabled,check_in_target_count,'
    'remind_at,archived,sort,extra_json'
)
REDEEM_COLUMNS = (
    'created_at,updated_at,uid,item_id,name,emoji,category,price,note,'
    'limit_per_day,once,stock,archived,created_by_parent,sort'
)

TASK_HANDLED = {
    'allowOvertime',
    'allowLateNoPenalty',
    'qualityRated',
    'checkInEnabled',
    'checkInTargetCount',
    'remindAt',
    'rewardItemIds',
    'windowStartMinute',
    'windowEndMinute',
}


def jbool(ex, key):
    v = ex.get(key)
    return 1 if v is True or v == 1 or v == '1' else 0


def jint(ex, key, default=0):
    v = ex.get(key)
    try:
        return int(v)
    except (TypeError, ValueError):
        return default


def task_row(uid, it, now):
    ex = json.loads(it['extra'] or '{}')
    target = jint(ex, 'checkInTargetCount')
    if jbool(ex, 'checkInEnabled') and target <= 0:
        target = 1
    rest = {k: v for k, v in ex.items() if k not in TASK_HANDLED}
    return (
        now,
        now,
        uid,
        it['name'],
        it['note'] or '',
        it['category'] or 'study',
        it['cycle'] or 'daily',
        it['planned_minutes'] or 30,
        it['base_points'] or 10,
        jbool(ex, 'allowOvertime'),
        jbool(ex, 'allowLateNoPenalty'),
        jbool(ex, 'qualityRated'),
        json.dumps(ex.get('rewardItemIds') or [], ensure_ascii=False),
        jint(ex, 'windowStartMinute') if 'windowStartMinute' in ex else None,
        jint(ex, 'windowEndMinute') if 'windowEndMinute' in ex else None,
        jbool(ex, 'checkInEnabled'),
        target,
        ex.get('remindAt') or '',
        0,
        it['sort'] or 0,
        json.dumps(rest, ensure_ascii=False, sort_keys=True) if rest else '',
    )


def redeem_row(uid, it, now):
    ex = json.loads(it['extra'] or '{}')
    return (
        now,
        now,
        uid,
        it['item_id'],
        it['name'],
        it['emoji'] or '',
        it['category'] or 'privilege',
        it['price'] or 0,
        it['note'] or '',
        it['limit_per_day'] or 0,
        1 if it['once'] else 0,
        it['stock'] if it['stock'] is not None else -1,
        0,
        1 if ex.get('createdByParent') else 0,
        it['sort'] or 0,
    )


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--uid', type=int, required=True)
    ap.add_argument('--kind', choices=['task', 'redeem', 'both'], default='both')
    ap.add_argument('--write', action='store_true', help='不加这个只打印，不落库')
    a = ap.parse_args()

    c = sqlite3.connect(DB)
    c.row_factory = sqlite3.Row
    now = int(time.time())

    kinds = ['task', 'redeem'] if a.kind == 'both' else [a.kind]
    for kind in kinds:
        tid = OFFICIAL[kind]
        items = c.execute(
            'select * from template_item where template_id=? and deleted_at is null order by sort, id',
            (tid,),
        ).fetchall()
        if kind == 'task':
            have = {
                (r['title'] or '').strip()
                for r in c.execute(
                    'select title from task where uid=? and deleted_at is null', (a.uid,)
                )
            }
            cols, sql = TASK_COLUMNS, 'insert into task (%s) values (%s)' % (
                TASK_COLUMNS,
                ','.join('?' * len(TASK_COLUMNS.split(','))),
            )
            label = '任务'
        else:
            # 兑换品两张口径一起判重：老数据（家长手建的）`item_id` 是空串，
               # 只按 item_id 判会把「看一集动画片」这类同名品再铺一遍。
            rows = c.execute(
                'select item_id,name from redeem_item where uid=? and deleted_at is null', (a.uid,)
            ).fetchall()
            have = {(r['item_id'] or '').strip() for r in rows}
            have_names = {(r['name'] or '').strip() for r in rows}
            cols, sql = REDEEM_COLUMNS, 'insert into redeem_item (%s) values (%s)' % (
                REDEEM_COLUMNS,
                ','.join('?' * len(REDEEM_COLUMNS.split(','))),
            )
            label = '兑换品'

        before = len(have)
        todo = []
        for it in items:
            if kind == 'task':
                if it['name'].strip() in have:
                    continue
            else:
                if (it['item_id'] or '').strip() in have or it['name'].strip() in have_names:
                    continue
            todo.append(it)

        print('【%s】模板 %d 条 · 账号现有 %d 条 · 要补 %d 条' % (label, len(items), before, len(todo)))
        for it in todo:
            print('   + %s' % it['name'])

        if not a.write:
            continue
        for it in todo:
            row = task_row(a.uid, it, now) if kind == 'task' else redeem_row(a.uid, it, now)
            c.execute(sql, row)
        c.commit()
        after = c.execute(
            'select count(*) from %s where uid=? and deleted_at is null'
            % ('task' if kind == 'task' else 'redeem_item'),
            (a.uid,),
        ).fetchone()[0]
        print('   → 写入完成，现在 %d 条' % after)

    if not a.write:
        print('\n（dry-run，没有落库；要真写加 --write）')


if __name__ == '__main__':
    main()
