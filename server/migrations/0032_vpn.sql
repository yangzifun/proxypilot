-- 0032: MASQUE-VPN 集成（节点 / 设备 / 授权绑定 / 会话 / 证书 / 管理网设置）
--
-- 设计要点：
--   * 节点数据同步默认走 WARP（management_transport=warp），自建 MASQUE 管理网作为备用；
--   * 节点必须绑定用户（vpn_node_grants）且用户有 VPN 资格（vpn_user_access）才可连接；
--   * 会话按设备/节点/授权记账，供配额与审计使用；
--   * 证书私钥只在节点本地，本表只存证书链与指纹。

CREATE TABLE IF NOT EXISTS `vpn_settings` (
  `id` TINYINT UNSIGNED NOT NULL DEFAULT 1,
  `enabled` TINYINT NOT NULL DEFAULT 0,
  `management_transport` VARCHAR(16) NOT NULL DEFAULT 'warp' COMMENT 'warp|masque|auto',
  `masque_fallback` TINYINT NOT NULL DEFAULT 1,
  `server_mesh_ip4` VARCHAR(64) NOT NULL DEFAULT '10.99.0.1',
  `mesh_cidr4` VARCHAR(64) NOT NULL DEFAULT '10.99.0.0/16',
  `client_cidr4` VARCHAR(64) NOT NULL DEFAULT '10.88.0.0/16',
  `mtu` INT NOT NULL DEFAULT 1280,
  `token_ttl_seconds` INT NOT NULL DEFAULT 600,
  `updated_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

INSERT INTO `vpn_settings` (`id`, `enabled`, `management_transport`, `masque_fallback`,
  `server_mesh_ip4`, `mesh_cidr4`, `client_cidr4`, `mtu`, `token_ttl_seconds`, `updated_at`)
SELECT 1, 0, 'warp', 1, '10.99.0.1', '10.99.0.0/16', '10.88.0.0/16', 1280, 600,
  UNIX_TIMESTAMP(NOW(3))*1000
WHERE NOT EXISTS (SELECT 1 FROM `vpn_settings` WHERE `id` = 1);

CREATE TABLE IF NOT EXISTS `vpn_nodes` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `server_id` BIGINT UNSIGNED NOT NULL COMMENT 'servers.id',
  `name` VARCHAR(128) NOT NULL DEFAULT '',
  `region` VARCHAR(64) NOT NULL DEFAULT '',
  `endpoint` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '客户端接入 host:port(UDP)',
  `mesh_ip4` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '管理网地址，如 10.99.0.11',
  `client_cidr4` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '本节点客户端子网，如 10.88.7.0/24',
  `node_pubkey` VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Ed25519 身份公钥 base64',
  `state` VARCHAR(16) NOT NULL DEFAULT 'pending' COMMENT 'pending|active|disabled',
  `listen_port` INT NOT NULL DEFAULT 443,
  `capacity` INT NOT NULL DEFAULT 500,
  `cert_mode` VARCHAR(16) NOT NULL DEFAULT 'internal_ca' COMMENT 'acme|internal_ca|selfsigned',
  `version` VARCHAR(64) NOT NULL DEFAULT '',
  `last_seen` BIGINT NOT NULL DEFAULT 0,
  `created_at` BIGINT NOT NULL DEFAULT 0,
  `updated_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_vpn_nodes_server` (`server_id`),
  KEY `idx_vpn_nodes_state` (`state`),
  KEY `idx_vpn_nodes_mesh` (`mesh_ip4`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `vpn_devices` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `uuid` VARCHAR(64) NOT NULL COMMENT 'uuid_info.uuid',
  `name` VARCHAR(128) NOT NULL DEFAULT '',
  `platform` VARCHAR(32) NOT NULL DEFAULT '',
  `device_pubkey` VARCHAR(128) NOT NULL DEFAULT '',
  `ip4` VARCHAR(64) NOT NULL DEFAULT '',
  `ip6` VARCHAR(128) NOT NULL DEFAULT '',
  `state` VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT 'active|revoked',
  `last_seen` BIGINT NOT NULL DEFAULT 0,
  `created_at` BIGINT NOT NULL DEFAULT 0,
  `revoked_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_vpn_devices_uuid` (`uuid`, `state`),
  UNIQUE KEY `uk_vpn_devices_ip4` (`ip4`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `vpn_device_codes` (
  `device_code` VARCHAR(64) NOT NULL,
  `user_code` VARCHAR(16) NOT NULL,
  `uuid` VARCHAR(64) NOT NULL DEFAULT '',
  `approved` TINYINT NOT NULL DEFAULT 0,
  `expires_at` BIGINT NOT NULL DEFAULT 0,
  `created_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`device_code`),
  UNIQUE KEY `uk_vpn_device_codes_user` (`user_code`),
  KEY `idx_vpn_device_codes_exp` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `vpn_user_access` (
  `uuid` VARCHAR(64) NOT NULL,
  `enabled` TINYINT NOT NULL DEFAULT 0,
  `expires_at` BIGINT NOT NULL DEFAULT 0 COMMENT '0=长期',
  `max_devices` INT NOT NULL DEFAULT 3,
  `quota_bytes` BIGINT NOT NULL DEFAULT 0 COMMENT '0=不限',
  `used_bytes` BIGINT NOT NULL DEFAULT 0,
  `note` VARCHAR(255) NOT NULL DEFAULT '',
  `created_by` VARCHAR(64) NOT NULL DEFAULT '',
  `created_at` BIGINT NOT NULL DEFAULT 0,
  `updated_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`uuid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `vpn_node_grants` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `node_id` BIGINT UNSIGNED NOT NULL,
  `subject_type` VARCHAR(16) NOT NULL DEFAULT 'user' COMMENT 'user|group|plan',
  `subject_id` VARCHAR(64) NOT NULL COMMENT '用户 UUID（v1）',
  `enabled` TINYINT NOT NULL DEFAULT 1,
  `expires_at` BIGINT NOT NULL DEFAULT 0 COMMENT '0=长期',
  `max_devices` INT NOT NULL DEFAULT 1,
  `quota_bytes` BIGINT NOT NULL DEFAULT 0,
  `used_bytes` BIGINT NOT NULL DEFAULT 0,
  `created_by` VARCHAR(64) NOT NULL DEFAULT '',
  `created_at` BIGINT NOT NULL DEFAULT 0,
  `revoked_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_vpn_grants_node` (`node_id`, `subject_id`),
  KEY `idx_vpn_grants_subject` (`subject_id`, `enabled`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `vpn_sessions` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `device_id` BIGINT UNSIGNED NOT NULL,
  `node_id` BIGINT UNSIGNED NOT NULL,
  `grant_id` BIGINT UNSIGNED NOT NULL DEFAULT 0,
  `ip4` VARCHAR(64) NOT NULL DEFAULT '',
  `ip6` VARCHAR(128) NOT NULL DEFAULT '',
  `client_ip` VARCHAR(64) NOT NULL DEFAULT '',
  `started_at` BIGINT NOT NULL DEFAULT 0,
  `ended_at` BIGINT NOT NULL DEFAULT 0,
  `bytes_tx` BIGINT NOT NULL DEFAULT 0,
  `bytes_rx` BIGINT NOT NULL DEFAULT 0,
  `packets` BIGINT NOT NULL DEFAULT 0,
  `close_reason` VARCHAR(32) NOT NULL DEFAULT '',
  PRIMARY KEY (`id`),
  KEY `idx_vpn_sessions_node` (`node_id`, `ended_at`),
  KEY `idx_vpn_sessions_device` (`device_id`, `started_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `vpn_node_links` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `node_id` BIGINT UNSIGNED NOT NULL,
  `transport` VARCHAR(16) NOT NULL DEFAULT 'warp' COMMENT 'warp|masque',
  `state` VARCHAR(16) NOT NULL DEFAULT 'down' COMMENT 'up|down',
  `rtt_ms` INT NOT NULL DEFAULT 0,
  `last_seen` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_vpn_links_node` (`node_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `vpn_certs` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `node_id` BIGINT UNSIGNED NOT NULL,
  `kind` VARCHAR(16) NOT NULL DEFAULT 'access' COMMENT 'access|identity|mesh',
  `mode` VARCHAR(16) NOT NULL DEFAULT 'internal_ca' COMMENT 'acme|internal_ca|selfsigned',
  `serial` VARCHAR(128) NOT NULL DEFAULT '',
  `fingerprint_sha256` VARCHAR(128) NOT NULL DEFAULT '',
  `san` TEXT,
  `not_before` BIGINT NOT NULL DEFAULT 0,
  `not_after` BIGINT NOT NULL DEFAULT 0,
  `cert_pem` MEDIUMTEXT,
  `chain_pem` MEDIUMTEXT,
  `status` VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT 'active|renewing|expired|revoked',
  `issued_at` BIGINT NOT NULL DEFAULT 0,
  `renewed_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_vpn_certs_node` (`node_id`, `kind`),
  KEY `idx_vpn_certs_status` (`status`, `not_after`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `vpn_signing_keys` (
  `kid` VARCHAR(32) NOT NULL,
  `public_key` VARCHAR(128) NOT NULL COMMENT 'Ed25519 公钥 base64',
  `private_key_enc` TEXT NOT NULL COMMENT 'AES-GCM 加密的私钥',
  `active` TINYINT NOT NULL DEFAULT 1,
  `created_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`kid`),
  KEY `idx_vpn_signing_active` (`active`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `vpn_audit` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `actor` VARCHAR(64) NOT NULL DEFAULT '',
  `action` VARCHAR(64) NOT NULL DEFAULT '',
  `target` VARCHAR(128) NOT NULL DEFAULT '',
  `detail` TEXT,
  `created_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_vpn_audit_created` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

