-- 0057: Clash/Mihomo 订阅模板编辑器（草稿 / 发布内容 / 版本）
--
-- 背景（task-20）：
--   config_templates 以前只存 URL，模板内容在 R2；UI 无法编辑内容，且运行时每次
--   请求实时拉 URL，S3/CF 抖动直接影响所有订阅。现在增加：
--     · draft_content / published_content：DB 草稿与已发布内容；
--     · *_sha256 / *_at / *_by / content_version：乐观锁与审计信息；
--     · config_template_versions：保存/发布/回滚/导入的操作历史。
--   发布仍写回原 R2 URL（保持旧客户端兼容），运行时优先 published_content。
--
-- 迁移说明：迁移执行器只执行未应用的版本，这里的 ALTER 不需要 IF NOT EXISTS；
-- 版本表用 CREATE TABLE IF NOT EXISTS，可安全重复执行。

ALTER TABLE `config_templates`
  ADD COLUMN `draft_content` MEDIUMTEXT NULL COMMENT '编辑器草稿 YAML',
  ADD COLUMN `published_content` MEDIUMTEXT NULL COMMENT '已发布 YAML（运行时优先使用）',
  ADD COLUMN `draft_sha256` CHAR(64) NOT NULL DEFAULT '' COMMENT '草稿 SHA-256',
  ADD COLUMN `published_sha256` CHAR(64) NOT NULL DEFAULT '' COMMENT '已发布 SHA-256',
  ADD COLUMN `draft_updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '草稿更新时间（毫秒）',
  ADD COLUMN `draft_updated_by` VARCHAR(191) NOT NULL DEFAULT '' COMMENT '草稿更新人',
  ADD COLUMN `published_at` BIGINT NOT NULL DEFAULT 0 COMMENT '发布时间（毫秒）',
  ADD COLUMN `published_by` VARCHAR(191) NOT NULL DEFAULT '' COMMENT '发布人',
  ADD COLUMN `content_version` BIGINT NOT NULL DEFAULT 0 COMMENT '已发布内容版本号';

CREATE TABLE IF NOT EXISTS `config_template_versions` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `template_id` BIGINT UNSIGNED NOT NULL,
  `version` BIGINT NOT NULL DEFAULT 0,
  `content` MEDIUMTEXT NOT NULL,
  `sha256` CHAR(64) NOT NULL DEFAULT '',
  `action` VARCHAR(16) NOT NULL DEFAULT '',
  `actor` VARCHAR(191) NOT NULL DEFAULT '',
  `created_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_ctv_tpl` (`template_id`, `id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;