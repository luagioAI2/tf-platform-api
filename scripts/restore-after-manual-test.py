# 手工走查收尾：把账号恢复成「23 款全拥有」，并作废剩下的测试码（2026-11-08）
#
# 手工走查前用 `_prep-manual-test.py` 把「收租公寓」退回去 + 铸了一枚码；
# 这里做反向操作。幂等：重复跑只会重复补同一条 user_game。
#
# ★ 为什么用硬删/硬插而不是软删：`user_game` 上有 (uid, game_id) 唯一索引，
#   软删的行还在表里 ⇒ 再授予会撞唯一键（GORM 的 upsert 也救不了）。
#
# 用法：python scripts/restore-after-manual-test.py
import os
import sqlite3
import time

DB = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'data', 'tf.db')
UID = 3
# 手工走查期间退回去过的游戏（激活码就是发给它们的）
GAMES = ('renttower', 'farmlord')

c = sqlite3.connect(DB)
c.row_factory = sqlite3.Row

before = c.execute(
    'select count(*) from user_game where uid=? and deleted_at is null', (UID,)
).fetchone()[0]

now = int(time.time())
for g in GAMES:
    c.execute(
        '''insert or ignore into user_game
           (created_at, updated_at, deleted_at, uid, game_id, source, price_paid, currency, order_id)
           values (?, ?, null, ?, ?, 'manual', 0, null, null)''',
        (now, now, UID, g),
    )

# 没用掉的测试码一律作废，免得留在库里被误用
n = 0
for g in GAMES:
    n += c.execute(
        "update wish_code set deleted_at=? where item_id=? and used=0 and deleted_at is null",
        (now, 'wish-game-' + g),
    ).rowcount
c.commit()

after = c.execute(
    'select count(*) from user_game where uid=? and deleted_at is null', (UID,)
).fetchone()[0]
for g in GAMES:
    own = c.execute(
        "select 1 from user_game where uid=? and game_id=? and deleted_at is null", (UID, g)
    ).fetchone()
    print(f'  {g}: {"有" if own else "没有"}')

print(f'user_game：{before} → {after} 款')
print(f'作废未用的测试码：{n} 枚')

# 提醒：8090 是我手工走查时停掉的（两个进程同写一个 tf.db 会 database is locked）
print('别忘了把 8090 起回来：cd tf-platform-api && ./tf-api.exe web -c ../_goprobe/config.8090.yaml')
c.close()
