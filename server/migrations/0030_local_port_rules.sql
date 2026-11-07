-- 本机端口开放/关闭规则（由 PP_PORTS 链落地）
CREATE TABLE IF NOT EXISTS local_port_rules (
  id          BIGINT       NOT NULL AUTO_INCREMENT,
  port        INT          NOT NULL,
  protocol    VARCHAR(8)   NOT NULL DEFAULT 'tcp',
  action      VARCHAR(8)   NOT NULL DEFAULT 'open',
  note        VARCHAR(128) NOT NULL DEFAULT '',
  created_at  BIGINT       NOT NULL DEFAULT 0,
  PRIMARY KEY (id),
  UNIQUE KEY uk_port_proto (port, protocol)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
