-- 0050: 密钥/私钥静态加密（安全审计 中-03 / 中-06）
--
-- vpn_certificates 增加 key_pem_enc：TLS 私钥用「面板外部 KEK」加密后存这里，
-- key_pem 仅作为兼容旧版/回滚时的明文列（启动迁移完成后会被清空，回滚模式恢复）。
--
-- 说明：app_settings.jwt_secret / agent_key_secret、servers.agent_token_enc 的重加密
-- 需要 KEK 参与，SQL 里拿不到 KEK，因此由服务启动时的 MigrateSecretsAtRest 幂等完成，
-- 不需要也不应该写进 SQL 迁移。本迁移只做列存在性变更（幂等：列已存在则 SELECT 1）。
--
-- 回滚：设置 PP_SECRET_AT_REST=decrypt 启动一次（把 enc:v2: 解密回旧格式），
-- 然后回滚二进制即可；key_pem_enc 列保留不影响旧代码。

SET @ddl := (SELECT IF(
  (SELECT COUNT(*) FROM information_schema.columns
     WHERE table_schema = DATABASE() AND table_name = 'vpn_certificates' AND column_name = 'key_pem_enc') = 0,
  'ALTER TABLE vpn_certificates ADD COLUMN key_pem_enc MEDIUMTEXT NULL',
  'SELECT 1'));

PREPARE stmt FROM @ddl;

EXECUTE stmt;

DEALLOCATE PREPARE stmt;