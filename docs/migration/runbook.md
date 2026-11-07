# 旧版 Worker → V15.0（Go + MySQL）迁移 Runbook

> 目标：把 Cloudflare Worker + D1 上的现有数据与配置迁移到 Go + MySQL，同时让旧 Agent、旧订阅链接、旧管理员密码/令牌继续可用。
> 建议预留 **10–20 分钟维护窗口**；真实“零停机双写”不在本方案内。

## 0. 先决条件

- 目标机：Linux，MySQL **8.0.13+**（推荐 8.4），直接 systemd 部署（`deploy/install.sh`）。
- 工具：`wrangler`（已登录）、`mysql` 客户端、Python 3。
- 新服务 V15.0 二进制或镜像。
- 旧 Worker 的敏感变量（必须原样带到新系统）：
  - `JWT_SECRET` → `PP_JWT_SECRET`
  - `AGENT_KEY_SECRET`（若有）→ `PP_AGENT_KEY_SECRET`
  - `TURNSTILE_*`、`OIDC_*`、SMTP / Cloudflare 发信配置
- 若使用 Turnstile/OIDC，域名必须与旧站一致或同步更新回调地址。

## 1. 备份与冻结（切换前 T-1 天）

```bash
# D1 全量导出（SQL）
wrangler d1 export <数据库名> --remote --output d1-$(date +%F).sql

# 记录当前 Worker 环境变量（控制台或 wrangler secret list）
# 记录当前 DNS TTL（建议提前改成 60s）

# 备份 R2/S3 桶（如有）
rclone sync r2:proxypilot-bucket ./bucket-backup
```

同时记录各表行数，切换后对拍：

```sql
-- D1 控制台执行
SELECT 'configs', COUNT(*) FROM configs
UNION ALL SELECT 'uuid_info', COUNT(*) FROM uuid_info
UNION ALL SELECT 'servers', COUNT(*) FROM servers
UNION ALL SELECT 'node_configs', COUNT(*) FROM node_configs
UNION ALL SELECT 'singbox_nodes', COUNT(*) FROM singbox_nodes
UNION ALL SELECT 'mtproto_nodes', COUNT(*) FROM mtproto_nodes
UNION ALL SELECT 'admin_users', COUNT(*) FROM admin_users;
```

## 2. 启动 MySQL 与新服务（空库）

```bash
cd server
cp .env.example .env
# 必改：PP_MYSQL_DSN、PP_JWT_SECRET、PP_AGENT_KEY_SECRET、PP_ADMIN_PASSWORD、PP_PUBLIC_BASE_URL
systemctl start mysql
# 先启动一次 Go，让它自动执行 0001–0005 迁移
go run ./cmd/proxypilot
```

迁移日志应显示 `0001_init` … `0006_alter_cmd_columns`。

## 3. D1 → MySQL 数据导入

```bash
# 1) D1 SQL → MySQL INSERT（直接读取 wrangler 导出的 SQL，无需手动还原 sqlite）
python3 server/tools/d1_to_mysql.py \
  --d1-sql d1-2026-11-01.sql \
  --out d1-import.sql

# 2) 导入（空库可不用 --truncate；重导时加 --truncate）
mysql -h127.0.0.1 -uroot -p proxypilot < d1-import.sql
```

转换规则：

- 只生成 INSERT，不建表（表结构由 migrations 负责）。
- 时间戳统一为 13 位毫秒（秒级自动 ×1000）。
- `sqlite_sequence`、`cfips`、`cf_domains`、`edgeone_domains`、`auto_update_settings` 自动跳过。
- 保留显式主键 ID；MySQL `AUTO_INCREMENT` 会从最大值继续。
- 不生成 `email_norm`（MySQL 生成列）。

如果 D1 中存在目标 schema 没有的列，导入会报 `Unknown column`：先用 `SHOW COLUMNS` 对比并在转换前删除该列/加迁移，不要直接忽略。


