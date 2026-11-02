-- =====================================================================
--  ProxyPilot · MySQL 8.0.13+ 初始化脚本（迁移自 D1 / SQLite）
--  版本：migration-0001
--  说明：
--   · 时间戳一律为 Unix 毫秒（BIGINT），与 Worker 端 Date.now() 一致
--   · 表达式默认值要求 MySQL >= 8.0.13；5.7 请把 DEFAULT (...) 改为 DEFAULT 0
--   · 已弃用表（cfips / cf_domains / edgeone_domains / auto_update_settings）不再创建
--   · configs 的 UNIQUE(uuid, config_data) 不再建立：MySQL 无法为 MEDIUMTEXT 建唯一索引，
--     去重逻辑保留在应用层（与原 Worker 的“先查重再插入”一致）
-- =====================================================================

-- ProxyPilot · MySQL 8.0.13+ schema
-- 由 SQLite(D1) schema 迁移生成；时间戳统一为 Unix 毫秒 BIGINT。
SET NAMES utf8mb4;

CREATE TABLE IF NOT EXISTS `admin_email_codes` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `username` VARCHAR(191) NOT NULL,
  `email` VARCHAR(320) NOT NULL,
  `code_hash` VARCHAR(255) NOT NULL,
  `purpose` VARCHAR(32) DEFAULT 'bind_email',
  `attempts` BIGINT DEFAULT 0,
  `expires_at` BIGINT DEFAULT 0,
  `used` BIGINT DEFAULT 0,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `admin_oidc_identities` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `username` VARCHAR(191) NOT NULL,
  `issuer` VARCHAR(255) NOT NULL,
  `sub` VARCHAR(191) NOT NULL,
  `email` VARCHAR(320),
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `last_login_at` BIGINT DEFAULT 0,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `admin_users` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `username` VARCHAR(191) NOT NULL,
  `password_hash` VARCHAR(255) NOT NULL,
  `mfa_enabled` BIGINT DEFAULT 0,
  `mfa_secret` VARCHAR(255),
  `last_mfa_login` BIGINT DEFAULT 0,
  `last_backup_login` BIGINT DEFAULT 0,
  `email` VARCHAR(320),
  `email_verified` BIGINT DEFAULT 0,
  `email_verified_at` BIGINT DEFAULT 0,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `agent_tasks` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `server_id` BIGINT NOT NULL,
  `config_id` BIGINT DEFAULT 0,
  `type` VARCHAR(32),
  `payload` LONGTEXT,
  `payload_hash` LONGTEXT,
  `status` VARCHAR(32) DEFAULT 'pending',
  `attempts` BIGINT DEFAULT 0,
  `exit_code` BIGINT DEFAULT 0,
  `output` LONGTEXT,
  `actor` VARCHAR(191),
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `sent_at` BIGINT DEFAULT 0,
  `finished_at` BIGINT DEFAULT 0,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `api_keys` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `key_name` LONGTEXT NOT NULL,
  `api_key` VARCHAR(255) NOT NULL,
  `secret_key` LONGTEXT,
  `enabled` BIGINT DEFAULT 1 NOT NULL,
  `permissions` VARCHAR(191) DEFAULT '',
  `key_hint` LONGTEXT,
  `expires_at` BIGINT DEFAULT 0,
  `created_by` LONGTEXT,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `updated_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `last_used_at` BIGINT DEFAULT 0,
  `usage_count` BIGINT DEFAULT 0,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `app_settings` (
  `key` VARCHAR(191) NOT NULL,
  `value` LONGTEXT,
  `updated_at` BIGINT,
  PRIMARY KEY (`key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `batch_job_items` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `job_id` BIGINT NOT NULL,
  `seq` BIGINT NOT NULL,
  `target` LONGTEXT,
  `target_id` BIGINT DEFAULT 0,
  `label` VARCHAR(191),
  `status` VARCHAR(32) DEFAULT 'pending',
  `message` LONGTEXT,
  `detail` LONGTEXT,
  `attempts` BIGINT DEFAULT 0,
  `started_at` BIGINT DEFAULT 0,
  `finished_at` BIGINT DEFAULT 0,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `batch_jobs` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `kind` VARCHAR(32) NOT NULL,
  `title` VARCHAR(191),
  `params` LONGTEXT,
  `status` VARCHAR(32) DEFAULT 'queued',
  `total` BIGINT DEFAULT 0,
  `done` BIGINT DEFAULT 0,
  `failed` BIGINT DEFAULT 0,
  `cursor` BIGINT DEFAULT 0,
  `runner_id` LONGTEXT,
  `locked_at` BIGINT DEFAULT 0,
  `actor` VARCHAR(191),
  `error` LONGTEXT,
  `summary` LONGTEXT,
  `created_at` BIGINT DEFAULT 0,
  `started_at` BIGINT DEFAULT 0,
  `finished_at` BIGINT DEFAULT 0,
  `updated_at` BIGINT DEFAULT 0,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `config_access_logs` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `uuid` VARCHAR(36) NOT NULL,
  `query_type` VARCHAR(32) NOT NULL,
  `client_ip` VARCHAR(191),
  `user_agent` LONGTEXT,
  `created_at` VARCHAR(191) DEFAULT (DATE_FORMAT(UTC_TIMESTAMP(),'%Y-%m-%dT%H:%i:%SZ')),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `config_templates` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `name` VARCHAR(191) NOT NULL,
  `url` VARCHAR(255) NOT NULL,
  `description` LONGTEXT,
  `is_default` BIGINT DEFAULT 0,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `updated_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `configs` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `uuid` VARCHAR(36) NOT NULL,
  `config_data` MEDIUMTEXT NOT NULL,
  `protocol` VARCHAR(32) NOT NULL,
  `remark` LONGTEXT,
  `domain_hosting` VARCHAR(191) DEFAULT 'Cloudflare' NOT NULL,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000) NOT NULL,
  `updated_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000) NOT NULL,
  `ip` VARCHAR(45),
  `domain` VARCHAR(255),
  `port` BIGINT,
  `meta` LONGTEXT,
  `singbox_node_id` BIGINT DEFAULT 0,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `ip_geo` (
  `ip` VARCHAR(45) NOT NULL,
  `country` LONGTEXT,
  `region` LONGTEXT,
  `lat` DOUBLE,
  `lon` DOUBLE,
  `updated_at` BIGINT,
  PRIMARY KEY (`ip`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `mfa_backup_codes` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `username` VARCHAR(191) NOT NULL,
  `code` LONGTEXT NOT NULL,
  `used` BIGINT DEFAULT 0,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `used_at` BIGINT DEFAULT 0,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `mtproto_clients` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `node_id` BIGINT NOT NULL,
  `uuid` VARCHAR(36),
  `email` VARCHAR(320),
  `label` VARCHAR(191),
  `secret` VARCHAR(255),
  `status` VARCHAR(32) DEFAULT 'active',
  `delivered_at` BIGINT DEFAULT 0,
  `deliver_count` BIGINT DEFAULT 0,
  `last_mail_at` BIGINT DEFAULT 0,
  `mail_count_today` BIGINT DEFAULT 0,
  `mail_day` VARCHAR(191) DEFAULT '',
  `revoked_at` BIGINT DEFAULT 0,
  `remark` LONGTEXT,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `updated_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `mtproto_nodes` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `server_id` BIGINT NOT NULL,
  `template_id` BIGINT DEFAULT 0,
  `name` VARCHAR(191) NOT NULL,
  `domain` VARCHAR(255),
  `port` BIGINT DEFAULT 0,
  `api_port` BIGINT DEFAULT 0,
  `secret_mode` VARCHAR(32) DEFAULT '',
  `engine` VARCHAR(32) DEFAULT 'mtg-multi',
  `config_path` VARCHAR(255) DEFAULT '',
  `service_name` VARCHAR(255) DEFAULT '',
  `concurrency` BIGINT DEFAULT 0,
  `prefer_ip` VARCHAR(32) DEFAULT '',
  `public_ipv4` LONGTEXT,
  `blocklist` BIGINT DEFAULT -1,
  `throttle_max` BIGINT DEFAULT -1,
  `bootstrap_secret` VARCHAR(255),
  `enabled` BIGINT DEFAULT 1,
  `last_apply_at` BIGINT DEFAULT 0,
  `last_render_hash` LONGTEXT,
  `remark` LONGTEXT,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `updated_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `host` VARCHAR(255),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `mtproto_templates` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `name` VARCHAR(191) NOT NULL,
  `remark` LONGTEXT,
  `domain` VARCHAR(255),
  `port` BIGINT DEFAULT 13709,
  `api_port` BIGINT DEFAULT 13719,
  `secret_mode` VARCHAR(32) DEFAULT 'multi',
  `concurrency` BIGINT DEFAULT 8192,
  `prefer_ip` VARCHAR(32) DEFAULT 'prefer-ipv4',
  `public_ipv4` LONGTEXT,
  `blocklist` BIGINT DEFAULT 0,
  `throttle_max` BIGINT DEFAULT 0,
  `config_path` VARCHAR(255) DEFAULT '/etc/mtg/config.toml',
  `service_name` VARCHAR(255) DEFAULT 'mtg',
  `enabled` BIGINT DEFAULT 1,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `updated_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `node_configs` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `name` VARCHAR(191) NOT NULL,
  `server_id` BIGINT NOT NULL,
  `engine` VARCHAR(32) DEFAULT 'other',
  `target_path` VARCHAR(255) NOT NULL,
  `service_name` VARCHAR(255),
  `restart_cmd` VARCHAR(255),
  `check_cmd` VARCHAR(255),
  `content` LONGTEXT,
  `backup` BIGINT DEFAULT 1,
  `enabled` BIGINT DEFAULT 1,
  `auto_push` BIGINT DEFAULT 0,
  `push_interval_min` BIGINT DEFAULT 0,
  `last_push_at` BIGINT DEFAULT 0,
  `last_push_status` LONGTEXT,
  `last_push_message` LONGTEXT,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `updated_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `version` BIGINT DEFAULT 0,
  `content_hash` LONGTEXT,
  `applied_version` BIGINT DEFAULT 0,
  `applied_at` BIGINT DEFAULT 0,
  `template_id` BIGINT DEFAULT 0,
  `uuid` VARCHAR(36),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `node_push_logs` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `config_id` BIGINT DEFAULT 0,
  `server_id` BIGINT DEFAULT 0,
  `action` VARCHAR(32),
  `status` VARCHAR(32),
  `exit_code` BIGINT DEFAULT 0,
  `message` LONGTEXT,
  `output` LONGTEXT,
  `duration_ms` BIGINT DEFAULT 0,
  `actor` VARCHAR(191),
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `node_templates` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `name` VARCHAR(191) NOT NULL,
  `engine` VARCHAR(32) DEFAULT 'other',
  `target_path` VARCHAR(255) NOT NULL,
  `service_name` VARCHAR(255),
  `restart_cmd` VARCHAR(255),
  `check_cmd` VARCHAR(255),
  `content` LONGTEXT,
  `backup` BIGINT DEFAULT 1,
  `remark` LONGTEXT,
  `enabled` BIGINT DEFAULT 1,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `updated_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `server_metrics` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `server_id` BIGINT NOT NULL,
  `status` VARCHAR(32),
  `latency_ms` BIGINT DEFAULT 0,
  `load1` DOUBLE DEFAULT 0,
  `load5` DOUBLE DEFAULT 0,
  `load15` DOUBLE DEFAULT 0,
  `cpu_percent` DOUBLE DEFAULT 0,
  `mem_percent` DOUBLE DEFAULT 0,
  `mem_used_mb` BIGINT DEFAULT 0,
  `mem_total_mb` BIGINT DEFAULT 0,
  `disk_percent` DOUBLE DEFAULT 0,
  `uptime_sec` BIGINT DEFAULT 0,
  `services` LONGTEXT,
  `raw` LONGTEXT,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `servers` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `name` VARCHAR(191) NOT NULL,
  `host` VARCHAR(255) NOT NULL,
  `port` BIGINT DEFAULT 22,
  `username` VARCHAR(191) NOT NULL,
  `auth_type` VARCHAR(32) DEFAULT 'password',
  `secret_enc` VARCHAR(255),
  `secret_hint` LONGTEXT,
  `host_key_fingerprint` LONGTEXT,
  `tags` LONGTEXT,
  `remark` LONGTEXT,
  `enabled` BIGINT DEFAULT 1,
  `last_status` VARCHAR(191) DEFAULT 'unknown',
  `last_latency_ms` BIGINT DEFAULT 0,
  `last_error` LONGTEXT,
  `last_check_at` BIGINT DEFAULT 0,
  `last_metrics` LONGTEXT,
  `os_info` LONGTEXT,
  `singbox_template_id` BIGINT DEFAULT 0,
  `singbox_domain` LONGTEXT,
  `singbox_warp` BIGINT DEFAULT -1,
  `singbox_auto_deploy` BIGINT DEFAULT 0,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `updated_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `agent_token_hash` VARCHAR(255),
  `agent_token_hint` LONGTEXT,
  `agent_token_enc` VARCHAR(255),
  `agent_version` LONGTEXT,
  `agent_last_seen` BIGINT DEFAULT 0,
  `agent_last_poll_at` BIGINT DEFAULT 0,
  `agent_next_poll_sec` BIGINT,
  `agent_poll_count` BIGINT DEFAULT 0,
  `metrics_every` BIGINT DEFAULT 1,
  `allow_exec` BIGINT DEFAULT 0,
  `last_ip` VARCHAR(45),
  `last_ip4` VARCHAR(45),
  `last_ip6` VARCHAR(45),
  `skip_hostkey_verify` BIGINT DEFAULT 0,
  `agent_key_created_at` BIGINT DEFAULT 0,
  `agent_key_used_at` BIGINT DEFAULT 0,
  `agent_platform` LONGTEXT,
  `last_notify_at` BIGINT DEFAULT 0,
  `ip_country` LONGTEXT,
  `ip_region` LONGTEXT,
  `ip_lat` DOUBLE,
  `ip_lon` DOUBLE,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `shell_sessions` (
  `server_id` BIGINT NOT NULL,
  `session` LONGTEXT NOT NULL,
  `created_at` BIGINT NOT NULL,
  `status` VARCHAR(32) DEFAULT 'waiting',
  PRIMARY KEY (`server_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `singbox_nodes` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `server_id` BIGINT NOT NULL,
  `template_id` BIGINT NOT NULL,
  `name` VARCHAR(191) NOT NULL,
  `domain` VARCHAR(255),
  `port_base` BIGINT DEFAULT 0,
  `warp_base` BIGINT DEFAULT 0,
  `sb_path` VARCHAR(255) DEFAULT '/etc/sing-box/sb.json',
  `caddy_path` VARCHAR(255) DEFAULT '/etc/sing-box/Caddyfile',
  `xray_path` VARCHAR(255) DEFAULT '/etc/sing-box/xray.json',
  `service_sb` VARCHAR(255) DEFAULT 'sing-box',
  `service_caddy` VARCHAR(255) DEFAULT 'caddy-naive',
  `service_xray` VARCHAR(255) DEFAULT 'xray',
  `uuid_list` LONGTEXT,
  `warp_enabled` BIGINT DEFAULT -1,
  `enabled` BIGINT DEFAULT 1,
  `last_apply_at` BIGINT DEFAULT 0,
  `last_render_hash` LONGTEXT,
  `remark` LONGTEXT,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `updated_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `last_link_hash` LONGTEXT,
  `cdn_domain` LONGTEXT,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `singbox_templates` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `name` VARCHAR(191) NOT NULL,
  `remark` LONGTEXT,
  `users_json` LONGTEXT,
  `ss_method` VARCHAR(191) DEFAULT '2022-blake3-aes-256-gcm',
  `ss_server_key` VARCHAR(255),
  `reality_private_key` VARCHAR(255),
  `reality_public_key` VARCHAR(255),
  `reality_short_id` VARCHAR(255),
  `reality_dest` VARCHAR(191) DEFAULT 'www.microsoft.com:443',
  `reality_sni` VARCHAR(191) DEFAULT 'www.microsoft.com',
  `ws_sni` VARCHAR(191) DEFAULT 'www.bing.com',
  `disguise_url` VARCHAR(191) DEFAULT 'https://www.bing.com',
  `cert_path` VARCHAR(255) DEFAULT '/etc/sing-box/cert.crt',
  `key_path` VARCHAR(255) DEFAULT '/etc/sing-box/private.key',
  `xhttp_path` VARCHAR(191) DEFAULT '/xhttp-auto',
  `port_base` BIGINT DEFAULT 13700,
  `warp_base` BIGINT DEFAULT 13800,
  `enable_ss` BIGINT DEFAULT 1,
  `enable_anytls` BIGINT DEFAULT 1,
  `enable_hy2` BIGINT DEFAULT 1,
  `enable_reality` BIGINT DEFAULT 1,
  `enable_vlws` BIGINT DEFAULT 1,
  `enable_vmess` BIGINT DEFAULT 1,
  `enable_xhttp` BIGINT DEFAULT 1,
  `warp_enabled` BIGINT DEFAULT 0,
  `warp_private_key` VARCHAR(255),
  `warp_address` LONGTEXT,
  `warp_peer_public_key` VARCHAR(255),
  `enabled` BIGINT DEFAULT 1,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `updated_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `enable_singbox` BIGINT DEFAULT 1,
  `enable_xray` BIGINT DEFAULT 0,
  `warp_engines` VARCHAR(191) DEFAULT 'singbox',
  `enable_masque` BIGINT DEFAULT 0,
  `masque_json` LONGTEXT,
  `masque_path` VARCHAR(191) DEFAULT '/etc/sing-box/masque.json',
  `service_masque` VARCHAR(191) DEFAULT 'masque',
  `protocol_options` VARCHAR(191) DEFAULT '{}',
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `uuid_info` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `uuid` VARCHAR(36) NOT NULL,
  `owner` VARCHAR(191) NOT NULL,
  `email` VARCHAR(320) NOT NULL,
  `contact_info` LONGTEXT,
  `status` VARCHAR(32) DEFAULT 'active',
  `remark` LONGTEXT,
  `last_accessed` BIGINT DEFAULT 0,
  `access_count` BIGINT DEFAULT 0,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `updated_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE IF NOT EXISTS `uuid_sub_sources` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `uuid` VARCHAR(36) NOT NULL,
  `url` VARCHAR(255) NOT NULL,
  `remark` LONGTEXT,
  `enabled` BIGINT DEFAULT 1,
  `created_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  `updated_at` BIGINT DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

-- ============ 唯一约束 ============
ALTER TABLE `admin_users` ADD UNIQUE KEY `uq_admin_users_username` (`username`);
ALTER TABLE `api_keys` ADD UNIQUE KEY `uq_api_keys_api_key` (`api_key`);
ALTER TABLE `config_templates` ADD UNIQUE KEY `uq_config_templates_name` (`name`);
ALTER TABLE `mtproto_nodes` ADD UNIQUE KEY `uq_mtproto_nodes_server_id` (`server_id`);
ALTER TABLE `mtproto_nodes` ADD UNIQUE KEY `uq_mtproto_nodes_name` (`name`);
ALTER TABLE `mtproto_templates` ADD UNIQUE KEY `uq_mtproto_templates_name` (`name`);
ALTER TABLE `node_configs` ADD UNIQUE KEY `uq_node_configs_name` (`name`);
ALTER TABLE `node_templates` ADD UNIQUE KEY `uq_node_templates_name` (`name`);
ALTER TABLE `servers` ADD UNIQUE KEY `uq_servers_name` (`name`);
ALTER TABLE `singbox_nodes` ADD UNIQUE KEY `uq_singbox_nodes_server_id` (`server_id`);
ALTER TABLE `singbox_nodes` ADD UNIQUE KEY `uq_singbox_nodes_name` (`name`);
ALTER TABLE `singbox_templates` ADD UNIQUE KEY `uq_singbox_templates_name` (`name`);
ALTER TABLE `uuid_info` ADD UNIQUE KEY `uq_uuid_info_uuid` (`uuid`);

-- 大小写不敏感的邮箱唯一（MySQL 无部分索引：用生成列 + 唯一索引）
-- 注意必须 NULLIF(...,'')：SQLite 的部分索引是 WHERE email <> ''，空邮箱不参与唯一；
-- 若直接 LOWER(email)，空串会互相冲突，导致“第二个不填邮箱的分组”插入失败。
ALTER TABLE `admin_users` ADD COLUMN `email_norm` VARCHAR(320) GENERATED ALWAYS AS (NULLIF(LOWER(`email`), '')) STORED, ADD UNIQUE KEY `uq_admin_users_email` (`email_norm`);
ALTER TABLE `uuid_info` ADD COLUMN `email_norm` VARCHAR(320) GENERATED ALWAYS AS (NULLIF(LOWER(`email`), '')) STORED, ADD UNIQUE KEY `uq_uuid_info_email` (`email_norm`);

-- ============ 索引 ============
ALTER TABLE `configs` ADD KEY `idx_configs_uuid` (`uuid`);
ALTER TABLE `configs` ADD KEY `idx_configs_protocol` (`protocol`);
ALTER TABLE `configs` ADD KEY `idx_configs_domain_hosting` (`domain_hosting`);
ALTER TABLE `uuid_info` ADD KEY `idx_uuid_info_owner` (`owner`);
ALTER TABLE `uuid_info` ADD KEY `idx_uuid_info_email` (`email`);
ALTER TABLE `uuid_info` ADD KEY `idx_uuid_info_status` (`status`);
ALTER TABLE `uuid_info` ADD KEY `idx_uuid_info_created` (`created_at`);
ALTER TABLE `uuid_info` ADD KEY `idx_uuid_info_last_accessed` (`last_accessed`);
ALTER TABLE `config_access_logs` ADD KEY `idx_access_logs_uuid` (`uuid`);
ALTER TABLE `config_access_logs` ADD KEY `idx_access_logs_date` (`created_at`);
ALTER TABLE `config_access_logs` ADD KEY `idx_access_logs_type` (`query_type`);
ALTER TABLE `config_access_logs` ADD KEY `idx_access_logs_client_ip` (`client_ip`);
ALTER TABLE `config_access_logs` ADD KEY `idx_access_logs_uuid_date` (`uuid`, `created_at`);
ALTER TABLE `mfa_backup_codes` ADD KEY `idx_mfa_backup_codes_user` (`username`);
ALTER TABLE `mfa_backup_codes` ADD KEY `idx_mfa_backup_codes_used` (`used`);
ALTER TABLE `api_keys` ADD KEY `idx_api_keys_enabled` (`enabled`);
ALTER TABLE `api_keys` ADD KEY `idx_api_keys_created` (`created_at`);
ALTER TABLE `config_templates` ADD KEY `idx_config_templates_is_default` (`is_default`);
ALTER TABLE `config_templates` ADD KEY `idx_config_templates_created` (`created_at`);
ALTER TABLE `uuid_sub_sources` ADD KEY `idx_uuid_sub_sources_uuid` (`uuid`);
ALTER TABLE `uuid_sub_sources` ADD KEY `idx_uuid_sub_sources_enabled` (`enabled`);
ALTER TABLE `admin_oidc_identities` ADD UNIQUE KEY `idx_admin_oidc_identity` (`issuer`, `sub`);
ALTER TABLE `admin_oidc_identities` ADD KEY `idx_admin_oidc_identity_user` (`username`);
ALTER TABLE `admin_email_codes` ADD KEY `idx_admin_email_codes_lookup` (`email`, `purpose`, `used`);
ALTER TABLE `admin_email_codes` ADD KEY `idx_admin_email_codes_user` (`username`);
ALTER TABLE `servers` ADD KEY `idx_servers_enabled` (`enabled`);
ALTER TABLE `server_metrics` ADD KEY `idx_server_metrics_server` (`server_id`, `created_at`);
ALTER TABLE `node_configs` ADD KEY `idx_node_configs_server` (`server_id`);
ALTER TABLE `node_push_logs` ADD KEY `idx_node_push_logs_created` (`created_at`);
ALTER TABLE `node_push_logs` ADD KEY `idx_node_push_logs_server` (`server_id`);
ALTER TABLE `agent_tasks` ADD KEY `idx_agent_tasks_pending` (`server_id`, `status`);
ALTER TABLE `agent_tasks` ADD KEY `idx_agent_tasks_created` (`created_at`);
ALTER TABLE `mtproto_clients` ADD KEY `idx_mtproto_clients_uuid` (`uuid`);
ALTER TABLE `mtproto_clients` ADD KEY `idx_mtproto_clients_node` (`node_id`, `status`);
ALTER TABLE `mtproto_nodes` ADD KEY `idx_mtproto_nodes_template` (`template_id`);
ALTER TABLE `configs` ADD KEY `idx_configs_singbox_node` (`singbox_node_id`);
ALTER TABLE `server_metrics` ADD KEY `idx_server_metrics_server_id` (`server_id`, `id`);
ALTER TABLE `batch_jobs` ADD KEY `idx_batch_jobs_status` (`status`, `id`);
ALTER TABLE `batch_jobs` ADD KEY `idx_batch_jobs_created` (`created_at`);
ALTER TABLE `batch_job_items` ADD KEY `idx_batch_items_job` (`job_id`, `seq`);

-- =====================================================================
--  初始数据
-- =====================================================================
INSERT INTO `admin_users` (`username`, `password_hash`, `mfa_enabled`, `created_at`)
VALUES ('admin', '5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8', 0,
        (UNIX_TIMESTAMP(NOW(3))*1000))
ON DUPLICATE KEY UPDATE `username` = `username`;

INSERT INTO `app_settings` (`key`, `value`, `updated_at`) VALUES
  ('uuid_reconcile_interval_min', '30', (UNIX_TIMESTAMP(NOW(3))*1000)),
  ('uuid_reconcile_last_at', '0', (UNIX_TIMESTAMP(NOW(3))*1000)),
  ('batch_cleanup_at', '0', (UNIX_TIMESTAMP(NOW(3))*1000)),
  ('notify_enabled', '0', (UNIX_TIMESTAMP(NOW(3))*1000))
ON DUPLICATE KEY UPDATE `key` = `key`;
