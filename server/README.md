# ProxyPilot Go 服务（V15.4）

Cloudflare Worker 的完整替代实现：Go + MySQL 8，内嵌生产前端 bundle，Agent 协议与数据格式保持兼容。

> 架构总览见 [`ARCHITECTURE.md`](../ARCHITECTURE.md)；迁移总案见 [`docs/migration/README.md`](../docs/migration/README.md)，
> Schema 映射见 [`docs/migration/schema-mapping.md`](../docs/migration/schema-mapping.md)，
> 功能缺口清单见 [`docs/migration/gaps.md`](../docs/migration/gaps.md)。
>
> 覆盖率检查：`python3 tools/check_coverage.py --repo ..`

## 当前实现

| 能力 | 状态 |
|---|---|
| MySQL schema（26 表 + 索引 + 种子） | ✅ |
| 迁移执行器（`schema_migrations`） | ✅ |
| 口令：PBKDF2 + 兼容旧 SHA-256 自动升级 | ✅ |
| JWT（与 Worker 同算法、同载荷） | ✅ |
| TOTP（RFC 6238） | ✅ |
| `GET /healthz` | ✅ |
| `POST /api/login`（含 MFA）、`POST /api/change-password`、`GET /api/mfa/status` | ✅ |
| 用户中心：`/api/portal/send-code`、`login`、`uuids`、`email/*` | ✅ |
| 订阅：`GET /sub/:uuid`、`GET /batch-configs/:uuid`（外部源缓存 + 摘要头） | ✅ |
| 分组：`GET/POST/PUT/DELETE /api/uuids` | ✅ |
| 服务器：列表 / 状态 / 新增 / 编辑 / 删除 / 详情 / 轮换接入令牌 | ✅ |
| 模板：`GET /api/templates`、`/api/templates/manage`（CRUD + 设为默认） | ✅ |
| 统计与日志：`GET /api/stats`、`/api/logs/summary`、`POST /api/logs/cleanup`（超管 + 二次验密） | ✅ |
| 配置管理：`/api/uuid-configs` CRUD、`/manage/configs`、`/manage/uuid-sub-sources` | ✅ |
| 账号体系：`/api/apikeys` CRUD/轮换、`/api/admin/users` CRUD、MFA 全套 | ✅ |
| Clash：`GET /clash-config`（9 种协议 + 模板注入 + 黄金用例） | ✅ |
| sing-box：渲染内核 + 全量 CRUD/preview/下发/授权/回收 | ✅ |
| 批量任务：`/api/jobs` + `singbox.apply_all` / `singbox.push` | 🟡 同步执行 |
| Agent：`keycheck`、`latest`、`poll`、`ack`、`tasks`、`push-update`、`bootstrap` | ✅（待真实 Agent 回归） |
| 鉴权：JWT + `X-API-Key`（`groups/configs/templates` 权限位） | ✅ |
| 页面：构建时生成并内嵌 `/admin-app.js`、`/admin-app.css`；运行时无需 npm | ✅ |
| 本地存储：桶 + Agent 分发（`PP_BUCKET_DIR` / `PP_AGENT_DIST_DIR` + `/agent-dist/*`） | ✅ |
| WebSSH：Go WebSocket 中继（无需 Durable Object） | ✅（单实例内存会话；多实例需粘性路由） |
| 全部 150 条路径（含动态子路由） | ✅ 100% |

**过渡期代理**：未迁移的 `/api/*`、`/sub/*`、`/manage*` 会保留方法、查询串、`Authorization`
与响应头，原样转发给旧 Worker。这样 Go 服务可以先接管域名、不牺牲现有功能，再按域逐步关闭代理。







## 本地直控（无需 Agent）

控制面板所在服务器会自动登记为 `link_mode=local` 的「本机（控制服务器）」：

- 每 5 秒采集 CPU / 内存 / 磁盘 / 网络 / 连接 / 进程 / 服务状态，写入与 Agent 相同的监控表。
- 支持与 Agent 相同的 deploy / restart / exec / probe 任务；任务入队后立刻在本机执行。
- 支持 WebSSH（本地 PTY），无需安装 Agent。
- 节点绑定、模板下发到本机时走同一套任务队列，直接写本机文件并执行重启命令。
- 因为需要写系统路径和重启服务，服务以 **root** 运行（`proxypilot.service` 的 `User=root`）；请自行评估安全边界。

