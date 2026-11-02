# 功能缺口检查（Worker → Go）

> 生成时间：2026-10-31
> 方法：静态解析 `workers/worker.js` 的路由与处理逻辑，逐项对照 `server/internal/api/`
> 的注册路由与实现；同时核对 `web-antd` 的调用契约与 MySQL schema。
> 可复现：`python3 server/tools/check_coverage.py --repo .`

## 0. 总览

| 指标 | 数值 |
|---|---|
| worker.js 路径（含正则动态子路由） | 150 |
| 原生实现 | 143 精确 + 7 通配（共 150，另有 Go 新增 `/healthz`） |
| 原生实现（动态子路由） | 全部（`{id}` / `:uuid` 均已注册） |
| 覆盖率 | **100%**（150/150，含动态子路由） |
| 未迁移路径 | 0 |
| 代理兜底 | 仍保留：未注册路径在配置 `PP_LEGACY_BASE` 时转发，否则 501 |

> 注：`docs/migration/routes.md` 里的 133 是“方法 × 路径 × 动态路由”展开后的条数；
> 这里按唯一路径统计，避免重复计数。

| 域 | 路由数 | 原生 | 未迁移 |
|---|---:|---:|---:|
| 页面 / 静态 | 10 | 10 | 0 |
| 订阅输出 | 3 | 3 | 0 |
| 安装脚本 | 3 | 3 | 0 |
| 用户中心 | 7 | 6 | 1 |
| 分组 / 配置管理 | 19 | 15 | 4 |
| 账号与安全 | 17 | 17 | 0 |
| 服务器 / Agent | 21 | 18 | 3 |
| 节点编排 | 34 | 28 | 6 |
| 批量任务 | 3 | 2 | 1 |
| 配置 / 统计 / 存储 | 12 | 12 | 0 |

## 0.1 本轮已补齐的缺口

| # | 原缺口 | 现状 |
|---|---|---|
| 1 | 原生路由不支持 `X-API-Key` | ✅ 已实现鉴权 + 权限位（`groups:read/write` 等），含历史明文密钥兼容、`last_used_at/usage_count` 更新 |
| 2 | `DELETE /api/uuids` 缺失 | ✅ 已实现：配置、元信息、外部订阅源、MTProto 授权的库内级联；节点重渲染交由巡检 |
| 3 | 外部订阅无缓存、无摘要头 | ✅ 120s 进程内缓存 + `X-External-Sources/Nodes/Errors/Unsupported` |
| 4 | `OIDC_DISABLE_PASSWORD` 未生效 | ✅ 支持 `PP_OIDC_DISABLE_PASSWORD` / `OIDC_DISABLE_PASSWORD`，命中时密码登录 403 |
| 5 | 服务器只能读 | ✅ 新增/编辑/删除/详情/轮换接入令牌（含 AES-GCM 可逆加密与安装命令） |
| 6 | `/api/templates` 未实现 | ✅ 公开模板列表（分页/搜索/排序） |
| 11 | Agent 接入鉴权 / keycheck / latest 缺失 | ✅ 接入令牌鉴权（Bearer / X-Agent-Token / body / query）、`/api/agent/keycheck`、`/api/agent/latest`（manifest 5 分钟缓存）、Agent 版本比较 |
| 12 | Agent poll / ack 未迁移 | ✅ 已实现：节点渲染层（占位符 / NODES / PROXIES_YAML / 模板合并）+ `POST /api/agent/poll`（心跳、指标、速率、任务下发、wire 协议）+ `POST /api/agent/ack`（结果回写、`applied_version`、日志、重试/跳过）。**尚未用真实 Agent + MySQL 验证**，切流前必须在测试机回归 |
| 13 | sing-box 渲染核心未迁移 | 🟡 已移植 `sb.json` 渲染与端口计算（ss2022 / anytls / hysteria2 / tuic / vless-reality / vless-ws / masque，含端口冲突回落与 protocol_options 覆盖），7 份黄金用例逐字节一致（sb / xray / links / ports）；**模板/节点 CRUD + UUID + preview 已迁移**（含密钥自动轮换、REALITY 公钥推导、CDN 校验、渲染前规则校验）；sing-box 域已收口：CRUD / preview（模板+节点）/ apply 系列 / push / reconcile / releases / from-config / WARP 全部原生（同步执行，未走批量任务） |
| 14 | 批量任务未迁移 | 🟡 `/api/jobs` 列表/创建/详情/取消/重试已迁移；内置 kind 仅 `singbox.apply_all` / `singbox.push`，其余 kind 走旧 Worker；执行方式为请求内同步，未接 Cron/Queue |
| 15 | MTProto 未迁移 | 🟡 渲染内核（黄金用例）+ 模板/节点 CRUD + apply（写 node_configs 并入队）+ 客户端列表/生成/链接/rotate/吊销/恢复 + 节点预览 + 可用 UUID；**用户端 Telegram 页、reconcile、模板 apply/preview 未迁移** |
| 10 | Clash 渲染缺失 | ✅ `/clash-config` 原生实现：9 种主流协议的 URI→Clash 对象转换（vmess/vless-reality/vless-ws/vless-xhttp/trojan/ss2022/hysteria2/tuic/anytls）、模板 `proxies:` 注入、去重与重命名、协议统计与 `X-*` 响应头；黄金用例由 worker.js 自身生成（`workers/gen_clash_fixtures.mjs`） |
| 9 | 账号体系未收口（API 密钥 / 管理员账号 / MFA 写操作） | ✅ `/api/apikeys` CRUD + 轮换、`/api/admin/users` CRUD（超管，50 账号上限/邮箱唯一/自删保护）、`/api/mfa/init|verify-first|disable|backup-codes/regenerate|login-with-backup` |
| 8 | 配置管理 / 用户中心配置接口缺失 | ✅ `/api/uuid-configs` CRUD（协议识别 + 备注解析 + 去重）、`/manage/configs` 增删改查（用户端归属校验）、`/manage/uuid-sub-sources` CRUD（SSRF 防护 + 上限 10 条） |
| 7 | 模板管理 / 统计 / 日志缺失 | ✅ `templates/manage` CRUD + 设为默认、`/api/stats`（总量/趋势/热门/最新）、`/api/logs/summary`、`/api/logs/cleanup`（二次验密 + 分批删除） |

