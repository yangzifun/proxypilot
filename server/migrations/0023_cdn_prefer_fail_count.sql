-- 0023: CDN 优选连续失败计数（用于自动停用）
ALTER TABLE `cdn_preferred_addresses`
  ADD COLUMN `fail_count` INT NOT NULL DEFAULT 0 AFTER `last_latency_ms`;
