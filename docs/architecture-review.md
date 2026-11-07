# 架构落地评审（Go + MySQL + Ant Design）

> 评审对象：`feat/go-backend-migration` 分支
> 结论先行：**静态路由覆盖 150/150 ≠ 生产可用**。核心业务链路已具备，但存在 3 类问题：
> ① 与现有 Ant Design 前端的 JSON 契约不一致（会直接坏页面）；
> ② 若干 Worker 能力尚未实现；
> ③ 部署/多实例/安全仍需工程化。

## 一、P0：上线前必须修（会阻断或坏页面）

| # | 问题 | 影响 | 证据 |
|---|---|---|---|
| 1 | `/api/monitor/notify` 契约不一致 | 前端监控通知页读取 `email_ready / enabled / emails / cooldown_min`，Go 实现是 Telegram/Webhook 字段；页面保存后无效 | `monitor.go` vs `assets_web/app.js` |
| 2 | `/api/monitor/uuid-check` 契约不一致 | 前端 POST 期望 `job_id`（或 `checked/fixed`），GET 期望 `last_at/last_result/interval_min`；Go 只有 GET 且返回 `total/problems/data`，巡检按钮不可用 | `monitor.go` |
| 3 | `/api/servers/monitor` 契约不一致 | 前端读取 `data.servers[]`（含 `ssh`），Go 返回 `data` 数组 + `summary`，服务器矩阵页会空 | `misc_routes.go` |
| 4 | `node_configs.install_versions` 列缺失 | sing-box/xray 模板选择的引擎版本无法随 deploy 下发，Agent 无法按版本安装/切换 | `0001_init.sql`、`singbox_apply.go` |
| 5 | WebSocket 无保活/Origin 校验 | `keepAlive()` 已写但未启动；反代/空闲会断；缺少 Origin 校验存在跨站 WS 风险 | `ws.go`、`shell.go` |
| 6 | 后台设置的 Secret 明文入库 | SMTP 密码、OIDC Secret、Turnstile Secret 存 `app_settings` 明文；DB 泄露即凭据泄露 | `settings_store.go` |
| 7 | 未在真实 MySQL / 容器方案 / Agent 上验证 | 全部 SQL、镜像构建、Agent 协议仅静态/单测验证 | — |


### P0 修复状态（2026-11-01）

| # | 修复 | 验证 |
|---|---|---|
| 1 | `/api/monitor/notify` 改为邮件语义：`email_ready/enabled/emails/cooldown_min`，PUT 校验至少一个邮箱，测试发信 | `normalizeEmailList` 单测 |
| 2 | `/api/monitor/uuid-check`：GET 返回 `last_at/last_result/interval_min`；POST 同步巡检并修复孤儿 UUID，记录结果 | 单测 + 构建 |
| 3 | `/api/servers/monitor`（含 `/services` 别名）返回 Worker 同构：`servers/services/watch/matrix/summary`，含 `ssh` 字段 | 构建 |
| 4 | 新增迁移 `0002_node_config_install_versions.sql`；sing-box apply 写入 `install_versions`，deploy payload 携带，Agent 可安装指定版本 | `singboxInstallVersions` 单测 |
| 5 | WebSocket 启动 ping 保活；浏览器端增加 Origin 校验（同源或 `PP_PUBLIC_BASE_URL` 同域） | 构建 |
| 6 | 后台敏感设置（SMTP 密码 / OIDC Secret / Turnstile Secret）使用 AES-GCM 加密存储，读取自动解密 | `isSecretSetting` 单测 |
| 7 | 真实 MySQL / 容器方案 / Agent 回归 | ❌ 仍需部署机完成 |

> 说明：已经以明文保存的旧 Secret 会在下次保存时自动转为密文；未重新保存前仍按明文读取。

## 二、P1：功能缺口（Worker 有、Go 没有或降级）