## 1. 本次检查发现并已修复的缺陷

| # | 缺陷 | 影响 | 修复 |
|---|---|---|---|
| 1 | `email_norm` 生成列直接用 `LOWER(email)` | `uuid_info.email` 是 `NOT NULL`，空串会互相撞唯一索引 → **第二个不填邮箱的分组插入失败** | 改为 `NULLIF(LOWER(email), '')`，空串落成 `NULL`（`migrations/0001_init.sql`） |
| 2 | `POST /api/login` 在 `step=verify_totp` 时跳过 Turnstile，但未校验账号是否真的启用了 MFA | 未启用 MFA 的账号可**绕过人机验证**直接拿 token | 增加 `step=verify_totp && mfaEnabled!=1 → 400`（`login.go`） |
| 3 | 改密要求新密码 ≥ 8 位，而前端表单只要求 ≥ 6 位 | 用户设置 6–7 位密码会被 400 拒绝 | 放宽为 6，与 UI/Worker 一致（`account.go`） |
| 4 | `d1_to_mysql.py` 会导出目标库不存在的遗留表 | 导入时报 `table doesn't exist`，迁移中断 | 内置跳过 `cfips / cf_domains / edgeone_domains / auto_update_settings` |
| 5 | `/api/servers` 的 `last_ip` 未过滤 `"unknown"`、`next_poll_sec` 未收敛 | 页面显示 `unknown`、轮询间隔可能显示 0 | 过滤 `unknown`，`clampInt(10,900)` |

全部修复已通过 `go build / go vet / go test`（24 项）与 sqlglot DDL 解析。

## 2. 阻断级缺口（不配代理就会坏）

| 缺口 | 影响 | 证据 |
|---|---|---|
| ~~46 条接口无原生实现~~ | ✅ 已补齐（150/150，含动态子路由） | `check_coverage.py` |
| ~~页面不由 Go 提供~~ | ✅ 已内嵌 V14.2 bundle + boot 注入，`/admin-app.js|css`、`/assets/world.svg` 均由 Go 提供 | `web_pages.go` |
| ~~WebSocket 不代理~~ | ✅ `/api/agent/ws`、`/api/shell/*` 已原生实现 WebSocket（自研 RFC6455 子集），Agent 唤醒与 WebSSH 可用 | `ws.go` / `shell.go` |
| ~~无 Cron / Queue / Durable Object~~ | ✅ 进程内 Cron；长轮询/唤醒用内存 Hub；批量任务同步执行（不再需要 Queue） | `cron.go` / `agent_wait.go` |
| 邮件通道变更 | Worker 用 Cloudflare `send_email`；Go 用 SMTP。**现可在后台 `/admin/settings` 配置**，未配置时验证码接口 503 | `mail.go` / `settings_*` |

