-- 0053: Agent 鉴权「临时按来源 IP 放行」开关（令牌轮换事故自愈用）
--
-- 背景：令牌轮换时 4 台节点的 /etc/proxypilot-agent.conf 里被写入了重复令牌行
-- （PROXYPILOT_TOKEN 与 TOKEN 各一行，Agent 解析是「后出现的覆盖前面的」），
-- 实际生效的是旧令牌；旧令牌明文已不可复原，面板按新令牌哈希查不到 → 401 → 节点掉线。
-- 面板无法 SSH 到节点，因此需要一个可自动恢复、用完即废止的临时通道：
--
--   agent_auth_grace=1 时，agentAuthenticate 在令牌哈希匹配失败后，若请求来源 IP
--   命中该服务器的 last_ip4 / last_ip / last_mesh_ip4，则放行本次请求，并给该节点
--   排一条「重写 conf 令牌 + 重启 Agent」的自愈任务；该节点用正常令牌认证成功后，
--   代码会自动把 agent_auth_grace 置回 0。
--
-- 默认 0（关闭）：正常节点完全不受影响；这是运维一次性开关，只对显式置 1 的机器生效。
-- 幂等：列已存在时执行 DO 0，可安全重放。
SET @ddl := (
  SELECT IF(
    EXISTS(
      SELECT 1 FROM information_schema.COLUMNS
       WHERE TABLE_SCHEMA = DATABASE()
         AND TABLE_NAME = 'servers'
         AND COLUMN_NAME = 'agent_auth_grace'
    ),
    'DO 0',
    'ALTER TABLE `servers` ADD COLUMN `agent_auth_grace` TINYINT NOT NULL DEFAULT 0 COMMENT ''1=允许按来源 IP 临时放行一次令牌轮换事故自愈'''
  )
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;