| 能力 | 现状 | 需要做什么 |
|---|---|---|
| 批量任务 kinds | ✅ 已补齐 7 种：singbox.apply_all / singbox.push / mtproto.apply_all / mtproto.template_apply / agent.update / ssh.toggle / uuid.reconcile | 后台 goroutine + 抢占锁，不再阻塞请求 |
| Cron 完整性 | ✅ 离线/恢复邮件通知、任务回收/过期/保留清理、批量任务恢复与清理、UUID 巡检（按间隔）、指标保留 | — |
| 管理员 OIDC 身份 | ✅ 已实现 Worker 授权模型：`(issuer,sub)` 绑定 → 已验证管理员邮箱自动绑定 → 拒绝；回调签发管理员 JWT 并跳 `/admin#token=` | — |
| 系统设置 UI | ✅ 已并入 AntD 后台导航「系统 → 系统设置」；覆盖登录方式、Turnstile、SMTP、OIDC/SSO、站点/Agent、通知与保留期 | 可补 SMTP/Turnstile 测试按钮 |
| 通知渠道 | ✅ 邮件语义（Worker 兼容）；离线/恢复按冷却时间发信 | — |
| 服务器 action 语义 | ✅ `test`/`monitor` 排队 `probe`；`exec` 修复 timeout 单位（ms→秒，2–60s）并返回 queued；`metrics` 支持 hours 过滤 | — |
| WebSSH 运维能力 | 可交互；无审计、无空闲超时、无只读模式 | 会话录制（可选）、空闲断开、审计日志、权限细分 |
| 多实例 | shellHub / waitHub 在进程内存；settings 缓存 30s | 共享会话（Redis/DB 租约）或强制按 `server_id` 粘性；设置变更广播 |
| 前端构建链 | 容器方案已移除；`web-antd` 产物由 `scripts/build.mjs` 嵌入 Go 二进制 | CI 构建 AntD 产物后再编译 Go，无需额外挂载 |
| D1 → MySQL 迁移 | 工具存在但未对真实导出验证 | 用真实 D1 导出跑通，校验行数/字符集/JSON/时间戳 |
| 设置覆盖面 | 登录/Turnstile/SMTP/OIDC/站点/Agent 基址可配 | 补：通知邮件、轮询间隔、日志保留、任务并发、WebSSH 开关等 |


### 第二批实现（2026-11-01）

| 能力 | 实现 |
|---|---|
| 批量任务 | 7 种 kind 全部注册；`startBatchJob` 后台 goroutine + `batchRunSem` 限流；`queued` 抢占锁防止 cron/API 重复执行；取消在任务项之间生效 |
| 任务恢复 | Cron 每 10 分钟回落 `queued` 任务（最多 2 个）；`running` 锁超过 5 分钟自动释放并重新排队 |
| Cron | 心跳按 `agent_next_poll_sec × 3` 自适应（30s–15m）；离线/恢复邮件通知（冷却可配）；agent 任务过期/未回执/保留清理；批量任务与项保留清理；`server_metrics` 保留清理 |
| UUID 巡检 | `reconcileSingboxUUIDState` 统一供 `/api/monitor/uuid-check`、`uuid.reconcile` 任务、Cron 使用：校验节点配置用户、订阅配置缺失，autoFix 重渲染 + 同步 configs + 排队下发 |
| 管理员 OIDC | 回调解析 `(issuer,sub)`/已验证邮箱，自动绑定 `admin_oidc_identities`，签发管理员 token 跳转 `/admin`；OIDC 启动校验 Turnstile |
| 新增设置项 | `batch_retention_days`、`agent_task_retention_days`、`metrics_retention_days`（默认 7 天） |

> 说明：这些设置目前通过 `app_settings`/API 配置，尚未加入 `/admin/settings` 页面。


### 第三批实现（2026-11-01）

