# 一次性：给手工走查准备「可以激活」的状态（2026-11-08）
#
# 为什么：测试账号 uid=3 已经把 23 款全拥有了，心愿里每一款都显示「已拥有」，
# 「去激活」根本点不出来。挑一款退回去（收租公寓 renttower），配一枚真码，
# 这样 激活 → 装包 → 玩 一条链能走完。
#
# 幂等：重复跑只会把码重新铸一枚（旧码作废），不会重复插 user_game。
import os
import hashlib
import secrets
import sqlite3
import time

DB = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'data', 'tf.db')
UID = 3
ITEM = 'wish-game-renttower'
GAME = 'renttower'
ALPHABET = '0123456789ABCDEFGHJKMNPQRSTUVWXYZ'  # 不含 I / L / O，跟服务端 normalize 口径一致

c = sqlite3.connect(DB)
c.row_factory = sqlite3.Row

# 1) 退回所有权（硬删：软删 + 唯一索引会让「再授予」撞唯一键）
before = c.execute(
    'select count(*) from user_game where uid=? and game_id=? and deleted_at is null', (UID, GAME)
).fetchone()[0]
c.execute('delete from user_game where uid=? and game_id=?', (UID, GAME))
print(f'user_game 退回：{GAME} 原来 owned={before} → 现在 owned=0')

# 2) 作废这一款之前没用掉的码，免得同一款堆一串有效码
c.execute(
    "update wish_code set deleted_at=? where item_id=? and used=0 and deleted_at is null",
    (int(time.time()), ITEM),
)

# 3) 铸一枚新码
code = ''.join(secrets.choice(ALPHABET) for _ in range(6))
norm = code.upper().replace('I', '1').replace('L', '1').replace('O', '0')
digest = hashlib.sha256(('tf-wish:' + norm).encode()).hexdigest()
now = int(time.time())
c.execute(
    '''insert into wish_code
       (created_at, updated_at, deleted_at, code_hash, code_tail, item_id, batch, used, used_by_uid, used_at, expire_at)
       values (?, ?, null, ?, ?, ?, 'manual-1108', 0, 0, null, ?)''',
    (now, now, digest, code[-4:], ITEM, now + 30 * 86400),
)
c.commit()

row = c.execute('select id, item_id, code_tail, expire_at from wish_code order by id desc limit 1').fetchone()
print(f'新码：{code}  (id={row["id"]} item={row["item_id"]} tail={row["code_tail"]})')
print(f'过期：{time.strftime("%Y-%m-%d %H:%M", time.localtime(row["expire_at"]))}')

owned = c.execute(
    'select count(*) from user_game where uid=? and deleted_at is null', (UID,)
).fetchone()[0]
w = c.execute('select points, coupon from wallet where uid=?', (UID,)).fetchone()
print(f'账号现状：owned={owned}  points={w["points"]}  coupon={w["coupon"]}')
c.close()
