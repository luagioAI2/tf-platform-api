-- 示例迁移文件（golang-migrate 格式）
-- 命名规范：{version}_{description}.up.sql / .down.sql
--
-- 使用方式：
--   migrate -path migrations -database "mysql://user:pass@tcp(host:3306)/db" up
--
-- 本项目约定：
--   1. 生产环境禁止 AutoMigrate，所有 DDL 走本目录
--   2. 每个迁移必须同时提供 up 与 down
--   3. 迁移一旦合入主干，不允许修改内容（只能追加新迁移）

-- 示例：创建用户表
CREATE TABLE IF NOT EXISTS `user` (
    `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '用户ID',
    `username`   VARCHAR(32)     NOT NULL COMMENT '用户名',
    `password`   VARCHAR(64)     NOT NULL COMMENT '密码哈希',
    `nickname`   VARCHAR(32)     NOT NULL DEFAULT '' COMMENT '昵称',
    `mobile`     VARCHAR(20)     NOT NULL DEFAULT '' COMMENT '手机号',
    `email`      VARCHAR(64)     NOT NULL DEFAULT '' COMMENT '邮箱',
    `status`     TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '状态 0禁用 1正常',
    `language`   VARCHAR(8)      NOT NULL DEFAULT 'en' COMMENT '语言偏好',
    `created_at` BIGINT          NOT NULL DEFAULT 0 COMMENT '创建时间(Unix秒)',
    `updated_at` BIGINT          NOT NULL DEFAULT 0 COMMENT '更新时间(Unix秒)',
    `deleted_at` BIGINT          DEFAULT NULL COMMENT '删除时间(Unix秒)',
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_username` (`username`),
    KEY `idx_mobile` (`mobile`),
    KEY `idx_email` (`email`),
    KEY `idx_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户表';
