-- 0003：Worker 的 servers 表有 link_mode（V9 起仅 agent），迁移时漏建
ALTER TABLE `servers` ADD COLUMN `link_mode` VARCHAR(32) DEFAULT 'agent';
