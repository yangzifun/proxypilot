> 迁移操作手册（含 D1 导出、导入、切换与回滚）：[runbook.md](runbook.md)

# 迁移总案：Cloudflare Worker → Go + MySQL

> 状态：**P0 完成**（可编译、可测试的 Go 骨架 + MySQL 全量 schema）；P1/P2 部分完成；
> 缺口清单与修复记录见 [`gaps.md`](gaps.md)，覆盖率为 **150/150（100%，含动态子路由）**。
> 最后更新：2026-10-31

---

## 1. 先把事实摆正

| 说法 | 实际 |
|---|---|
| “Go 开发后端” | Go 只用于节点上的 `proxypilot-agent`（3,631 行）；后端是 **单文件 Cloudflare Worker**：`workers/worker.js`，14,216 行、4.75 MB、406 个顶层函数、100+ 路由 |
| “MySQL 数据库” | 现在是 **Cloudflare D1（SQLite）**：26 张表，`AUTOINCREMENT` / `STRFTIME` / `PRAGMA` 方言 |
| “前端 antd” | 正确：React 18 + antd 6 + antd-mobile 5 + @ant-design/plots，构建产物内联进 `worker.js` |

所以这次迁移的实质是：**把 1.4 万行 JS 按业务域搬到 Go，把 D1 换成 MySQL 8，前端与 Agent 协议保持不变。**

## 2. 目标与非目标

**目标**

- 可自托管、可水平扩展（API 无状态，多副本）
- 摆脱单文件 14k 行、单 isolate 限制、Workers 运行时约束
- 数据库可运维：备份、慢查询、只读副本、在线 DDL
- 保留现有前端、订阅链接、Agent 1.6.0 协议

**非目标**

- 不重写前端（antd 代码原样复用）
- 不改 Agent 的 poll/ack/WS 协议（只改服务端实现）
- 不改 `/sub/:uuid` 等对外 URL
- 不在第一阶段实现 WebSSH / MTProto 等长尾能力（P3/P4）

## 3. 迁移原则

1. **契约优先**：JSON 字段名、状态码、错误体 `{error: "..."}` 与 Worker 完全一致，前端零改动。
2. **一个域一个域搬**：每个域有独立验收与回滚开关，未迁移接口明确返回 501，而不是假装 404。
3. **先迁数据、后切流量**：MySQL 先追平 D1，再按域切读、切写。
4. **可回滚**：域名/路由层切换，Worker 保留只读兜底；数据库保留切换前快照。

## 4. 目标架构

```
                    ┌──────────────────────────────┐
  浏览器 / 客户端 ──▶│  Nginx / Caddy（TLS、路由拆分）│
                    └───────┬──────────────┬───────┘
                            │ /api/* /sub  │ /admin /login 静态页面
                            ▼              ▼
                   ┌────────────────┐   ┌──────────────────┐
                   │ Go API 服务     │   │ Cloudflare Worker │
                   │ (无状态, N 副本) │   │ (过渡期保留)       │
                   └───┬────┬───┬───┘   └──────────────────┘
                       │    │   │
              ┌────────▼─┐ ┌▼───▼─────────┐ ┌──────────────┐
              │ MySQL 8  │ │ Redis（队列/  │ │ S3 / MinIO    │
              │          │ │ 唤醒/会话）   │ │ (rule/、二进制)│
              └──────────┘ └──────────────┘ └──────────────┘
                       ▲
                       │ 出站 HTTPS（协议不变）
              proxypilot-agent（Go 1.6.0，systemd）
```

| Worker 能力 | Go 侧替身 | 阶段 |
|---|---|---|
| HTTP 路由（if 链 + URLPattern） | `net/http` ServeMux（Go 1.22 方法路由） | P0 ✅ |
| D1 | MySQL 8（`database/sql` + go-sql-driver） | P0 ✅ |
| Cron（每分钟） | `time.Ticker` + 单实例锁（MySQL `GET_LOCK`） | P4 |
| Queues（批量任务） | asynq（Redis）或 MySQL 任务表 + worker | P4 |
| Durable Object `AgentHub` | Redis Pub/Sub + WebSocket Hub | P3 |
| Durable Object `ShellSession` | WebSocket 网关（双向转发） | P3 |
| R2 | S3 / MinIO | P4 |
| Email 绑定 | SMTP / 邮件服务 API | P1 |
| Turnstile | 同左（HTTP 调用） | P0 ✅ |

