-- 心愿商城：一次性兑换
--
-- wish_offer.one_time = 1 时，兑换成功那一刻在**同一个事务里**把
-- closed_at 置为当前时间：
--   · 商品从「兑换」列表里消失；
--   · 心愿商城里回到「我想要」，孩子可以重新许愿、家长重新定价。
--
-- 记在 wish_offer 而不是 wish_item 上，因为它是**本家庭的选择**：
-- 同一件玩具，这家想「只能换一次」，那家可能想「每周都能换」。
--
-- ⚠️ 关闭动作不能交给客户端再发一次 set-offer —— 那一次失败时
--   券已经扣了、商品却还挂在兑换列表上，孩子能再换一遍，而两边都不报错。

ALTER TABLE wish_offer ADD COLUMN one_time INTEGER NOT NULL DEFAULT 0;
