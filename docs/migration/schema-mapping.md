# Schema 映射：D1(SQLite) → MySQL 8

> 生成依据：`workers/init_database.sql` + `workers/worker.js` 中的运行时建表/补列（取并集）。
> 最终产物：`server/migrations/0001_init.sql`（26 张表、30+ 索引、种子数据）。

## 1. 总览

| 项目 | 数量 |
|---|---|
| 迁移的表 | 26 |
| 被丢弃的遗留表 | 4（`cfips` / `cf_domains` / `edgeone_domains` / `auto_update_settings`，Worker 中 0 引用） |
| 运行时才建、SQL 脚本缺失的表 | 5（`app_settings` / `batch_jobs` / `batch_job_items` / `shell_sessions` / `ip_geo`） |
| 新增列（仅为唯一性） | 2（`admin_users.email_norm`、`uuid_info.email_norm`） |
| 删除的唯一索引 | 1（`configs(uuid, config_data)`，MySQL 无法为 `MEDIUMTEXT` 建唯一索引） |

## 2. 类型映射

| SQLite | MySQL | 说明 |
|---|---|---|
| `INTEGER PRIMARY KEY AUTOINCREMENT` | `BIGINT UNSIGNED NOT NULL AUTO_INCREMENT` | 应用层按 int64 处理 |
| `INTEGER` | `BIGINT` | 时间戳、布尔、计数统一 |
| `REAL` | `DOUBLE` | 经纬度、负载 |
| `TEXT`（短、需索引） | `VARCHAR(n)` | `uuid` 36、`email` 320、`ip` 45、状态类 32、路径类 255、其余 191 |
| `TEXT`（长） | `LONGTEXT` / `MEDIUMTEXT` | `config_data` 用 `MEDIUMTEXT`（订阅节点行可能很长） |
| `BOOLEAN` / 0-1 | `BIGINT` | 保持 0/1，Go 侧显式转换 |
| 布尔语义 | `enabled` / `mfa_enabled` … | 不改成 `TINYINT(1)`，避免前端拿到 `true/false` 差异 |
| 时间戳 | `BIGINT`（Unix 毫秒） | 与 `Date.now()` 对齐 |
| 时间戳默认值 | `DEFAULT (UNIX_TIMESTAMP(NOW(3))*1000)` | 需要 MySQL ≥ 8.0.13 |
| JSON 字符串 | `LONGTEXT` | 不启用 MySQL `JSON` 类型，保持字符串读写零改动 |

## 3. 行为差异（迁移时最容易被忽略的 6 点）

1. **排序规则**
   SQLite 默认 `BINARY`（区分大小写），MySQL 默认 `utf8mb4_0900_ai_ci`（不区分）。
   → 全库使用 `utf8mb4_0900_as_cs`，避免 `WHERE username = ?` 从“区分”变成“不区分”。

2. **邮箱唯一索引（含一个容易踩的坑）**
   原索引是表达式 + 部分索引：`UNIQUE INDEX ... (LOWER(email)) WHERE email IS NOT NULL` /
   `WHERE email <> ''`。MySQL 不支持部分索引 → 改为生成列：
   ```sql
   email_norm VARCHAR(320) GENERATED ALWAYS AS (NULLIF(LOWER(email), '')) STORED,
   UNIQUE KEY uq_admin_users_email (email_norm)
   ```
   **必须带 `NULLIF(..., '')`**：`uuid_info.email` 是 `NOT NULL`，Worker 允许大量分组留空邮箱；
   若直接 `LOWER(email)`，空串会互相撞唯一索引，第二个不填邮箱的分组直接插入失败。
   经 `NULLIF` 后空串变 `NULL`，多个 `NULL` 不参与唯一，语义与原部分索引一致。

3. **`configs` 去重**
   `UNIQUE(uuid, config_data)` 删除。原 Worker 本来就在应用层“先 SELECT 去重再 INSERT”，
   所以行为不变；若直接依赖数据库唯一约束的脚本，需要改为先查后插。

4. **保留字 `key`**
   `app_settings.key` 在 MySQL 是保留字 → 全部用反引号；Java/Go 侧拼 SQL 时注意。

5. **布尔与空串**
   SQLite 允许 `''` 与 `NULL` 混用；MySQL 严格模式下把 `''` 插入 `BIGINT` 会报错。
   → 迁移脚本需把空串转 `0` / `NULL`（见第 5 节）。

6. **DDL 自愈消失**
   Worker 每个请求都会 `CREATE TABLE IF NOT EXISTS` 兜底；Go 侧改为启动时迁移 + 版本表，
   不再“请求时建表”，这是性能与一致性的双重收益。

## 4. 表清单