| 能力 | 实现 |
|---|---|
| 服务器动作 | `test` / `monitor` 改为排队 `probe` 任务（与 Worker 一致）；`exec` 修复 timeout 单位并限制 2–60 秒；`metrics` 支持 `hours` 过滤并倒序 |
| 接入密钥查看 | `GET/POST /servers/{id}/agent-key` 对齐前端：`agent_token`、`token_hint`、`install_command`、`disable_ssh`；密钥不可回溯时返回结构化 409 |
| 设置页扩展 | 新增“通知 / 巡检 / 保留”分组：离线通知开关、收件邮箱、冷却、UUID 巡检间隔、批量/Agent 任务/指标保留天数 |
| 设置校验 | 邮箱列表规范化；数值范围校验（冷却 1–1440、巡检 5–1440、保留 1–365）；空值表示回落默认 |


### 第四批实现（2026-11-01）

- AntD 管理后台新增「系统设置」页（`#settings`，仅超管）：
  - 登录方式开关、Cloudflare Turnstile Site/Secret
  - SMTP、离线通知与冷却
  - OIDC / SSO 全部参数（Issuer、Client、Scopes、显示名、回调）
  - 对外地址、Agent 分发基址、UUID 巡检间隔、任务/指标保留期
- Secret 字段留空保持不变；保存后 30 秒内生效；前端同步构建进内嵌 bundle（`app.js`）。
- 前端版本号同步 V15.0；Worker 保留下仅更新内嵌静态资源。

## 三、需要调整的架构点

1. **前端单一来源**：当前 `web-antd/src`（主）与 `assets_web` 内嵌快照（兜底）双轨。
   需要明确发布流程：CI 构建 `web-antd/dist` → 镜像/挂载；快照只用于零构建启动，不再手改。
2. **设置页与 SPA 分离**：独立页可用但体验割裂，应并入 AntD 路由并复用权限/布局。
3. **Notify 语义统一**：邮件（Worker 兼容）为主，Telegram/Webhook 为可选，避免两套字段。
4. **WebSocket 层补齐**：启动 ping 保活、Origin 白名单、连接数限制、优雅关闭（shutdown 时通知所有会话）。
5. **Agent 唤醒覆盖**：目前任务入队/终端打开会唤醒；模板保存、批量操作等也应 `wakeAgent`。
6. **配置读取一致性**：JWT/DB/目录继续走 env；业务开关走 `app_settings`；避免新增代码直接读 `cfg`。
7. **事务边界**：配置/节点/客户端多表写入目前多为独立语句；需要事务包裹（如 push、apply、删除级联）。

## 四、需要优化（性能 / 安全 / 运维）

| 类别 | 问题 | 建议 |
|---|---|---|
| 性能 | `singboxNodesList` / `mtprotoNodesList` 每行再查模板（N+1） | JOIN 或一次性 map 缓存 |
| 前端体积 | `app.js` 3.6MB 未压缩，依赖反代 gzip | 镜像内预压缩 `.gz/.br` + 正确 `Content-Encoding`，或强依赖 Caddy 压缩 |
| 安全 | CORS `*`；无登录失败限速；JWT 无法撤销 | 收敛 Origin；登录限速/锁定；会话版本号 |
| 安全 | Turnstile 不可用时登录直接失败 | 超时/熔断 + 后台“关闭人机验证”开关（已有） |
| 安全 | API Key 权限位已实现，但缺少审计 | 记录 key 使用（已有 usage_count）+ 管理页展示 |
| 可观测 | 只有 JSON 日志，无 request id / 指标 | 中间件 request id、慢查询日志、Prometheus/expvar |
| 数据库 | 无外键；部分表缺少组合索引 | 审计热点查询，补索引；迁移脚本记录 |
| 任务 | 同步执行占用请求（WriteTimeout 180s） | 异步 job worker + 进度查询 |
| 运维 | 无自动备份/恢复脚本 | `mysqldump` + bucket/agent 目录快照；文档化 |
| 健康检查 | 仅 `/healthz` 含 DB Ping | 增加 `/readyz`（迁移完成/依赖就绪） |

## 五、验证缺口（本仓库内无法完成）

