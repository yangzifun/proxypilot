-- 0039: 客户端网页登录授权码
CREATE TABLE IF NOT EXISTS client_auth_codes (
  code VARCHAR(96) NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  token TEXT NOT NULL,
  email VARCHAR(191) NOT NULL DEFAULT '',
  client_name VARCHAR(128) NOT NULL DEFAULT '',
  created_at BIGINT NOT NULL DEFAULT 0,
  expires_at BIGINT NOT NULL DEFAULT 0,
  approved_at BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (code),
  KEY idx_client_auth_exp (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
