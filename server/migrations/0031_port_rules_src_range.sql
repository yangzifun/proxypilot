-- 端口规则增加：来源网段（原 IP/CIDR）与端口范围
SET @has_src := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'local_port_rules' AND COLUMN_NAME = 'src_cidr'
);
SET @ddl := IF(@has_src = 0,
  'ALTER TABLE local_port_rules ADD COLUMN src_cidr VARCHAR(64) NOT NULL DEFAULT '''' AFTER note',
  'DO 0');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @has_end := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'local_port_rules' AND COLUMN_NAME = 'port_end'
);
SET @ddl2 := IF(@has_end = 0,
  'ALTER TABLE local_port_rules ADD COLUMN port_end INT NOT NULL DEFAULT 0 AFTER port',
  'DO 0');
PREPARE stmt2 FROM @ddl2;
EXECUTE stmt2;
DEALLOCATE PREPARE stmt2;

-- 唯一键改为 (port, port_end, protocol, src_cidr)
SET @has_old_uk := (
  SELECT COUNT(*) FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'local_port_rules' AND INDEX_NAME = 'uk_port_proto'
);
SET @ddl3 := IF(@has_old_uk > 0, 'ALTER TABLE local_port_rules DROP INDEX uk_port_proto', 'DO 0');
PREPARE stmt3 FROM @ddl3;
EXECUTE stmt3;
DEALLOCATE PREPARE stmt3;

SET @has_new_uk := (
  SELECT COUNT(*) FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'local_port_rules' AND INDEX_NAME = 'uk_rule'
);
SET @ddl4 := IF(@has_new_uk = 0,
  'ALTER TABLE local_port_rules ADD UNIQUE KEY uk_rule (port, port_end, protocol, src_cidr)',
  'DO 0');
PREPARE stmt4 FROM @ddl4;
EXECUTE stmt4;
DEALLOCATE PREPARE stmt4;