## 5. 代码结构（本次已落地）

```
server/
├── cmd/proxypilot/main.go        # 启动、配置、迁移、优雅退出
├── internal/config               # 环境变量校验（拒绝默认 JWT 密钥）
├── internal/db                   # 连接池 + 迁移执行器 + SQL 拆分
├── internal/auth                 # PBKDF2（兼容旧 SHA-256）、HS256 JWT、TOTP
├── internal/api                  # 路由、中间件、login、servers、501 兜底
├── migrations/0001_init.sql      # MySQL 8 全量 schema（26 表 + 索引 + 种子）
└── README.md
```

**已实现（原生）**：
- 认证：`POST /api/login`（密码 + TOTP、哈希自动升级、可禁用密码登录）、`POST /api/change-password`、`GET /api/mfa/status`
- 用户中心：`/api/portal/send-code`、`/login`、`/uuids`、`/email/send-code`、`/email/verify`
- 订阅：`GET /sub/:uuid`、`GET /batch-configs/:uuid`（本地配置 + 外部订阅源 + 120s 缓存 + 摘要头、访问日志、状态拦截）
- 分组：`GET/POST/PUT/DELETE /api/uuids`（邮箱唯一、CTE 合并历史分组、库内级联删除）
- 服务器：`GET /api/servers`、`/api/servers/status`、`POST`、`GET/PUT/DELETE /{id}`、`POST /{id}/agent-token`（含 AES-GCM 密钥密文与安装命令）
- 模板：`GET /api/templates`（公开）+ `/api/templates/manage` CRUD / 设为默认
- 统计与日志：`GET /api/stats`、`GET /api/logs/summary`、`POST /api/logs/cleanup`（超管 + 二次验密）
- 配置管理：`/api/uuid-configs`（协议识别/去重/批量删）、`/manage/configs`（用户端，归属校验）、`/manage/uuid-sub-sources`（SSRF 防护）
- 账号体系：`/api/apikeys` CRUD/轮换、`/api/admin/users` CRUD、MFA 全套（含备份码登录）
- Clash：`GET /clash-config`（9 种协议转换 + 模板注入 + 去重/重命名 + 统计头）
- sing-box：渲染内核（sb/xray/ports/links，黄金用例）+ 模板/节点 CRUD + 可用 UUID + preview + apply 系列 + push + reconcile + releases + from-config + 模板预览 / WARP
- MTProto：渲染内核（secret / mtg TOML / tg 链接，黄金用例）+ 模板/节点 CRUD + apply + 客户端列表/生成/链接/rotate/吊销/恢复 + 邮件投递 + 节点预览 + 可用 UUID + apply-all + push + reconcile + 用户端 /api/portal/mtproto
- OIDC：/api/oidc/status|login|callback（发现文档、state/nonce/事务 Cookie、PKCE S256、id_token RS256+JWKS 验签、email_verified、code 换 token、userinfo → 邮箱绑定 → portal JWT）
- 访问统计：/api/stats/uuid-details、/api/uuid-configs/stats（访问日志 + 首末访问时间）
- 批量任务：`/api/jobs` 列表/创建/详情/取消/重试，内置 `singbox.apply_all` / `singbox.push` 两种 kind（同步执行）
- Agent 升级：POST /api/agent/push-update（按 manifest 版本与平台排队 update_agent）
- Agent 任务管理：GET /api/agent/tasks（服务器/状态过滤）、POST /api/agent/tasks/{id}/cancel
- Agent 接入：`keycheck` / `latest` / `poll` / `ack` + 节点渲染层（占位符、NODES、PROXIES_YAML、模板合并）；geo/自动装 sing-box/WebSSH 提示未迁移
- 鉴权：JWT + `X-API-Key`（`groups/configs/templates` 权限位，兼容历史明文密钥）
- 基础设施：统一 CORS / 结构化日志 / panic 恢复 / `GET /healthz`

