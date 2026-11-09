# 铸一枚测试用激活码（2026-11-08）
#
# 为什么要自己铸：这个库里**没有**「后台生成码」的接口，历史那些码
# （batch='probe-1105'）也是探针直接插的。码只存 sha256，明文捞不回来。
#
# 口径必须和服务端 `hashWishCode` 逐条一致：
#   sha256("tf-wish:" + normalize(code))，normalize = 大写 + 去 `空格 - _ · ・`
#   + `I,L → 1`、`O → 0`。
#
# 用法：
#   python scripts/mint-test-code.py wish-game-farmlord              # 只铸码
#   python scripts/mint-test-code.py wish-game-farmlord --revoke      # 顺便退回所有权
#   python scripts/mint-test-code.py wish-game-farmlord --revoke --uid 3
import os
import argparse
import hashlib
import secrets
import sqlite3
import time
DB = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'data', 'tf.db')

ALPHABET = '0123456789ABCDEFGHJKMNPQRSTUVWXYZ'  # 不含 I / L / O

ap = argparse.ArgumentParser()
ap.add_argument('item', help='wish_item.item_id，例如 wish-game-farmlord')
ap.add_argument('--uid', type=int, default=3, help='退回所有权给谁（默认测试家长 3）')
ap.add_argument('--revoke', action='store_true', help='先把这个游戏从该账号退回（硬删 user_game）')
ap.add_argument('--days', type=int, default=30)
args = ap.parse_args()

game = args.item[len('wish-game-'):] if args.item.startswith('wish-game-') else args.item

c = sqlite3.connect(DB)
c.row_factory = sqlite3.Row

row = c.execute('select item_id, kind, game_id from wish_item where item_id=?', (args.item,)).fetchone()
if not row:
    raise SystemExit(f'心愿里没有 {args.item}')
print(f'心愿商品：{row["item_id"]}  kind={row["kind"]}  game_id={row["game_id"] or "(空)"}')

if args.revoke:
    had = c.execute(
        'select 1 from user_game where uid=? and game_id=? and deleted_at is null', (args.uid, game)
    ).fetchone()
    # 硬删：软删的行还在表里，再授予会撞 (uid, game_id) 唯一键
    c.execute('delete from user_game where uid=? and game_id=?', (args.uid, game))
    print(f'所有权：uid={args.uid} 的 {game} 原来 {"有" if had else "没有"} → 现在退回')

# 同一款没用的旧码作废，免得库里堆一串有效码
voided = c.execute(
    'update wish_code set deleted_at=? where item_id=? and used=0 and deleted_at is null',
    (int(time.time()), args.item),
).rowcount

code = ''.join(secrets.choice(ALPHABET) for _ in range(6))
norm = code.upper()
for a, b in (('I', '1'), ('L', '1'), ('O', '0')):
    norm = norm.replace(a, b)
digest = hashlib.sha256(('tf-wish:' + norm).encode()).hexdigest()

now = int(time.time())
c.execute(
    '''insert into wish_code
       (created_at, updated_at, deleted_at, code_hash, code_tail, item_id, batch, used, used_by_uid, used_at, expire_at)
       values (?, ?, null, ?, ?, ?, 'manual-1108', 0, 0, null, ?)''',
    (now, now, digest, code[-4:], args.item, now + args.days * 86400),
)
c.commit()

print(f'作废旧码：{voided} 枚')
print(f'★ 新码：{code}   （{row["item_id"]}，{args.days} 天有效，一次性）')
print(f'  过期：{time.strftime("%Y-%m-%d %H:%M", time.localtime(now + args.days * 86400))}')
c.close()