**部署要求**：全部请求指向 Go。只有配置了 `PP_LEGACY_BASE` 时，未注册路径才会转发旧 Worker。
反向代理必须允许 WebSocket Upgrade（`/api/agent/ws`、`/api/shell/*`）。

## 3. 原生接口的剩余行为缺口

| 级别 | 缺口 | 影响 | 证据 |
|---|---|---|---|
| 中 | `DELETE /api/uuids` 只做库内级联，不重渲染节点 | 节点上的 sing-box 授权列表要等巡检才消失 | `uuids_delete.go` 返回 `nodes_updated=0` 并带 `warning` |
| 中 | 登录无失败限流/锁定 | Worker 同样缺失，但迁移后仍是可爆破面 | `login.go` |
| 低 | TOTP 无尝试次数上限 | 6 位码可被在线枚举（窗口 30s，风险有限） | `login.go` |
| 低 | `agent_latest`/`agent_update` 仍恒为空 | 服务器列表的“可升级”提示依赖 Worker manifest | `servers.go` |
| 低 | 邮件模板简化 | 观感差异，不影响送达 | `mail.go` |
| 低 | 无 gzip / ETag | JSON 响应体积偏大 | `response.go` |

### 已核对一致的部分

- `/api/uuids` 列表：CTE 合并历史分组、搜索/状态过滤/分页/排序、`count=node_count` 字段一致。
- `/api/uuids` 保存：邮箱大小写不敏感唯一、409 `email_taken`、`created` 语义一致。
- `/sub`、`/batch-configs`：状态拦截 403、空订阅 404、Base64 输出、访问日志与计数、订阅响应头一致。
- 用户中心五个接口：验证码哈希（固定向量）、频率限制（60s/10 条/5 次）、换绑后换发 token 一致。
- `/api/login`、`/api/change-password`、`/api/mfa/status`：响应字段与前端调用一致。
- `/api/servers` 增删改查：字段名、`agent_token` 一次性返回、`install_command`、401/409 语义与 Worker 一致。
- API 密钥：路由白名单、`groups/configs/templates` 权限位、明文历史密钥兼容与 Worker 一致。

## 4. 数据与运维缺口

| 缺口 | 影响 | 建议 |
|---|---|---|
| **未在真实 MySQL 上执行过 DDL** | 表达式默认值、生成列、索引名等仍属“语法通过、未实跑” | 空库执行 `0001_init.sql` + `PP_TEST_DSN` 集成测试 |
| MySQL 表达式默认值需 ≥ 8.0.13 | 5.7 建表失败 | 文档已标注；5.7 改 `DEFAULT 0` + 应用赋值 |
| 重复邮箱会让唯一索引创建失败 | 迁移中断 | 已提供预检 SQL（`schema-mapping.md` §5.3） |
| 时间戳秒/毫秒混用需手工纠偏 | 统计/排序错乱 | 已提供 `UPDATE ... *1000` 脚本 |
| `configs` 无唯一约束 | 并发重复插入可能产生重复节点 | 应用层“先查后插”（Worker 现有逻辑）；Go 侧写配置前需沿用 |
| 迁移脚本部分失败后重跑 | 可能重复执行 `ALTER` 报错 | 建议按语句记录，或失败后人工确认再重跑 |
| 无自动备份/回滚脚本 | 切流风险 | 切流前 `mysqldump`，D1 保留 7 天 |

## 5. 前端契约核对

| 检查项 | 结果 |
|---|---|
| 改密字段 `oldPassword/newPassword` | ✅ 与 `Security.jsx` 一致（本轮修复了 6 位下限） |
| 用户中心 `send-code` 读取 `res.email` | ✅ 已返回 |
| `/api/mfa/status` 字段 | ✅ `mfa_enabled / last_mfa_login / backup_codes_remaining` |
| 服务器列表字段（`last_ip`、`agent_next_poll_sec` 等） | ✅ 已对齐并修正 `unknown` |
| 订阅地址拼接（`sub.raw/batch/clash`） | ✅ 与 `PortalLogin`/`PortalHome` 期望一致 |
| Clash / 节点 / MTProto / 配置详情字段 | ⏳ 未核对（这些接口仍由代理返回原实现） |

## 6. 建议的收口顺序