1. **MySQL**：`PP_TEST_DSN=... go test -tags integration ./internal/api -run TestIntegrationLoginAndSub`
2. **D1 → MySQL**：真实导出导入 + 行数/内容对拍
3. **安装脚本**：`deploy/install.sh`（在目标机验证 MySQL 初始化与 systemd 托管）
4. **真实 Agent**：keycheck → bootstrap → poll/ws → deploy → ack → WebSSH
5. **外部服务**：SMTP、OIDC（含 id_token 验签）、Turnstile
6. **反向代理**：TLS + WebSocket Upgrade（`/api/agent/ws`、`/api/shell/*`）
7. **多实例**：粘性路由或共享会话验证


## 真机部署验证（2026-11-01）

环境：Ubuntu 26.04 / MySQL 8.4.11 / systemd / 对外 80 端口，Go 服务以 `proxypilot` 用户 +
`CAP_NET_BIND_SERVICE` 运行，数据库迁移 0001–0005 自动执行。

### 已通过的验证

| 项 | 结果 |
|---|---|
| `/healthz`、`/admin`、`/admin-app.js|css`、`/assets/world.svg` | 200 |
| boot 注入 | `mode=admin`、`version=V15.0` |
| `/login` → `/admin`、`/manage` → `/` | 302 |
| 错误密码登录 | 401 |
| 正确登录 + `/api/stats`、`/api/servers`、`/api/node-configs`、`/api/singbox/*`、`/api/mtproto/*`、`/api/jobs`、`/api/logs/summary`、`/api/monitor/*` | 200 |
| UUID 新建 / 详情 / 列表 / 删除 | 通过 |
| 服务器新建 / 详情 / 轮换密钥 / 查看密钥（含 `--disable-ssh`）/ 删除 | 通过 |
| 设置读取与保存 | 通过 |
| sing-box 模板新建（含 `singbox_version` / `xray_version`）/ 列表 / 删除 | 通过 |
| 模拟 Agent：`keycheck` → `poll`（指标入库）→ `test`（probe 入队）→ `poll` 取任务 → `ack` | 通过 |
| 订阅占位 UUID | 404 |

### 真机暴露并已修复的问题

| # | 问题 | 修复 |
|---|---|---|
| 1 | MySQL 8.4 中 `groups` 是保留字，`WITH groups AS` 语法错误 | CTE 改名 `uuid_grp` |
| 2 | `servers.link_mode` 列缺失，服务器创建 500 | 迁移 `0003_servers_link_mode` |
| 3 | `server_metrics` 缺少网络/磁盘/连接等扩展列，采样写入失败 | 迁移 `0004_server_metrics_extended` |
| 4 | `singbox_templates` 缺少 `singbox_version` / `xray_version` | 迁移 `0005_singbox_template_versions` |
| 5 | 空库首次保存设置被“至少保留一种登录方式”误拦截 | 校验改用 env/默认值兜底 |
| 6 | boot 仍显示 V14.2 | `assets_web/meta.json` 同步 V15.0 |

### 仍需验证

- 真实浏览器交互（登录页、任务抽屉、终端）
- SMTP / OIDC / Turnstile 真实凭据
- 真实 Agent 二进制安装、deploy 落盘与 WebSSH
- D1 导出 → MySQL 导入

## 六、建议路线图

### P0（上线前）
1. 修 3 个前端契约（notify / uuid-check / servers-monitor）
2. 补 `install_versions` 列并下发
3. WS 保活 + Origin 校验
4. 设置 Secret 加密存储
5. 真实 MySQL + 容器方案 + Agent 回归

### P1（上线后 1–2 周）
1. 批量任务异步化 + 补 kind
2. Cron 巡检/清理/通知
3. 设置页并入 AntD + 通知邮件语义
4. 管理员 OIDC 身份
5. 多实例会话方案

### P2（持续）
1. N+1 / 压缩 / 索引 / 事务
2. 可观测性与备份
3. WebSSH 审计与只读
4. 契约测试（直接以 `web-antd` 调用为准）
