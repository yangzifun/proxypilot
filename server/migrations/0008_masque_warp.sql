-- 0008: MASQUE over Cloudflare WARP (server-side registration, per-UUID profile)

CREATE TABLE IF NOT EXISTS `masque_warp_settings` (
  `id` TINYINT UNSIGNED NOT NULL DEFAULT 1,
  `enabled` TINYINT NOT NULL DEFAULT 0,
  `sni` VARCHAR(255) NOT NULL DEFAULT 'zt-masque.cloudflareclient.com',
  `server_ips` LONGTEXT NOT NULL,
  `ports` LONGTEXT NOT NULL,
  `mtu` INT NOT NULL DEFAULT 1280,
  `dns` LONGTEXT NOT NULL,
  `updated_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

INSERT INTO `masque_warp_settings`
  (`id`, `enabled`, `sni`, `server_ips`, `ports`, `mtu`, `dns`, `updated_at`)
SELECT 1, 0, 'zt-masque.cloudflareclient.com',
  '["162.159.198.1","162.159.198.2","162.159.199.1","162.159.199.2","2606:4700:103::1","2606:4700:103::2","2606:4700:104::1","2606:4700:104::2"]',
  '[443,500,1701,4500,4443,8443,8095]',
  1280,
  '["1.1.1.1","2606:4700:4700::1111"]',
  UNIX_TIMESTAMP(NOW(3))*1000
WHERE NOT EXISTS (SELECT 1 FROM `masque_warp_settings` WHERE `id` = 1);

CREATE TABLE IF NOT EXISTS `masque_warp_profiles` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `uuid` VARCHAR(36) NOT NULL,
  `device_id` VARCHAR(128) NOT NULL,
  `token_enc` TEXT NOT NULL,
  `private_key_enc` TEXT NOT NULL,
  `public_key` VARCHAR(64) NOT NULL,
  `peer_public_key` VARCHAR(64) NOT NULL,
  `client_ipv4` VARCHAR(64) NOT NULL DEFAULT '',
  `client_ipv6` VARCHAR(128) NOT NULL DEFAULT '',
  `status` VARCHAR(16) NOT NULL DEFAULT 'active',
  `last_error` VARCHAR(500) NOT NULL DEFAULT '',
  `authorized_by` VARCHAR(64) NOT NULL DEFAULT '',
  `created_at` BIGINT NOT NULL DEFAULT 0,
  `last_refresh_at` BIGINT NOT NULL DEFAULT 0,
  `revoked_at` BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_masque_warp_uuid` (`uuid`),
  KEY `idx_masque_warp_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