~~1. API 密钥鉴权~~ ✅
~~2. `DELETE /api/uuids`~~ ✅（节点重渲染待巡检）
~~3. 外部订阅缓存 + `X-External-*` 头~~ ✅
~~4. `OIDC_DISABLE_PASSWORD`~~ ✅
~~5. 服务器写接口~~ ✅
~~6. 模板管理 / 统计 / 日志~~ ✅
7. **Clash 渲染 → sing-box → MTProto → Agent/任务/对象存储**（按依赖顺序）。
8. **页面托管方案**（Worker 继续托管，或迁到 R2/nginx 静态站），决定 Worker 能否下线。

> 建议把 `check_coverage.py` 接进 CI：原生路由数只增不减，避免“修一个、漏一个”。

## 0.2 行为差异与降级实现（100% 静态路径 ≠ 100% 行为等价）

| 能力 | Worker | Go 实现 | 影响 |
|---|---|---|---|
| 前端页面 / 静态 | 内置构建产物 | 内嵌 V14.2 bundle（从 worker 导出 `app.js/app.css/world.svg`），服务端注入 `window.__PP_BOOT__`（admin/portal）；`PP_WEB_DIST` 仅用于自定义构建 | 无需 npm；与 Worker UI 同一份产物 |
| WebSSH | Durable Object | Go 内置 WebSocket 中继：浏览器 ↔ Go(shellHub) ↔ Agent WS ↔ PTY；`/api/agent/ws` 为真 WS 唤醒，`/api/agent/wait` 为真实长轮询 | 可用；多实例需粘性会话 |
| 对象存储 / Agent 分发 | R2/S3 + Worker 代理 | 本地目录：桶 `PP_BUCKET_DIR`（默认 `server/data/bucket`）；Agent 二进制/manifest 由 `PP_AGENT_DIST_DIR`（默认 `agent/dist`）经 `/agent-dist/*` 直接托管 | 多实例需共享卷；不再依赖 S3，上传/下载均在本机 |
| 通知通道 | Worker 环境变量 | `app_settings` 表 `notify_*` 键，支持 Telegram + Webhook | 需在后台配置 |
| 批量任务 | Queue/DO 异步 | 请求内同步执行（`apply-all`/`push`/`jobs`） | 大批量请求耗时 |
| 邮件 | Cloudflare send_email | SMTP（`PP_SMTP_*`） | 未配置时验证码接口 503 |
| OIDC | 支持 RS256 | RS256 + JWKS + PKCE + nonce；不支持 ES256/EdDSA | 少数 IdP 需换算法 |
| 真实环境 | — | **MySQL 与真实 Agent 尚未实跑** | 上线前必须集成回归 |

## 0.3 检查结论与修复（2026-11-01，Round 1 已补齐）

> 之前的 100% 只统计了字面路径；把 `path.match(/^\/api.../)` 动态子路由纳入后，
> 真实覆盖为 **114/130（88%）**。以下按“真实缺失”与“降级实现”分类。

### A. 缺失的接口（动态子路由 / 方法）

| # | 接口 | Worker 行为 | Go 现状 |
|---|---|---|---|
| 1 | `GET /api/uuids/{uuid}` | 单个 UUID 详情 | 未注册 |
| 2 | `GET /api/uuids/{uuid}/nodes` | 该 UUID 的节点列表 | 未注册 |
| 3 | `GET/POST /api/uuids/{uuid}/configs` | 该 UUID 的配置增查 | 未注册 |
| 4 | `GET/PUT/DELETE /api/uuids/{uuid}/configs/{id}` | 单条配置读写删 | 未注册 |
| 5 | `POST /api/servers/{id}/test` | 连接/Agent 连通性测试 | 未注册 |
| 6 | `POST /api/servers/{id}/monitor` | 触发一次采集 | 未注册 |
| 7 | `POST /api/servers/{id}/exec` | 下发命令（allow_exec） | 未注册 |
| 8 | `GET /api/servers/{id}/metrics` | 指标历史 | 未注册 |
| 9 | `POST /api/servers/{id}/agent-key` | 查看明文接入密钥 | 未注册 |
| 10 | `POST /api/servers/{id}/agent-install` | 生成安装命令 | 未注册 |
| 11 | `POST /api/servers/{id}/tasks` | 服务器任务列表/创建 | 未注册 |
| 12 | `GET /api/node-templates/{id}` | 单模板详情 | 仅 PUT/DELETE |
| 13 | `POST /api/node-configs` | 创建节点配置 | 未注册 |
| 14 | `GET/PUT/DELETE /api/node-configs/{id}` | 单配置读写删 | 未注册 |
| 15 | `POST /api/node-configs/{id}/push` | 单配置下发 | 仅有批量 body 版本 |
| 16 | `GET /api/mtproto/templates/{id}` | 单模板详情 | 仅 PUT/DELETE |
| 17 | `POST /api/mtproto/templates/{id}/apply` | 按模板批量下发 | 未注册 |
| 18 | `POST /api/mtproto/templates/{id}/preview` | 模板级预览 | 未注册 |
| 19 | `POST /api/portal/mtproto/{id}/deliver` | 用户自助获取链接 | 未注册 |
| 20 | `POST /api/portal/mtproto/{id}/send-mail` | 用户自助发邮件 | 未注册 |
| 21 | `DELETE /api/agent/tasks/{id}` | 取消任务（DELETE） | 只有 POST `/{id}/cancel` |
| 22 | `GET/POST /api/shell/browser/{id}`、`/api/shell/agent/{id}` | WebSSH | ✅ 已实现 Go WebSocket 中继（无需 DO） |

