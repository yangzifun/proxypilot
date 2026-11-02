-- 0007：服务器解锁检测缓存
CREATE TABLE IF NOT EXISTS `server_unlock_checks` (
  `server_id` BIGINT UNSIGNED NOT NULL,
  `results` LONGTEXT NOT NULL,
  `checked_at` BIGINT NOT NULL DEFAULT 0,
  `task_id` BIGINT NOT NULL DEFAULT 0,
  `updated_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`server_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