## 实时监控

- Agent 1.7.0 起默认 `agent_next_poll_sec=2`（最小 2 秒），`metrics_every=1`（每次 poll 都采指标）；任务入队通过 Agent WebSocket 立即唤醒。
- 离线判定阈值 = `max(60 秒, 4 × 心跳间隔)`，严格大于心跳；Cron 每 15 秒检查一次。
- 监控页默认 5 秒刷新；卡片展示实时网络 ↓/↑、磁盘实时读/写，以及累计流量、连接数、进程数。

## 附件管理（R2）

- 后台「系统 → 附件管理」：浏览整个 R2 Bucket（含 `agent/`、`rule/`），支持上传、下载、删除。
- 文本文件可在线读取与编辑（≤ 2 MiB）：打开文件 → 编辑 → 保存；二进制/超大文件仅支持下载。
- 可选 CDN：配置 R2 公共自定义域后，文件列表会生成 `cdn_url`，页面可复制 CDN 链接。
- API：
  - `GET /api/attachments?prefix=`
  - `GET /api/attachments/status`
  - `POST /api/attachments/upload?prefix=`（multipart `file`）
  - `GET /api/attachments/download?key=`
  - `GET /api/attachments/content?key=`
  - `PUT /api/attachments/content`（JSON `key` / `content`）
  - `DELETE /api/attachments?key=`
- 配置：统一使用 `PP_CLOUDFLARE_API_TOKEN` + `PP_CLOUDFLARE_ACCOUNT_ID` + `PP_R2_BUCKET`；`PP_CDN_BASE_URL` 可选。
- R2 的 S3 凭证由 Cloudflare Token 自动派生，无需单独 R2 Access Key。

## 备份与恢复

- 后台「系统 → 备份管理」提供独立页面，支持导出与导入。
- API：
  - `GET /api/backup/export`（仅超管 JWT）
    - `bucket=0` 不打包本地对象桶（默认包含）
    - `agent=1` 额外打包 Agent 二进制（默认不含）
    - `logs=0` 跳过访问日志与下发日志
  - `POST /api/backup/import`（仅超管 JWT，multipart `file`）
- ZIP 内容：`manifest.json`、`schema.sql`、`db/<表>.json`、`bucket/...`、可选 `agent-dist/...`。
- 导入时在事务内按备份中的表整表替换，桶文件随后覆盖写入；会跳过 `schema_migrations`，避免回退迁移版本。
- 注意：`app_settings` 中的 Secret 为 AES-GCM 密文；恢复时必须使用相同的 `PP_JWT_SECRET` / `PP_AGENT_KEY_SECRET` 才能解密。

## 后台系统设置（/admin/settings）

后台不再需要改环境变量重启：超管打开 `https://<域名>/admin/settings`（页面会要求输入后台 token，
与 localStorage 的 `token` 相同）即可配置，保存后 30 秒内全量生效。

| 分组 | 可配置项 | 环境变量回落 |
|---|---|---|
| 登录方式 | 密码登录、邮箱验证码登录、OIDC 登录开关 | `PP_OIDC_DISABLE_PASSWORD` |
| 人机验证 | Turnstile Site Key / Secret | `PP_TURNSTILE_SITE_KEY` / `PP_TURNSTILE_SECRET` |
| 邮件 | 发信通道（auto/smtp/cloudflare）、SMTP、Cloudflare API Token / Account ID / 发件地址 | `PP_SMTP_*` / `PP_MAIL_FROM` / `PP_CLOUDFLARE_*` |
| OIDC | Issuer、Client ID/Secret、Scopes、显示名、按钮文案、回调地址 | `PP_OIDC_*` |
| 站点 | 对外地址（Agent 分发 / OIDC 回调推导） | `PP_PUBLIC_BASE_URL` |

规则：

