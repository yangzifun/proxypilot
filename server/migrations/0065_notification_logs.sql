-- 0065: 通知发送记录（按批次记录，不按收件人拆条）。
CREATE TABLE IF NOT EXISTS `notification_logs` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `kind` VARCHAR(32) NOT NULL DEFAULT '',
  `title` VARCHAR(191) NOT NULL DEFAULT '',
  `server_ids` TEXT,
  `recipients` TEXT,
  `status` VARCHAR(16) NOT NULL DEFAULT '',
  `error` TEXT,
  `created_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_notification_logs_created` (`created_at`),
  KEY `idx_notification_logs_kind_created` (`kind`, `created_at`),
  KEY `idx_notification_logs_status_created` (`status`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;