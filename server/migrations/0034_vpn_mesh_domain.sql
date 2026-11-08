-- 0034: 组网中心域名（服务端 ↔ 节点端自建组网）
ALTER TABLE vpn_settings ADD COLUMN server_mesh_domain VARCHAR(255) NOT NULL DEFAULT 'vpncenter.yzfn.top' AFTER server_mesh_ip4;
