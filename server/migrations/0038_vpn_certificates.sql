-- 0038: 平台签发的证书（ACME 通配证书，供组网中心与各节点 VPN 复用）
CREATE TABLE IF NOT EXISTS vpn_certificates (
  id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  domains VARCHAR(512) NOT NULL DEFAULT '',
  source VARCHAR(32) NOT NULL DEFAULT 'acme', -- acme | internal | selfsigned
  provider VARCHAR(32) NOT NULL DEFAULT 'cloudflare',
  cert_pem LONGTEXT NOT NULL,
  key_pem LONGTEXT NOT NULL,
  chain_pem LONGTEXT NOT NULL,
  fingerprint VARCHAR(128) NOT NULL DEFAULT '',
  not_before BIGINT NOT NULL DEFAULT 0,
  not_after BIGINT NOT NULL DEFAULT 0,
  status VARCHAR(32) NOT NULL DEFAULT 'valid',
  created_at BIGINT NOT NULL DEFAULT 0,
  updated_at BIGINT NOT NULL DEFAULT 0
);