### 3.1 只有 D1 Read、没有 Export 权限时（API 只读导出）

`wrangler d1 export` 需要 D1 Export 权限；若 Token 只有 Read，会出现
`Authentication error`。此时用 HTTP API 按表 SELECT（**只读，不修改线上数据**）：

```bash
export CF_TOKEN=<Cloudflare API Token>

# 在目标 MySQL 上生成“目标列清单”
mysql -h127.0.0.1 -uroot -p -N -B -e "
  SELECT table_name, column_name FROM information_schema.columns
  WHERE table_schema='proxypilot_migrate'
  ORDER BY table_name, ordinal_position" > target-columns.tsv

python3 server/tools/d1_api_dump.py \
  --account 9ef942805abcaef4dbf6574131453c58 \
  --database fda934d7-19e0-4dbe-99c9-9cea352c3f71 \
  --target-columns target-columns.tsv \
  --out d1-api-import.sql

mysql -h127.0.0.1 -uroot -p proxypilot_migrate < d1-api-import.sql
```

要点：

- 使用 `rowid` keyset 分页，避免线上写入导致 OFFSET 重复/漏行。
- 自动只导入“源表与目标表共有的列”，跳过 `_cf_KV`、`sqlite_sequence`、已废弃 Cloudflare 表。
- 时间戳 `*_at` 自动把秒级转成毫秒。
- 仍需在低峰/停写时做最终增量导入；API 导出不是快照。

### 3.2 已实测的 schema 差异

D1 的 TEXT 列导入 MySQL `VARCHAR(255)` 会报 `Data too long`（本项目实测
`node_configs.restart_cmd`）。已通过迁移 `0006_alter_cmd_columns` 把
`node_configs/node_templates` 的 `restart_cmd`、`check_cmd` 放宽为 `LONGTEXT`。
真实导入前请先确认目标库已执行到 0006。

## 4. 行数与关键数据对拍

```sql
SELECT 'configs', COUNT(*) FROM configs
UNION ALL SELECT 'uuid_info', COUNT(*) FROM uuid_info
UNION ALL SELECT 'servers', COUNT(*) FROM servers
UNION ALL SELECT 'node_configs', COUNT(*) FROM node_configs
UNION ALL SELECT 'singbox_nodes', COUNT(*) FROM singbox_nodes
UNION ALL SELECT 'mtproto_nodes', COUNT(*) FROM mtproto_nodes
UNION ALL SELECT 'admin_users', COUNT(*) FROM admin_users;
```

重点检查：

```sql
SELECT username, email, email_verified FROM admin_users;      -- 管理员与邮箱
SELECT id, name, link_mode, agent_token_hint FROM servers;    -- Agent 服务器
SELECT uuid, email, status FROM uuid_info LIMIT 10;           -- 用户
SELECT COUNT(*) FROM configs WHERE singbox_node_id > 0;       -- 节点生成的配置
```


## 4.1 迁移预演记录（2026-11-01）

- 源库：D1 `proxypilot`（fda934d7-…），通过 HTTP API 只读导出。
- 目标：独立库 `proxypilot_migrate`（MySQL 8.4），未修改生产库。
- 导入结果：25 张表、16,138 行；`servers=18`、`configs=644`、`uuid_info=16`、
  `node_configs=22`、`singbox_nodes=17`、`server_metrics=13,045`。
- 用临时 Go 实例连接预演库验证：`/healthz`、`/api/uuids`（16）、`/api/servers`（18）、
  `/api/stats`（11 UUID / 6 模板 / 989 访问日志）均正常。
- 预演发现并修复：`restart_cmd` 长度不足（迁移 0006）。

## 5. 配置与文件迁移

