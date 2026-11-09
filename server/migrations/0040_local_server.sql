-- 本机（控制服务器）独立存储：不再与节点服务器共用 servers 表
CREATE TABLE IF NOT EXISTS local_server (
  id        TINYINT UNSIGNED NOT NULL PRIMARY KEY,
  name      VARCHAR(191)  NOT NULL DEFAULT '本机（控制服务器）',
  host      VARCHAR(255)  NOT NULL DEFAULT '127.0.0.1',
  status    VARCHAR(32)   NOT NULL DEFAULT 'online',
  ssh_guard TINYINT       NOT NULL DEFAULT 0,
  os_info   TEXT          NULL,
  metrics   LONGTEXT      NULL,
  ip        VARCHAR(64)   NOT NULL DEFAULT '',
  ip4       VARCHAR(64)   NOT NULL DEFAULT '',
  ip6       VARCHAR(191)  NOT NULL DEFAULT '',
  checked_at BIGINT       NOT NULL DEFAULT 0,
  updated_at BIGINT       NOT NULL DEFAULT 0
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT INTO local_server (id, name, host)
SELECT 1, '本机（控制服务器）', '127.0.0.1'
  FROM DUAL
 WHERE NOT EXISTS (SELECT 1 FROM local_server WHERE id = 1);

-- 从旧的 servers 记录迁移一次（密钥/开关），随后删除该行
UPDATE local_server ls
   JOIN servers s ON s.link_mode = 'local'
   SET ls.ssh_guard = COALESCE(s.ssh_guard, 0),
       ls.metrics   = s.last_metrics,
       ls.os_info   = s.os_info,
       ls.ip        = COALESCE(s.last_ip, ''),
       ls.ip4       = COALESCE(s.last_ip4, ''),
       ls.ip6       = COALESCE(s.last_ip6, ''),
       ls.checked_at = COALESCE(s.last_check_at, 0),
       ls.updated_at = COALESCE(s.updated_at, 0)
 WHERE ls.id = 1;

DELETE FROM servers WHERE link_mode = 'local';