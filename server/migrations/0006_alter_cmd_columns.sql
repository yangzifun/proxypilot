-- 0006：D1 的 TEXT 列在 MySQL 中不能限制为 255（重启/校验命令可含较长 systemd 命令）
ALTER TABLE `node_configs`
  MODIFY COLUMN `restart_cmd` LONGTEXT,
  MODIFY COLUMN `check_cmd` LONGTEXT;

ALTER TABLE `node_templates`
  MODIFY COLUMN `restart_cmd` LONGTEXT,
  MODIFY COLUMN `check_cmd` LONGTEXT;
