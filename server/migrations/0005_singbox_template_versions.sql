-- 0005：sing-box 模板的引擎版本（Worker 动态补列），protocol_options 放宽为 LONGTEXT
ALTER TABLE `singbox_templates`
  ADD COLUMN `singbox_version` VARCHAR(64) DEFAULT '',
  ADD COLUMN `xray_version` VARCHAR(64) DEFAULT '',
  MODIFY COLUMN `protocol_options` LONGTEXT;