**过渡期代理**：其余接口在配置 `PP_LEGACY_BASE` 后**原样转发**给旧 Worker
（保留 Authorization、查询串、响应头与状态码），未配置时返回 501。这样 Go 服务可以
直接接管域名而不牺牲现有功能，再按域逐步“关掉代理”。

**验证**：`go build ./...`、`go vet ./...`、`go test ./...` 全部通过（auth / db / api / migrations 共 94 个单测（含黄金用例子测试））；
gofmt / vet 无告警；DDL 的 84 条语句经 sqlglot（MySQL 方言）解析通过。
**验证边界**：本机没有 MySQL 服务（尝试下载便携版失败：网络约 8KB/s），
建表与数据导入**尚未在真实 MySQL 上跑过** —— 首次部署请先在空库执行 `0001_init.sql` 再切流。
已提供 `server/internal/api/integration_test.go`（构建标签 `integration`），
设置 `PP_TEST_DSN` 后可一键验证“迁移 + 登录 + 建分组 + 拉订阅”。

## 6. 分期计划

### P0 · 骨架与认证底座（✅ 完成）

- [x] MySQL DDL（26 表、30+ 索引、种子 admin）
- [x] 迁移执行器（`schema_migrations`、幂等）
- [x] 口令：PBKDF2-SHA256 + 兼容旧 64 位 SHA-256，登录即升级
- [x] JWT：与 Worker 相同算法/载荷（`exp` 毫秒、`role=user`）
- [x] TOTP（RFC 6238，含 skew）
- [x] `/api/login`、`/api/servers`、`/api/servers/status`、`/healthz`
- [x] 501 兜底，前端可灰度
- [x] **过渡期代理**：未迁移接口可整体转发旧 Worker（`PP_LEGACY_BASE`），保证功能不中断
- 验收：编译/单测通过；错误口令 401；未迁移接口 501 或代理 200。

### P1 · 账号与会话（进行中）

- [x] `/api/change-password`
- [x] `/api/mfa/status`
- [x] 用户中心邮箱验证码：`send-code` / `login` / `email/send-code` / `email/verify`
- [x] 验证码频率限制（60s / 每小时 10 条 / 5 次尝试），哈希算法与 Worker 完全一致
- [ ] `/api/login/email-code*`（后台邮箱登录）
- [ ] `/api/admin/users*`
- [ ] MFA：`init / verify-first / backup-codes / disable / login-with-backup`
- [ ] OIDC：discovery、JWKS 缓存、PKCE、id_token 校验、身份绑定
- [ ] API 密钥：`/api/apikeys`、权限位、过期与使用计数
- [x] SMTP 发信（`PP_SMTP_ADDR` / `PP_MAIL_FROM`）
- 验收：`test_roles.mjs`（14 项）、`test_portal.mjs` 中的登录/越权用例全绿。

### P2 · 订阅与配置（进行中）

- [x] `/api/uuids`（列表 + 新建/更新，含邮箱唯一约束）
- [x] `/sub/:uuid`、`/batch-configs/:uuid`（本地 + 外部订阅源合并、访问日志）
- [x] `/api/portal/uuids`（用户端订阅地址）
- [ ] `/api/uuids` DELETE（级联清理节点）
- [ ] `/api/uuid-configs`（配置 CRUD）
- [ ] `/clash-config`（协议渲染，涉及 8 种协议的 YAML 生成）
- [ ] `/manage/*`（配置 CRUD、外部订阅源、统计）
- [ ] 访问统计聚合 / `/api/stats`
- 验收：`test_sub_unit.mjs`（18 项）+ 订阅链接逐字节对比（同一 UUID 输出一致）。

### P3 · 服务器与节点编排（3–4 周）

