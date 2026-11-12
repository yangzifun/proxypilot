-- 节点配置一致性巡检：记录最近一次「机器实际文件 vs 面板内容」的核对时间与结果。
--
-- 背景（2026-11-12 曼谷1 故障）：
--   节点离线很久后重新上线，Agent 安装脚本用 bootstrap 默认模板覆盖了 sb.json，
--   而面板 applied_version 仍是旧值（陈旧）、不会自动重下发，
--   结果 sing-box 是 active 但只有 4 个入站，面板以为一切正常。
--
-- 有了这两列，面板可以定期让 Agent 报一次真实文件的 sha256：
--   与 content_hash 不一致 → 自动重下发一次（带冷却，避免抖动）。

ALTER TABLE node_configs
  ADD COLUMN last_checked_at BIGINT NOT NULL DEFAULT 0 COMMENT '最近一次一致性核对时间（毫秒）',
  ADD COLUMN last_check_sha  VARCHAR(64) NOT NULL DEFAULT '' COMMENT '核对时机器上实际文件的 sha256',
  ADD COLUMN last_heal_at    BIGINT NOT NULL DEFAULT 0 COMMENT '最近一次自愈重下发时间（毫秒）';

CREATE INDEX idx_node_configs_checked ON node_configs (last_checked_at);