- **Secret 留空 = 保持原值**；填 `__clear__` 表示清空并回落环境变量。
- 保存时会校验：三种登录方式至少要保留一种**可用**（邮箱方式要求 SMTP 或 Cloudflare Email Sending 已配置），防止把自己锁在门外。
- 所有配置存 `app_settings` 表，30 秒缓存；后台保存后立即失效缓存。
- 人机验证仍是 Cloudflare Turnstile 的 `siteverify` 接口（Cloudflare 的外部服务，不是 Workers 运行时依赖），
  也可留空关闭；邮件支持 SMTP 或 Cloudflare Email Sending REST API（不依赖 Workers 绑定）。\n- **Cloudflare 统一凭证**：一个 `cloudflare_api_token` 同时用于 R2 对象存储与 Email Sending；R2 的 S3 Access Key 由 Token 自动派生（Access Key ID = Token ID，Secret = SHA256(Token)），无需再配置单独的 R2 Key。\n- Token 权限需包含 `Account → R2 → Object Read & Write` 和 `Account → Email Sending → Edit`；Turnstile 的 Site Secret 属于独立校验密钥，不能复用 API Token。

API：`GET/PUT /api/settings`（仅超管 JWT）。

## 生产部署（替换域名）

> 旧版 Worker + D1 的完整迁移步骤见 [docs/migration/runbook.md](../docs/migration/runbook.md)。

目标：把域名直接指向本服务，功能与 Worker 对齐；Worker 仅作为可选回滚。

### 1. 准备产物

```bash
# Agent 二进制 / manifest（需要 Go 环境；产出到 agent/dist）
cd agent && ./build.sh && cd ..

# 服务镜像（内嵌前端，无需 npm）
cd server && docker compose build
```

### 2. 数据迁移（D1 → MySQL）

在旧环境导出 D1（SQLite）后：

```bash
python3 server/tools/d1_to_mysql.py --input d1-export.sql --output server/data/import.sql
# 用 MySQL 客户端导入；或首次启动前把 import.sql 放到可执行位置手工执行
mysql -h127.0.0.1 -uroot -p proxypilot < server/data/import.sql
```

也可以空库启动（自动迁移 26 张表），再由后台重新录入。

### 3. 配置

```bash
cp server/.env.example server/.env
# 必改：PP_JWT_SECRET（与 Worker 的 JWT_SECRET 相同）、PP_SUPER_ADMIN、PP_PUBLIC_BASE_URL
# 按需：SMTP、OIDC、Turnstile、PP_AGENT_KEY_SECRET（与 Worker 相同才能解密旧 Agent 密钥）
```

关键点：

- **JWT_SECRET 必须与 Worker 一致**，否则切换后现有后台/用户 token 全部失效。
- **AGENT_KEY_SECRET 必须与 Worker 一致**，否则已有服务器的 Agent 密钥无法解密（可在后台重置）。
- `PP_PUBLIC_BASE_URL` 反代后必填，用于 `/agent-dist` 下载地址与 OIDC 回调。
- `PP_ADMIN_PASSWORD` 生产必配：首次启动若 admin 仍是默认口令 `password`，会自动重置为该值；不配则日志告警。
- 旧 Worker 可临时保留：`PP_LEGACY_BASE=https://旧域名` 作为未注册路径兜底。

### 4. 启动

```bash
cd server
docker compose up -d --build      # mysql + server（server 启动时自动迁移）
curl -s http://127.0.0.1:8080/healthz
```

### 5. 反向代理

Caddy 示例：

```caddy
pp.example.com {
    encode zstd gzip
    reverse_proxy 127.0.0.1:8080
}
```

Nginx 需转发：`Host`、`X-Forwarded-Proto`、`X-Forwarded-Host`、`X-Real-IP`；
Agent 的 `/api/agent/ws` 与 `/api/shell/*` 都是 WebSocket 升级请求，反代必须允许 Upgrade。

### 6. 切换前验收清单

