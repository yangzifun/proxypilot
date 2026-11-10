-- 用户中心支持密码登录。
--
-- uuid_info 就是「用户」实体：一个邮箱绑定一个 UUID（email_norm 唯一）。
-- 之前只支持邮箱验证码登录；现在为每个用户生成一个初始密码，
-- 管理员新建用户时随机生成并一次性展示，用户可在用户中心自行修改。

ALTER TABLE `uuid_info`
  ADD COLUMN `password_hash` VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'pbkdf2_sha256$...' AFTER `email_norm`,
  ADD COLUMN `password_updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '最近改密时间（毫秒）' AFTER `password_hash`,
  ADD COLUMN `password_must_change` TINYINT NOT NULL DEFAULT 0 COMMENT '1=首次登录需改密' AFTER `password_updated_at`;