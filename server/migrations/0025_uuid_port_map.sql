-- 0025_uuid_port_map: 方案A（按用户拆入站）端口分配表
-- 每个 (server_id, uuid) 分配一个独立端口，用于连接级在线识别与订阅渲染。
CREATE TABLE IF NOT EXISTS uuid_port_map (
  server_id BIGINT NOT NULL,
  uuid VARCHAR(36) NOT NULL,
  port INT NOT NULL,
  protocol VARCHAR(32) NOT NULL DEFAULT '',
  created_at BIGINT NOT NULL DEFAULT (UNIX_TIMESTAMP(NOW(3)) * 1000),
  updated_at BIGINT NOT NULL DEFAULT (UNIX_TIMESTAMP(NOW(3)) * 1000),
  PRIMARY KEY (server_id, uuid, protocol),
  UNIQUE KEY uk_port (server_id, port),
  KEY idx_uuid (uuid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 端口段配置：默认 20000 起，每节点最多 400 个
INSERT INTO app_settings (`key`, `value`)
VALUES ('singbox_per_user_enabled', '0'), ('singbox_port_base', '20000')
ON DUPLICATE KEY UPDATE `value` = `value`;
