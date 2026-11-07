-- 组网（Cloudflare WARP）地址：与公网出口、本机内网都不同，单独存储。
SET @has4 := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'servers' AND COLUMN_NAME = 'last_mesh_ip4'
);
SET @ddl4 := IF(@has4 = 0,
  'ALTER TABLE servers ADD COLUMN last_mesh_ip4 VARCHAR(64) NOT NULL DEFAULT '''' AFTER last_ip_private',
  'DO 0');
PREPARE s4 FROM @ddl4;
EXECUTE s4;
DEALLOCATE PREPARE s4;

SET @has6 := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'servers' AND COLUMN_NAME = 'last_mesh_ip6'
);
SET @ddl6 := IF(@has6 = 0,
  'ALTER TABLE servers ADD COLUMN last_mesh_ip6 VARCHAR(64) NOT NULL DEFAULT '''' AFTER last_mesh_ip4',
  'DO 0');
PREPARE s6 FROM @ddl6;
EXECUTE s6;
DEALLOCATE PREPARE s6;
