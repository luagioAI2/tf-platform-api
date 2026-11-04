-- 盲盒「一次开 N 款」（5 连开）
--
-- 两列，缺一不可：
--   · wish_item.unlock_count   —— 这件盲盒开几款（1 = 老的单开）
--   · wish_order.opened_game_ids —— 这一单实际开出的**全部**
--     （`opened_game_id` 仍然写第一个，老客户端只看那一个）
--
-- ⚠️ 只加 `unlock_count` 不加 `opened_game_ids` 的后果：
--   一单授了 5 款，但表里只记得第一款 ⇒ 取消时另外 4 款**收不回来**，
--   而且不报错（账本只看 delta 之和，界面只会显示「取消成功、券退了」）。

ALTER TABLE wish_item ADD COLUMN unlock_count INTEGER NOT NULL DEFAULT 1;

ALTER TABLE wish_order ADD COLUMN opened_game_ids TEXT NOT NULL DEFAULT '[]';
