-- 0058: 面板协议能力（private + MASQUE 切换）
--
-- 背景：节点需要声明支持哪些数据面协议，客户端连接时按节点启用的协议选择。
-- 旧数据安全默认：仅启用私有协议（private），MASQUE 关闭，升级后行为不变。
--
-- 兼容性：
--   * vpn_nodes 显式列 + 默认值，不改动既有列；
--   * vpn_sessions.protocol 记录本次连接使用的协议，旧会话默认 private；
--   * API 在列缺失（旧库未迁移）时按「仅私有协议」兜底，但正式环境应执行本迁移。

ALTER TABLE `vpn_nodes`
  ADD COLUMN `private_enabled` TINYINT NOT NULL DEFAULT 1 COMMENT '私有协议（自研 QUIC 数据面）是否启用';

ALTER TABLE `vpn_nodes`
  ADD COLUMN `masque_enabled` TINYINT NOT NULL DEFAULT 0 COMMENT 'MASQUE（HTTP/3 CONNECT-UDP）是否启用';

ALTER TABLE `vpn_nodes`
  ADD COLUMN `masque_path` VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'MASQUE 路径，空按 /masque';

ALTER TABLE `vpn_nodes`
  ADD COLUMN `masque_port` INT NOT NULL DEFAULT 0 COMMENT 'MASQUE 端口，0=沿用 listen_port';

ALTER TABLE `vpn_nodes`
  ADD COLUMN `masque_auth` VARCHAR(32) NOT NULL DEFAULT '' COMMENT 'MASQUE 鉴权：空/none=无额外鉴权，bearer=Authorization: Bearer <token>';

ALTER TABLE `vpn_nodes`
  ADD COLUMN `masque_state` VARCHAR(16) NOT NULL DEFAULT 'disabled' COMMENT 'disabled|pending|ready|error';

ALTER TABLE `vpn_sessions`
  ADD COLUMN `protocol` VARCHAR(16) NOT NULL DEFAULT 'private' COMMENT 'private|masque';

-- 历史行兜底：默认值已覆盖，这里再防御外部工具写入的 NULL/空值
UPDATE `vpn_nodes` SET `private_enabled` = 1 WHERE `private_enabled` NOT IN (0, 1);
UPDATE `vpn_nodes` SET `masque_enabled` = 0 WHERE `masque_enabled` NOT IN (0, 1);
UPDATE `vpn_nodes` SET `masque_state` = 'disabled' WHERE `masque_state` = '';
UPDATE `vpn_sessions` SET `protocol` = 'private' WHERE `protocol` = '';