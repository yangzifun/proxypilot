-- 0061: 管理账号支持多个超级管理员。
--
-- 说明：
--   * Admin 账号增加 is_super；PP_SUPER_ADMIN（root）始终视为超管，不依赖该列。
--   * 不迁移默认数据：现有账号 is_super 默认 0；root 由配置兜底。
--   * 需要至少保留 root，root 不可降级/删除；普通超管可由绑定 API 管理。
ALTER TABLE `admin_users`
  ADD COLUMN `is_super` TINYINT NOT NULL DEFAULT 0 COMMENT '1=超级管理员；root(PP_SUPER_ADMIN) 始终为超管';