- [ ] `/api/agent/*`（poll / ack / keycheck / bootstrap / latest）
- [ ] Agent 唤醒：Redis + WS（替代 `AgentHub` DO）
- [ ] `/api/node-templates`、`/api/node-configs`（版本化下发）
- [ ] `/api/singbox/*`（模板、节点、预览、编排）——渲染逻辑最重，建议先做“纯函数 + 黄金用例”
- [ ] `/api/mtproto/*`
- [ ] WebSSH（P3 末期，可暂缓）
- 验收：`test_singbox_unit.mjs`（160 项）、`test_mtproto_api.mjs`（98 项）等价用例。

### P4 · 任务、巡检、文件（1–2 周）

- [ ] `/api/jobs*` 批量任务（MySQL 任务表 + 工作者）
- [ ] Cron：心跳超时、任务回收、UUID 巡检、批量清理
- [ ] `/api/bucket/*`（S3）
- [ ] 通知（邮件 / Telegram）
- 验收：连续运行 72h，任务不丢、不重复执行。

## 7. 数据迁移步骤

1. **冻结写入**（维护窗口）：Worker 置只读（或停写），记录时间点 T0。
2. **导出 D1**：
   ```bash
   npx wrangler d1 export proxypilot --remote --output=d1.sql
   ```
3. **导入临时 SQLite**：`sqlite3 d1.db < d1.sql`。
4. **转换并导入 MySQL**：用 `server/tools/d1_to_mysql.py`（见 `schema-mapping.md`）逐表导出 INSERT，
   或将每表导出 CSV 后 `LOAD DATA LOCAL INFILE`。
5. **校验**（必须全绿）：
   - 每表行数一致
   - `uuid_info.uuid` 无重复、`uuid_info.email_norm` 无重复
   - `configs(uuid, config_data)` 无重复（唯一索引已改为应用层去重）
   - 时间戳均为 13 位毫秒
   - 抽样 20 个 UUID 的订阅输出与 Worker 一致
6. **切换**：DNS/反代把 `/api/*`、`/sub/*` 指向 Go；页面仍由 Worker 提供。
7. **回滚**：DNS 切回 Worker；MySQL 保留快照，Worker 侧 D1 在整个观察期不删。

> 不建议“双写”：D1 没有 binlog/CDC，双写只能靠应用层补偿，复杂度高于停机窗口。

## 8. 兼容性细节（必读）

| 项目 | 约定 |
|---|---|
| JWT | HS256，`exp` 为**毫秒**；后台 `{sub, exp, requires_mfa}`，用户端 `{sub, exp, role:"user"}`；沿用同一个 `JWT_SECRET` 即可让旧 token 继续有效 |
| 口令 | 新格式 `pbkdf2_sha256$210000$salt$hash`；旧格式 `hex(sha256)` 校验通过后自动 UPDATE |
| API Key | `pk_` 前缀、SHA-256 入库；权限位语义不变 |
| Agent Token | `ag_` 前缀；哈希鉴权；AES-GCM 密文列 `agent_token_enc` 保留，密钥派生算法需在 P3 复刻（HKDF-SHA256，salt `proxypilot-agent-key-v1`，info `agent-token-v1`） |
| 时间戳 | 统一 Unix 毫秒 `BIGINT`；旧数据中秒级值需 ×1000 |
| JSON 字段 | 保持 Worker 原样（snake_case 与少量 camelCase 混用，如 `latencyMs`、`checkedAt`） |
| 错误体 | `{"error":"..."}`；401/403/404/409/429 语义保持一致 |

## 9. 运行

```bash
cd server
cp .env.example .env          # 填 DSN、JWT_SECRET
systemctl start mysql            # 使用本机 MySQL 8
set -a; . ./.env; set +a
go run ./cmd/proxypilot
curl -s localhost:8080/healthz
```

生产建议 systemd 或容器；`PP_AUTO_MIGRATE=1` 时启动自动执行未应用的迁移。

