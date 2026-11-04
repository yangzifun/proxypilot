-- mihomo 模板版本历史

CREATE TABLE IF NOT EXISTS `mihomo_template_versions` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `template_id` BIGINT UNSIGNED NOT NULL,
  `name` VARCHAR(191) NOT NULL,
  `remark` LONGTEXT,
  `content` LONGTEXT,
  `actor` VARCHAR(191),
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  PRIMARY KEY (`id`),
  KEY `idx_mihomo_template_versions_tpl` (`template_id`, `id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