- **对象桶**：旧 R2/S3 → 新机 `PP_BUCKET_DIR`（`rclone copy`），路径保持一致。
- **Agent 分发**：`agent/dist` 下放 `manifest.json`、两个平台二进制、`SHA256SUMS`。
- **邮件**：后台「系统设置 → 邮件/通知」填 SMTP 或 Cloudflare API Token；也可用 `PP_*` 环境变量。
- **Turnstile / OIDC**：后台「登录与验证 / SSO」填写；OIDC 回调地址改为新域名 `/api/oidc/callback`。
- **站点地址**：`PP_PUBLIC_BASE_URL=https://新域名`（Agent 下载与 OIDC 推导）。

## 6. 并行验证（不切 DNS）

在本地 hosts 把测试域名指到新机，或直接用 IP + Host 头：

```bash
curl -s http://新IP/healthz
curl -s -H 'Host: pp.example.com' http://新IP/admin | head
server/tools/smoke.sh http://新IP
```

再验证：

1. 管理员登录、MFA、邮箱验证码。
2. `/sub/<旧 UUID>` 输出与旧站一致。
3. 旧 Agent：把其中一台的 `PROXYPILOT_URL` 临时指到新地址，确认 keycheck/poll 正常（令牌不变）。
4. sing-box/MTProto 预览、任务下发、WebSSH。
5. 备份导出（系统设置 → 数据备份）。

## 7. 切换窗口（约 10 分钟）

1. 公告维护，停止后台写入（不再创建/修改配置，暂停 Agent 任务）。
2. **最后一次**导出 D1：
   ```bash
   wrangler d1 export <db> --remote --output d1-final.sql
   ```
3. 重新转换并整库重导：
   ```bash
   python3 server/tools/d1_to_mysql.py --d1-sql d1-final.sql --out d1-final-import.sql
   mysql -h127.0.0.1 -uroot -p proxypilot < d1-final-import.sql
   ```
   （或 `--truncate` 清空后重导；两种方式都要在维护窗口内完成。）
4. DNS 切换到新机（A/AAAA 或 CNAME），等待 TTL 生效。
5. 旧 Worker 保持在线但不再接流量；保留至少 24–48 小时。

## 8. 切换后验收

```bash
server/tools/smoke.sh https://pp.example.com
```

- 管理员登录 / 用户中心邮箱登录 / OIDC
- 旧订阅链接、Clash 输出
- Agent 在线状态、任务下发、WebSSH
- 邮件通知（离线/恢复/验证码）
- 备份导出 ZIP
- 观察 `journalctl -u proxypilot -f` 中的 ERROR

## 9. 回滚

- DNS 切回旧 Worker（TTL 60s，通常几分钟）。
- 旧 D1 数据在切换后可能已落后；若新系统已产生写入，回滚会丢失这部分数据，需人工补录。
- 因此建议：**切换窗口内停止写入**，并在回滚前评估差异。

## 10. 兼容性要点

| 项 | 处理 |
|---|---|
| 管理员旧 JWT | 同一 `PP_JWT_SECRET` → 继续有效 |
| Agent 接入令牌 | 同一 `PP_AGENT_KEY_SECRET` → `enc:v1:` 可解密；协议兼容 |
| Agent 轮询 | poll/ack 协议一致；WS/wait 自动回退 |
| 订阅 UUID | 数据原样导入，链接不变 |
| 自增 ID | 显式导入，后续从最大值递增 |
| D1 时间戳 | 秒级自动 ×1000，保持毫秒语义 |
| 旧表 | `sqlite_sequence` / 已废弃 Cloudflare 表跳过 |
| 前端 | 内嵌 V15.0 bundle；如自定义前端，设置 `PP_WEB_DIST` |

## 11. 已知限制

- 不是双写方案；维护窗口内会短暂不可写。
- 导入脚本不自动处理 D1 与 MySQL 的**列差异**，需先对拍 schema。
- 迁移后旧 Worker 不再同步；回滚会丢失切换后的新数据。
- 真实导入前建议先在测试库跑一遍并核对行数。
