-- 0062: 根超管身份落库（支持修改默认超管用户名）。
--
-- 背景：
--   * 0061 引入 admin_users.is_super，但 root 仍只由 PP_SUPER_ADMIN 用户名决定；
--   * 现在 root 用户名也允许在面板修改，必须把“根超管”身份持久化为 is_root，
--     不再依赖用户名。
--
-- 规则：
--   * 给现有默认超管（当前部署 username='admin'）补 is_root=1、is_super=1；
--   * 无 is_root 行的旧库由启动时 EnsureRootAdmin 按 PP_SUPER_ADMIN 兜底补一行；
--   * 一旦 DB 存在 is_root 行，旧用户名不再自动成为 root（只认 DB 标记）。
ALTER TABLE `admin_users`
  ADD COLUMN `is_root` TINYINT NOT NULL DEFAULT 0 COMMENT '1=根超管（PP_SUPER_ADMIN 初始账号，可改名但身份保留）';

UPDATE `admin_users`
   SET `is_root` = 1, `is_super` = 1
 WHERE `username` = 'admin';