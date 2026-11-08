-- 0035: 自建组网成员（所有服务器均可加入，不要求绑定 VPN 节点）
CREATE TABLE IF NOT EXISTS vpn_mesh_nodes (
  server_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  mesh_ip4 VARCHAR(64) NOT NULL DEFAULT '',
  domain VARCHAR(255) NOT NULL DEFAULT '',
  state VARCHAR(16) NOT NULL DEFAULT 'down',
  transport VARCHAR(16) NOT NULL DEFAULT 'masque',
  rtt_ms INT NOT NULL DEFAULT 0,
  last_seen BIGINT NOT NULL DEFAULT 0,
  updated_at BIGINT NOT NULL DEFAULT 0
);
