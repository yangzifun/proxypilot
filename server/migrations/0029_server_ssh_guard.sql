-- SSH 安全防护开关的期望状态（1=仅允许组网网段，0=允许所有来源）
SET @has := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'servers' AND COLUMN_NAME = 'ssh_guard'
);
SET @ddl := IF(@has = 0,
  'ALTER TABLE servers ADD COLUMN ssh_guard TINYINT NOT NULL DEFAULT 0',
  'DO 0');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
