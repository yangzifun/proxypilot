-- 0066: 服务器上下线事件（仅状态真实切换时写入；online 记录离线时长）。
CREATE TABLE IF NOT EXISTS `server_status_events` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `server_id` BIGINT NOT NULL,
  `event` VARCHAR(16) NOT NULL DEFAULT '',
  `reason` TEXT,
  `occurred_at` BIGINT NOT NULL DEFAULT 0,
  `duration_sec` BIGINT NOT NULL DEFAULT 0,
  `created_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_server_status_events_server_time` (`server_id`, `occurred_at`),
  KEY `idx_server_status_events_event_time` (`event`, `occurred_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;