| 项 | 命令 / 检查 |
|---|---|
| 健康检查 | `curl -s https://pp.example.com/healthz` |
| 后台页面 | 打开 `/admin`，确认 `window.__PP_BOOT__.mode === 'admin'` |
| 用户中心 | 打开 `/`，确认 `mode === 'portal'` |
| 登录 | 密码 / 邮箱验证码 / OIDC 三条路径 |
| 订阅 | `curl -s https://pp.example.com/sub/<uuid>`、`/clash-config?uuid=<uuid>` |
| 服务器列表 | 后台能看到、Agent 最近心跳正常 |
| Agent 接入 | 新服务器用后台生成命令安装，`poll` 200、任务可下发 |
| sing-box / MTProto | 预览、apply、客户端链接、二维码 |
| 邮件 | 验证码能收到（SMTP 或 Cloudflare Email Sending 已配置） |
| 对象存储 | `/api/bucket/files` 可列出本地桶 |
| 前端静态 | `/admin-app.js`、`/assets/world.svg` 200 |
| WebSSH | 后台打开终端 → `waiting` → `ready` → 可交互；`/api/shell/status` 返回 `available:true` |
| 冒烟脚本 | `server/tools/smoke.sh https://pp.example.com` 全部 OK |

### 7. 切流与回滚

1. DNS 指向新服务器（或反代切上游）。
2. 观察 24h：`/healthz`、错误日志、Agent 心跳、订阅请求。
3. 回滚：DNS 指回 Worker；MySQL 期间产生的数据需用反向脚本同步（建议切流窗口内只读或短时维护）。

### 部署前必须实测（本仓库内无法完成）\n\n1. **MySQL 迁移**：`PP_TEST_DSN=... go test -tags integration ./internal/api -run TestIntegrationLoginAndSub`\n2. **Docker 构建**：本机无 Docker，`docker compose build` 需在部署机执行\n3. **真实 Agent**：keycheck → bootstrap → poll → deploy → ack\n4. **SMTP / OIDC / Turnstile**：需真实服务\n5. **反向代理 TLS**：`X-Forwarded-Proto/Host` 透传\n\n### 已知降级（切换前确认可接受）

- **WebSSH 已可用**：浏览器 ↔ Go ↔ Agent(WS) ↔ PTY；多实例部署需要按 server_id 粘性路由（会话在进程内存）。
- **批量任务同步执行**：`apply-all`/`push` 在大批量时会占用请求，建议分批。
- **通知**：Telegram/Webhook 走后台配置（`app_settings`），不是 Worker 环境变量。
- **OIDC**：仅 RS256。
- **MySQL 与真实 Agent 未做端到端回归**：第一次上线务必按上面的清单验证。

## 快速开始

```bash
cd server

# 1) 本地 MySQL 8（自动执行 migrations/0001_init.sql）
docker compose up -d mysql

# 2) 配置
cp .env.example .env
# 生成密钥：openssl rand -base64 48  → 填入 PP_JWT_SECRET

# 3) 运行（自动迁移）
set -a; . ./.env; set +a
go run ./cmd/proxypilot
```

验证：

```bash
curl -s localhost:8080/healthz
# {"ok":true,"service":"proxypilot-api","version":"dev","db":"up"}

TOKEN=$(curl -s -X POST localhost:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"password"}' | jq -r .token)

curl -s localhost:8080/api/servers -H "Authorization: Bearer $TOKEN" | jq
```

> 初始账号仍是 `admin / password`（与 Worker 一致的 SHA-256 哈希）。
> 首次登录后请立刻改密；登录成功会自动把哈希升级为 PBKDF2。

## 环境变量

| 变量 | 必填 | 说明 |
|---|---|---|
| `PP_MYSQL_DSN` | ✅ | `user:pass@tcp(host:3306)/proxypilot?charset=utf8mb4&parseTime=false&loc=UTC` |
| `PP_JWT_SECRET` | ✅ | 与 Worker 共用可让旧 token 直接可用；等于默认值会拒绝启动 |
| `PP_LEGACY_BASE` | | 未迁移接口的转发地址（如 `https://proxypilot.example.com`）；为空返回 501 |
| `PP_SMTP_ADDR` | | SMTP 地址（如 `smtp.example.com:587`，STARTTLS）；未配置时验证码接口返回 503 |
| `PP_SMTP_USER` / `PP_SMTP_PASS` | | SMTP 认证（可留空） |
| `PP_MAIL_FROM` | | 发件地址，需与 SMTP 账号匹配 |
| `PP_OIDC_DISABLE_PASSWORD` | | 为 true 时禁用密码登录（也接受 `OIDC_DISABLE_PASSWORD`） |
| `PP_AGENT_KEY_SECRET` | | Agent 令牌 AES-GCM 密钥；为空回落 `PP_JWT_SECRET` |
| `PP_AGENT_INSTALL_BASE` | | 安装脚本对象存储基址；为空用本站脚本 |
| `PP_ADDR` | | 监听地址，默认 `:8080` |
| `PP_SUPER_ADMIN` | | 超管用户名，默认 `admin` |
| `PP_AUTO_MIGRATE` | | 启动自动迁移，默认 `1` |
| `PP_TURNSTILE_SECRET` | | 为空则跳过人机验证 |
| `PP_DEV` | | 允许短密钥（仅本地） |

