-- mihomo 绑定可选协议过滤（JSON 数组；空 = 全部协议）

ALTER TABLE `mihomo_nodes` ADD COLUMN `protocols` LONGTEXT NULL;
