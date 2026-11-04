-- mihomo 客户端上报（external-controller / 脚本上报）

CREATE TABLE IF NOT EXISTS `mihomo_reports` (
  `uuid` VARCHAR(36) NOT NULL,
  `up_total` BIGINT DEFAULT 0,
  `down_total` BIGINT DEFAULT 0,
  `connections` BIGINT DEFAULT 0,
  `ip` VARCHAR(64) DEFAULT '',
  `ua` VARCHAR(255) DEFAULT '',
  `updated_at` BIGINT DEFAULT 0,
  PRIMARY KEY (`uuid`),
  KEY `idx_mihomo_reports_ts` (`updated_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
