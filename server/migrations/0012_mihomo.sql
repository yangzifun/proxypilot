-- mihomo 模板与节点绑定（与 sing-box 并存，独立管理）

CREATE TABLE IF NOT EXISTS `mihomo_templates` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `name` VARCHAR(191) NOT NULL,
  `remark` LONGTEXT,
  `content` LONGTEXT,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `updated_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_mihomo_templates_name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `mihomo_nodes` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `name` VARCHAR(191) NOT NULL,
  `server_id` BIGINT NOT NULL,
  `template_id` BIGINT NOT NULL,
  `uuid_list` LONGTEXT,
  `enabled` BIGINT DEFAULT 1,
  `remark` LONGTEXT,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `updated_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_mihomo_nodes_name` (`name`),
  UNIQUE KEY `uq_mihomo_nodes_server` (`server_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
