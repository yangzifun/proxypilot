-- 0033: VPN 节点域名（用于证书与客户端连接）
ALTER TABLE vpn_nodes ADD COLUMN domain VARCHAR(255) NOT NULL DEFAULT '' AFTER name;