### B. 非路由能力缺口

- **Cron**：离线判定、全局任务过期、UUID 巡检、定时对账在 Go 中不存在（仅 Agent 轮询时顺带回收）。
- **Queue / Durable Object**：批量任务异步化、Agent 唤醒、WebSSH 会话。
- **前端静态资源**：`web-antd` 没有构建产物；`/assets/world.svg` 依赖构建后的 `public/`。
- **批量任务 kind**：仅 `singbox.apply_all` / `singbox.push`；`agent.update` 等仍会 400。
- **OIDC**：无管理员 OIDC 身份绑定；仅 RS256。
- **真实环境**：MySQL 迁移与 Agent 协议从未实跑。

### C. 建议顺序

1. 先补 A 表中 1–15（配置/服务器/UUID 管理闭环），再补 16–22。
2. 加 Cron 等价实现（进程内 ticker：离线判定 + 任务过期 + 对账）。
3. 构建前端并做 MySQL + Agent 集成回归。

## 0.4 Round 1 修复记录

- ✅ `/api/uuids/{uuid}`、`/{uuid}/nodes`、`/{uuid}/configs`、`/{uuid}/configs/{id}`
- ✅ `/api/servers/{id}/test|monitor|exec|metrics|agent-key|agent-install|tasks`
- ✅ `GET /api/node-templates/{id}`；`POST /api/node-configs`、`GET/PUT/DELETE /{id}`、`POST /{id}/push`
- ✅ `GET /api/mtproto/templates/{id}`、`POST /{id}/apply`、`POST /{id}/preview`
- ✅ `POST /api/portal/mtproto/{id}/deliver|send-mail`
- ✅ `DELETE /api/agent/tasks/{id}`；`GET /api/shell/{browser|agent}/{id}`（明确 503 降级）
- ✅ 进程内 Cron：心跳离线、sent 任务回收、pending 过期、批量任务锁释放
- ✅ **本地对象存储 + 本地 Agent 分发**：`PP_BUCKET_DIR` / `PP_AGENT_DIST_DIR`，新增 `GET /agent-dist/*`；
  `agentBinaryBaseURL()` 不再默认指向 S3，未配置外部基址时使用 `PP_PUBLIC_BASE_URL + /agent-dist` 或请求 origin；
  manifest 优先读本地文件，安装脚本/升级任务的下载地址均指向本站。

### 生产化补充（2026-11-01 第二轮）

- ✅ **内嵌生产 UI**：`workers/export_web_assets.mjs` 从 worker 导出 `app.js/app.css/world.svg`，
  Go 直接托管 `/admin-app.js`、`/admin-app.css`、`/assets/world.svg`，并按路径注入 admin/portal boot。
- ✅ **部署产物**：`server/Dockerfile`、`docker compose`（mysql + server）、`.env.example`、README「生产部署（替换域名）」。
- ✅ **安全**：基础安全响应头；OIDC Cookie 按 `X-Forwarded-Proto` 设置 Secure；管理员邮箱绑定仅超管；
  `PP_LEGACY_BASE` 自环保护。
- ✅ **本地分发**：Agent 二进制/manifest 走 `/agent-dist/*`，安装/升级 URL 全部指向本站。

### 后台可配置化（2026-11-01 第三轮）

- ✅ `/admin/settings` 管理页 + `GET/PUT /api/settings`（仅超管）
- ✅ 登录方式（密码/邮箱验证码/OIDC）、Turnstile、SMTP、OIDC、站点地址均可在后台配置，
  存 `app_settings`，30 秒缓存 + 保存即失效；环境变量作为回落默认值
- ✅ 登录锁定保护：保存时校验至少一种可用登录方式
- ⚠️ WebSSH 仍依赖 Durable Object；批量任务仍同步执行