| 表 | 列数 | 备注 |
|---|---:|---|
| `admin_users` | 11 | 口令升级为 PBKDF2；`email_norm` 新增 |
| `admin_email_codes` | 9 | 验证码哈希沿用 `sha256(purpose:email:code)`，Go 侧需复刻才能兼容未过期验证码 |
| `admin_oidc_identities` | 7 | `(issuer, sub)` 唯一 |
| `mfa_backup_codes` | 6 | 哈希存储 |
| `api_keys` | 13 | `api_key` 存 SHA-256；兼容历史明文行 |
| `app_settings` | 3 | `key` 为保留字；主键 `VARCHAR(191)` |
| `configs` | 13 | `config_data` 为 `MEDIUMTEXT`；去重靠应用层 |
| `config_templates` | 7 | `name` 唯一 |
| `config_access_logs` | 6 | `created_at` 为 ISO 字符串（原样保留，便于按日聚合） |
| `uuid_info` | 11 | `uuid` 唯一；`email_norm` 唯一 |
| `uuid_sub_sources` | 7 | — |
| `servers` | 46 | 历史列 `host`/`secret_enc` 保留但不再使用；Agent 字段全部保留 |
| `server_metrics` | 16 | 7 天保留，Go 侧定时清理 |
| `agent_tasks` | 14 | 状态机 pending/sent/done/failed/expired |
| `node_templates` | 13 | — |
| `node_configs` | 24 | 版本化下发：`version` / `content_hash` / `applied_version` |
| `node_push_logs` | 11 | — |
| `singbox_templates` | 40 | 协议模板，字段最多 |
| `singbox_nodes` | 23 | `server_id` 唯一（一机一节点） |
| `mtproto_templates` | 17 | `port` 默认 13709 |
| `mtproto_nodes` | 24 | `server_id` 唯一；`-1` 表示继承模板 |
| `mtproto_clients` | 16 | `(node_id, uuid)` 唯一 |
| `batch_jobs` | 18 | 后台批量任务 |
| `batch_job_items` | 12 | `(job_id, seq)` 唯一 |
| `shell_sessions` | 4 | WebSSH 会话登记（P3 用） |
| `ip_geo` | 6 | `ip` 主键 |

## 5. 数据迁移

### 5.1 导出与转换

```bash
# 1) 导出 D1
npx wrangler d1 export proxypilot --remote --output=d1.sql

# 2) 还原到本地 SQLite
sqlite3 d1.db < d1.sql

# 3) 逐表生成 MySQL INSERT（脚本见 server/tools/d1_to_mysql.py）
python3 server/tools/d1_to_mysql.py --sqlite d1.db --out d1_mysql.sql

# 4) 导入（先建表）
mysql -u root -p proxypilot < server/migrations/0001_init.sql
mysql -u root -p proxypilot < d1_mysql.sql
```

转换脚本负责：

- 空串 → `NULL`（数值列）/ `''`（文本列）
- 时间戳统一为 13 位毫秒
- `0/1` 布尔保持原值
- JSON 文本原样输出，非法 JSON 置 `NULL` 并记录
- 转义单引号、反斜杠、换行

### 5.2 时间戳纠偏

老库中 `configs.created_at` 早期写入的是**秒**，后期是**毫秒**，需统一：

```sql
UPDATE configs SET created_at = created_at * 1000 WHERE created_at > 0 AND created_at < 100000000000;
UPDATE configs SET updated_at = updated_at * 1000 WHERE updated_at > 0 AND updated_at < 100000000000;
-- 其余表同理（uuid_info / servers / node_configs / agent_tasks ...）
```

### 5.3 唯一性预检（导入前必须执行）

```sql
-- 重复邮箱（会让生成列唯一索引创建失败）
SELECT LOWER(email) AS e, COUNT(*) c FROM uuid_info
 WHERE email <> '' GROUP BY LOWER(email) HAVING c > 1;
SELECT LOWER(email) AS e, COUNT(*) c FROM admin_users
 WHERE email IS NOT NULL GROUP BY LOWER(email) HAVING c > 1;

-- 重复 UUID
SELECT uuid, COUNT(*) c FROM uuid_info GROUP BY uuid HAVING c > 1;

-- 重复配置行（去重逻辑依赖应用层）
SELECT uuid, config_data, COUNT(*) c FROM configs GROUP BY uuid, config_data HAVING c > 1;
```

### 5.4 迁移后校验

```sql
SELECT 'uuid_info' t, COUNT(*) FROM uuid_info UNION ALL
SELECT 'configs', COUNT(*) FROM configs UNION ALL
SELECT 'servers', COUNT(*) FROM servers UNION ALL
SELECT 'singbox_templates', COUNT(*) FROM singbox_templates;

-- 时间戳范围（应全部 > 1e12）
SELECT MIN(created_at), MAX(created_at) FROM servers;

-- 管理员可登录（哈希长度应为 64 或 pbkdf2 前缀）
SELECT username, LENGTH(password_hash) FROM admin_users;
```

## 6. 回滚

- 切流前：`mysqldump --single-transaction --routines --triggers proxypilot > snapshot.sql`
- 切流后 7 天内：D1 保留只读副本；回滚只需把 DNS/反代切回 Worker
- MySQL 侧不再写入的唯一风险是“切换后新增数据”，回滚前需按时间窗口反向同步
