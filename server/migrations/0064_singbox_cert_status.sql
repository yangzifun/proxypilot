-- 0064: sing-box/节点证书下发状态回采。
--
-- 证书任务回执里包含 singbox_sha256 / 证书指纹；面板把结果落到 servers，
-- 供 /api/vpn/cert/nodes 展示真实同步状态，并支持离线重试/自动续期。
ALTER TABLE `servers`
  ADD COLUMN `cert_fingerprint` TEXT,
  ADD COLUMN `cert_synced_at` BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN `cert_status` VARCHAR(32) NOT NULL DEFAULT '',
  ADD COLUMN `cert_error` TEXT;