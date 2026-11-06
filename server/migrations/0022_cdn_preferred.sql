-- 0022: CDN 优选地址
CREATE TABLE IF NOT EXISTS `cdn_preferred_addresses` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `group_name` VARCHAR(64) NOT NULL DEFAULT 'default',
  `address` VARCHAR(255) NOT NULL,
  `port` INT NOT NULL DEFAULT 443,
  `remark` VARCHAR(191) DEFAULT '',
  `enabled` TINYINT NOT NULL DEFAULT 1,
  `sort` INT NOT NULL DEFAULT 0,
  `last_check_at` BIGINT DEFAULT 0,
  `last_check_ok` TINYINT DEFAULT 0,
  `last_latency_ms` INT DEFAULT 0,
  `created_at` BIGINT DEFAULT 0,
  `updated_at` BIGINT DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_group_addr` (`group_name`, `address`, `port`),
  KEY `idx_cdn_prefer_group` (`group_name`, `enabled`, `sort`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
