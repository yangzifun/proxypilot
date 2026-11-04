-- mihomo 服务端模式：模板类型/服务端参数 + 绑定部署字段

ALTER TABLE `mihomo_templates` ADD COLUMN `mode` VARCHAR(16) DEFAULT 'subscription';
ALTER TABLE `mihomo_templates` ADD COLUMN `server_json` LONGTEXT NULL;

ALTER TABLE `mihomo_nodes` ADD COLUMN `port_base` BIGINT DEFAULT 0;
ALTER TABLE `mihomo_nodes` ADD COLUMN `domain` VARCHAR(255) NULL;
ALTER TABLE `mihomo_nodes` ADD COLUMN `config_path` VARCHAR(255) DEFAULT '/etc/mihomo/config.yaml';
ALTER TABLE `mihomo_nodes` ADD COLUMN `service` VARCHAR(64) DEFAULT 'mihomo';
ALTER TABLE `mihomo_nodes` ADD COLUMN `last_apply_at` BIGINT DEFAULT 0;
ALTER TABLE `mihomo_nodes` ADD COLUMN `last_render_hash` VARCHAR(64) NULL;
