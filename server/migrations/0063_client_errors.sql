-- 0063: 客户端错误持久化（前端运行时错误上报查询）。
--
-- 只存错误定位字段，不存 Authorization/Cookie/query：
--   * URL 在写入前已由 API 去除 query/fragment；
--   * message/url/page/mode/ua 与上报截断长度保持一致；
--   * stack 使用 TEXT，最多按上报截断 4000 字符写入。
-- 保留期由 cronCleanup 按 app_settings.client_error_retention_days（默认 30 天）清理。
CREATE TABLE IF NOT EXISTS `client_errors` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `message` VARCHAR(500) NOT NULL DEFAULT '',
  `stack` TEXT,
  `url` VARCHAR(500) NOT NULL DEFAULT '',
  `page` VARCHAR(500) NOT NULL DEFAULT '',
  `mode` VARCHAR(20) NOT NULL DEFAULT '',
  `ua` VARCHAR(300) NOT NULL DEFAULT '',
  `created_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_client_errors_created` (`created_at`),
  KEY `idx_client_errors_mode_created` (`mode`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;