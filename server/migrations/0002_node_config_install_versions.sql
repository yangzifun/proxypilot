-- 0002：Agent 下发时需要知道模板指定的引擎版本（sing-box / xray）
ALTER TABLE `node_configs` ADD COLUMN `install_versions` LONGTEXT NULL;
