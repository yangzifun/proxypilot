-- 0004：Worker 动态补的扩展监控列，迁移时漏建
ALTER TABLE `server_metrics`
  ADD COLUMN `net_rx_bytes` BIGINT DEFAULT 0,
  ADD COLUMN `net_tx_bytes` BIGINT DEFAULT 0,
  ADD COLUMN `net_rx_rate` BIGINT DEFAULT 0,
  ADD COLUMN `net_tx_rate` BIGINT DEFAULT 0,
  ADD COLUMN `disk_read_bytes` BIGINT DEFAULT 0,
  ADD COLUMN `disk_write_bytes` BIGINT DEFAULT 0,
  ADD COLUMN `disk_read_rate` BIGINT DEFAULT 0,
  ADD COLUMN `disk_write_rate` BIGINT DEFAULT 0,
  ADD COLUMN `conn_total` BIGINT DEFAULT 0,
  ADD COLUMN `conn_established` BIGINT DEFAULT 0,
  ADD COLUMN `conn_timewait` BIGINT DEFAULT 0,
  ADD COLUMN `proc_total` BIGINT DEFAULT 0;
