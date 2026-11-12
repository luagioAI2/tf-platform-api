-- 主线活动：领取记录（2026-11-12）
--
-- 每领一关写一行。**两件事必须一起成立**：这行在，游戏就一定在 `user_game`；
-- 反过来（行在、游戏没授）就是孩子这一关永远领不到 —— 客户端看到「已领取」，
-- 界面再也给不出领取入口，而两边都不报错。所以服务端把两件事放在同一个事务里。
--
-- 唯一索引建在 `(uid, step_id)`，不是 `(uid, game_id)`：
-- 两关以后可能送同一款游戏（改配置时很容易），按游戏去重会让人领不到第二关。
--
-- ⚠️ 生产环境 `EnableAutoMigrate` 会被忽略（`providers.go` 里判 IsProduction），
--    所以这张表**必须**在这里建 —— 只在本地跑 AutoMigrate 是不够的。

CREATE TABLE IF NOT EXISTS `campaign_claim` (
  `id` integer PRIMARY KEY AUTOINCREMENT,
  `created_at` integer,
  `updated_at` integer,
  `deleted_at` datetime,
  `uid` integer NOT NULL,
  `step_id` text NOT NULL,
  `game_id` text NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_campaign_claim_uid_step`
  ON `campaign_claim`(`uid`, `step_id`);
CREATE INDEX IF NOT EXISTS `idx_campaign_claim_deleted_at`
  ON `campaign_claim`(`deleted_at`);
