-- 0026_user_node_recent: 用户最近活跃节点（连接级在线展示）
CREATE TABLE IF NOT EXISTS user_node_recent (
  uuid VARCHAR(36) NOT NULL,
  node_id BIGINT NOT NULL,
  last_ts BIGINT NOT NULL DEFAULT 0,
  up BIGINT NOT NULL DEFAULT 0,
  down BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (uuid, node_id),
  KEY idx_last (last_ts)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