| 变量 | 作用 |
|---|---|
| `PP_MYSQL_DSN` | MySQL 连接串（必填） |
| `PP_JWT_SECRET` | 与 Worker 共用即可复用旧 token；等于默认值会拒绝启动 |
| `PP_LEGACY_BASE` | **过渡期代理**：未迁移的 `/api/*`、`/sub/*`、`/manage*` 转发到该地址；为空则 501 |
| `PP_SMTP_ADDR` / `PP_SMTP_USER` / `PP_SMTP_PASS` / `PP_MAIL_FROM` | 验证码邮件；未配置时发送接口返回 503 |
| `PP_OIDC_DISABLE_PASSWORD`（或 `OIDC_DISABLE_PASSWORD`） | 为 true 时禁用密码登录，只留 SSO |
| `PP_AGENT_KEY_SECRET`（或 `AGENT_KEY_SECRET`） | Agent 接入令牌的 AES-GCM 加密密钥，为空回落 `PP_JWT_SECRET` |
| `PP_AGENT_INSTALL_BASE`（或 `AGENT_INSTALL_BASE`） | 安装脚本分发基址（对象存储）；为空用本站 `/api/agent/install.sh` |
| `PP_TURNSTILE_SECRET` | 为空则跳过人机验证 |
| `PP_ADDR` / `PP_SUPER_ADMIN` / `PP_AUTO_MIGRATE` / `PP_DEV` | 监听地址 / 超管名 / 自动迁移 / 本地短密钥 |

## 10. 风险

| 风险 | 影响 | 对策 |
|---|---|---|
| MySQL 表达式默认值需 8.0.13+ | 低版本启动失败 | 文档标注；5.7 改为 `DEFAULT 0` + 应用赋值 |
| 大小写敏感排序规则变更 | 用户名/配置比对行为变化 | 已统一 `utf8mb4_0900_as_cs`，与 SQLite BINARY 对齐 |
| 订阅渲染差异 | 客户端不可用 | 逐字节对比 + 黄金用例 |
| Agent 密钥加密不复刻 | 后台看不到明文 | P3 按 HKDF 参数复刻，或强制轮换 |
| 单实例 Cron 重复执行 | 任务重复 | MySQL `GET_LOCK` 选主 |
| WebSSH/DO 语义 | 长连接 | 自建 WS 网关，先只做 Agent 唤醒 |
| 代理模式依赖旧 Worker | 旧 Worker 故障会放大 | 代理只作为过渡；按域关闭，监控 502 |
| 代理超时 60s | Agent 长轮询（25s）可过，WebSSH 不可代理 | WebSocket 升级请求不代理，明确返回 501 |

## 11. 当前进度看板

| 模块 | 状态 |
|---|---|
| Schema / 迁移器 | ✅ 26 表 + 幂等迁移 |
| 认证（口令/JWT/TOTP） | ✅ 含旧哈希自动升级 |
| 后台登录 / 改密 / MFA 状态 | ✅（支持禁用密码登录） |
| 用户中心邮箱验证码登录 | ✅（需 SMTP） |
| 订阅 `/sub` / `/batch-configs` | ✅ 本地 + 外部源 + 缓存 + 摘要头 |
| 分组 `/api/uuids` | ✅ 列表 / 保存 / 删除 |
| 服务器管理 | ✅ 列表 / 状态 / 新增 / 编辑 / 删除 / 轮换密钥 |
| 模板 `/api/templates` + manage | ✅ 列表 / 增删改 / 设为默认 |
| 统计与日志 | ✅ dashboard 聚合 + 日志清理 |
| 配置管理 | ✅ 后台 / 用户中心 / 外部订阅源 |
| 账号体系 | ✅ API 密钥 / 管理员 / MFA |
| Clash 渲染 | ✅ 9 种协议 + 模板注入（黄金用例） |
| sing-box | ✅ 渲染 + 模板/节点 CRUD + preview（下发待接） |
| Agent 接入 | ✅ keycheck / latest / poll / ack（未经真实 Agent 验证） |
| API 密钥鉴权 | ✅ 权限位 + 明文兼容 |
| 其余接口（节点、Agent、任务、Clash、统计…） | 🔁 代理旧 Worker（未配置则 501） |
| 数据迁移工具 | ✅ `tools/d1_to_mysql.py` |
| 前端 | 不变 |
| Agent | 不变 |
