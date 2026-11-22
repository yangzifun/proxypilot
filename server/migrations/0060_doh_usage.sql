-- 0060: 面板内置 DoH（DNS over HTTPS）按小时使用量统计。
--
-- 设计：
--   * 面板只作为 DoH 入口（/dns-query/<uuid>），不记录 DNS 查询内容；默认只累计次数/字节。
--   * node_id 固定 0（DoH 仅面板提供，不部署到节点）；保留字段便于以后扩展到节点。
--   * 每个 UUID + 小时一行，主键 (uuid, node_id, hour_ts)；查询统计按 uuid 聚合。
--   * 不建外键，避免用户删除 UUID 时阻塞/级联；统计可保留历史。
CREATE TABLE IF NOT EXISTS `doh_usage_hourly` (
  `uuid` VARCHAR(36) NOT NULL COMMENT 'UUID',
  `node_id` BIGINT NOT NULL DEFAULT 0 COMMENT '节点；面板固定 0',
  `hour_ts` BIGINT NOT NULL COMMENT '小时起始毫秒时间戳',
  `queries` BIGINT NOT NULL DEFAULT 0 COMMENT '查询次数（成功+失败）',
  `bytes_in` BIGINT NOT NULL DEFAULT 0 COMMENT 'DNS 请求字节数',
  `bytes_out` BIGINT NOT NULL DEFAULT 0 COMMENT 'DNS 响应字节数（失败为 0）',
  `updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '最后更新毫秒时间戳',
  PRIMARY KEY (`uuid`, `node_id`, `hour_ts`),
  KEY `idx_doh_usage_hour` (`hour_ts`),
  KEY `idx_doh_usage_uuid_hour` (`uuid`, `hour_ts`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs COMMENT='DoH 按小时使用量（默认不记录查询内容）';