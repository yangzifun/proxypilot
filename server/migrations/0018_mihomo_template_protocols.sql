-- mihomo 模板默认协议（绑定未指定时生效），与 sing-box 模板协议开关对齐

ALTER TABLE `mihomo_templates` ADD COLUMN `protocols` LONGTEXT NULL;
