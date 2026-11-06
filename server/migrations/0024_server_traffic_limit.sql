-- 0024: 服务器流量上限与按月重置
CREATE TABLE IF NOT EXISTS `server_traffic_limits` (
  `server_id` BIGINT NOT NULL,
  `enabled` TINYINT NOT NULL DEFAULT 0,
  `mode` ENUM('rx','tx','both') NOT NULL DEFAULT 'both',
  `limit_bytes` BIGINT NOT NULL DEFAULT 0,
  `reset_day` TINYINT NOT NULL DEFAULT 1,
  `reset_hour` TINYINT NOT NULL DEFAULT 0,
  `action` ENUM('notify','suspend') NOT NULL DEFAULT 'notify',
  `warn_percent` TINYINT NOT NULL DEFAULT 80,
  `updated_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`server_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `server_traffic_usage` (
  `server_id` BIGINT NOT NULL,
  `period_start` BIGINT NOT NULL,
  `rx_bytes` BIGINT NOT NULL DEFAULT 0,
  `tx_bytes` BIGINT NOT NULL DEFAULT 0,
  `last_rx_total` BIGINT NOT NULL DEFAULT 0,
  `last_tx_total` BIGINT NOT NULL DEFAULT 0,
  `last_sample_at` BIGINT NOT NULL DEFAULT 0,
  `exceeded_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`server_id`, `period_start`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE `servers`
  ADD COLUMN `traffic_state` ENUM('normal','warning','exceeded') NOT NULL DEFAULT 'normal';
