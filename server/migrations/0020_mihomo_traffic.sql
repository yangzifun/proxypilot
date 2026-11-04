-- mihomo 按用户流量（由上报差分生成）+ 节点上报令牌

ALTER TABLE `mihomo_nodes` ADD COLUMN `report_token` VARCHAR(64) NULL;

CREATE TABLE IF NOT EXISTS `mihomo_traffic` (
  `uuid` VARCHAR(36) NOT NULL,
  `up_total` BIGINT DEFAULT 0,
  `down_total` BIGINT DEFAULT 0,
  `last_active_at` BIGINT DEFAULT 0,
  `updated_at` BIGINT DEFAULT 0,
  PRIMARY KEY (`uuid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `mihomo_traffic_hourly` (
  `uuid` VARCHAR(36) NOT NULL,
  `node_id` BIGINT NOT NULL,
  `hour_ts` BIGINT NOT NULL,
  `up` BIGINT DEFAULT 0,
  `down` BIGINT DEFAULT 0,
  PRIMARY KEY (`uuid`, `node_id`, `hour_ts`),
  KEY `idx_mihomo_traffic_hourly_ts` (`hour_ts`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