## 目录

```
cmd/proxypilot     启动、配置、迁移、优雅退出
internal/config    环境变量与校验
internal/db        连接池、迁移器、SQL 拆分
internal/auth      PBKDF2 / JWT / TOTP
internal/api       路由、中间件、登录、用户中心、订阅、分组、服务器、代理
migrations         0001_init.sql（embed 进二进制）
tools              d1_to_mysql.py（D1 数据转换）
```

## 测试与构建

生产部署只使用编译后的 `proxypilot` 二进制，前端已通过 `go:embed` 打进二进制；服务器不需要 Node.js、npm 或静态文件目录。

```bash
# 1) 构建前端并同步到 server/internal/api/assets_web
cd web-antd && node scripts/build.mjs

# 2) 编译服务端（前端随之嵌入）
cd ../server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X main.version=$(git describe --tags --always)" \
  -o proxypilot ./cmd/proxypilot

# 或使用 Makefile 一键完成前后端构建
make release
```

```bash
go vet ./...
go test ./...
```

单测覆盖：

- 口令（新格式 / 遗留 SHA-256 / 错误口令）、JWT（往返 / 篡改 / 过期）、TOTP（RFC 6238 向量 + skew）
- SQL 拆分器（注释 / 引号 / 反引号）、内嵌 SQL（26 张表 / 无尾分号）
- 验证码哈希与 Worker 对齐（固定向量）、邮箱规范化、订阅体解析（明文 / Base64 / 过滤 / 不支持协议计数）
- HTTP 契约：501 兜底、401、CORS 预检、404、代理透传（方法 / Authorization / 查询串 / 响应头）
- API 密钥：路由白名单 → 权限位映射、`groups/configs/templates` 判定、白名单外 403
- Agent 令牌：生成格式、掩码、HKDF+AES-GCM 加解密往返、安装命令两种基址
- 服务器输入校验：名称/地址清洗、轮询间隔收敛；OIDC 只读模式下密码登录 403

真实 MySQL 的端到端用例（登录 → 建分组 → 拉订阅）默认跳过，提供 DSN 时才运行：

```bash
PP_TEST_DSN='root:proxypilot@tcp(127.0.0.1:3306)/pp_test?charset=utf8mb4&loc=UTC' \
  go test -tags integration ./internal/api -run TestIntegrationLoginAndSub -v
```

## 设计说明

- **无框架**：只用标准库 `net/http`（Go 1.22 方法路由）与 `database/sql`，依赖仅 MySQL 驱动。
- **契约优先**：JSON 字段名、错误体、状态码与 `worker.js` 完全一致，前端零改动。
- **渐进替换**：未迁移接口转发旧 Worker，而不是让功能半残；关闭代理即完成一个域的迁移。
- **安全基线**：拒绝默认 JWT 密钥、口令无盐 SHA-256 自动升级、登录失败不做账号存在性区分。

## 本地存储与 Agent 分发

- `PP_BUCKET_DIR`（默认 `server/data/bucket`）：本地对象存储，`/api/bucket/files` 列出、`/api/bucket/file` 上传/下载。
- `PP_AGENT_DIST_DIR`（默认 `agent/dist`）：Agent 二进制、`manifest.json`、`SHA256SUMS`、sing-box 二进制。
- `GET /agent-dist/<file>`：直接托管上述文件；安装脚本/`bootstrap`/`push-update` 在未配置外部基址时自动使用本站地址。
- `PP_PUBLIC_BASE_URL`（可选）：反向代理后无法从请求推断公网地址时，用它拼接 `/agent-dist`。
- 发布 Agent：`./agent/build.sh` 产出到 `agent/dist` 即可，无需上传 S3。
