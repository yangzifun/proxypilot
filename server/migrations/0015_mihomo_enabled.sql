-- mihomo 模板启用开关 + 绑定最近生成时间

ALTER TABLE `mihomo_templates` ADD COLUMN `enabled` BIGINT DEFAULT 1;
ALTER TABLE `mihomo_nodes` ADD COLUMN `last_preview_at` BIGINT DEFAULT 0;
