-- 0054: 节点累计流量「单调累计」（task-17）
--
-- 背景：面板「节点累计 ↓/↑」之前直接使用 sing-box clash_api 的 downloadTotal /
-- uploadTotal（进程内内存计数）。sing-box 一重启（节点重启/配置重下发/服务重启）
-- 该计数归零，面板显示跟着掉。
--
-- 本表按 node_id 维护：
--   up_total/down_total : 历史求和（单调递增，重启不回退）—— 面板展示这个值
--   last_raw_up/down    : 上一轮上报的原始计数（用于计算增量/识别重启）
--
-- 幂等：CREATE TABLE IF NOT EXISTS；回填用 INSERT IGNORE，可重复执行。
-- 回填把存量节点的「当前快照累计」同时作为 lifetime 初值与 last_raw，
-- 这样迁移后的第一次上报就能正确累加，不会显示为 0。
CREATE TABLE IF NOT EXISTS node_traffic_lifetime (
  node_id BIGINT NOT NULL,
  up_total BIGINT NOT NULL DEFAULT 0,
  down_total BIGINT NOT NULL DEFAULT 0,
  last_raw_up BIGINT NOT NULL DEFAULT 0,
  last_raw_down BIGINT NOT NULL DEFAULT 0,
  updated_at BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (node_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

INSERT IGNORE INTO node_traffic_lifetime
  (node_id, up_total, down_total, last_raw_up, last_raw_down, updated_at)
SELECT node_id, up_total, down_total, up_total, down_total, sampled_at
  FROM node_stats_snapshot
 WHERE node_id > 0;