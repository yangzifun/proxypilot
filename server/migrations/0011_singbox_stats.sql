-- 0011：sing-box 用户流量 / 连接统计（V15.6）
--
-- 数据来源：sb.json 的 experimental.clash_api（连接明细）与 experimental.v2ray_api
--          （按用户的累计上下行）。Agent 每轮 poll 读取本机 127.0.0.1 的这两个接口，
--          把快照随下一次 poll 上报；服务端做差分后落库。
-- 口径：user_traffic_hourly 存增量；node_user_counter 存上一轮绝对值（差分用）；
--       node_connections 只保留最新快照，不进历史。

ALTER TABLE `singbox_nodes`
  ADD COLUMN `stats_enabled` BIGINT DEFAULT 1,
  ADD COLUMN `stats_secret_enc` VARCHAR(255) DEFAULT '';

-- 用户累计（跨节点汇总）
CREATE TABLE IF NOT EXISTS `user_traffic` (
  `uuid` VARCHAR(36) NOT NULL,
  `up_total` BIGINT DEFAULT 0,
  `down_total` BIGINT DEFAULT 0,
  `last_active_at` BIGINT DEFAULT 0,
  `updated_at` BIGINT DEFAULT 0,
  PRIMARY KEY (`uuid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

-- 小时聚合（用户 × 节点）
CREATE TABLE IF NOT EXISTS `user_traffic_hourly` (
  `uuid` VARCHAR(36) NOT NULL,
  `node_id` BIGINT NOT NULL,
  `hour_ts` BIGINT NOT NULL,
  `up` BIGINT DEFAULT 0,
  `down` BIGINT DEFAULT 0,
  PRIMARY KEY (`uuid`, `node_id`, `hour_ts`),
  KEY `idx_user_traffic_hourly_ts` (`hour_ts`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

-- 上一轮绝对值（差分基线）
CREATE TABLE IF NOT EXISTS `node_user_counter` (
  `node_id` BIGINT NOT NULL,
  `uuid` VARCHAR(36) NOT NULL,
  `up_total` BIGINT DEFAULT 0,
  `down_total` BIGINT DEFAULT 0,
  `sampled_at` BIGINT DEFAULT 0,
  PRIMARY KEY (`node_id`, `uuid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

-- 节点最近一次快照（速率 / 连接数）
CREATE TABLE IF NOT EXISTS `node_stats_snapshot` (
  `node_id` BIGINT NOT NULL,
  `sampled_at` BIGINT DEFAULT 0,
  `conn_count` BIGINT DEFAULT 0,
  `up_total` BIGINT DEFAULT 0,
  `down_total` BIGINT DEFAULT 0,
  `up_rate` BIGINT DEFAULT 0,
  `down_rate` BIGINT DEFAULT 0,
  `err_text` LONGTEXT,
  PRIMARY KEY (`node_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

-- 连接明细（只保留最新快照）
CREATE TABLE IF NOT EXISTS `node_connections` (
  `node_id` BIGINT NOT NULL,
  `conn_id` VARCHAR(64) NOT NULL,
  `source_ip` VARCHAR(45),
  `source_port` BIGINT DEFAULT 0,
  `host` VARCHAR(255),
  `network` VARCHAR(16),
  `inbound` VARCHAR(64),
  `upload` BIGINT DEFAULT 0,
  `download` BIGINT DEFAULT 0,
  `started_at` BIGINT DEFAULT 0,
  `seen_at` BIGINT DEFAULT 0,
  PRIMARY KEY (`node_id`, `conn_id`),
  KEY `idx_node_connections_seen` (`seen_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs
