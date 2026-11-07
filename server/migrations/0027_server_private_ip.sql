-- 服务器内网 IP：Agent 经内网/WARP 直连时，面板看到的来源地址是内网地址，
-- 与公网出口不同，需要单独存储展示。
-- 幂等：MySQL 8 不支持 ADD COLUMN IF NOT EXISTS，用 information_schema 判断后再执行。
SET @has_col := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'servers' AND COLUMN_NAME = 'last_ip_private'
);
SET @ddl := IF(@has_col = 0,
  'ALTER TABLE servers ADD COLUMN last_ip_private VARCHAR(64) NOT NULL DEFAULT '''' AFTER last_ip6',
  'DO 0');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;