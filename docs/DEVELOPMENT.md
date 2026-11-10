# ProxyPilot 开发文档

> 版本：V35.3 / Agent 2.1.1 / 客户端 2.7　最后更新：2026-11
>
> 读者：接手后端、前端、Agent、桌面客户端开发的同学。
> 协议字节级细节见 [readme 的「VPN 与组网协议栈」](../readme.md#vpn-与组网协议栈)；
> DNS 泄露专题见 [DNS-LEAK.md](DNS-LEAK.md)。

---

## 1. 项目概览

ProxyPilot 是一套「面板 + Agent + 客户端 + 自研隧道」的代理编排与 VPN 系统：

```
浏览器 ──▶ 面板（Go，单二进制，内嵌前端）
             │  MySQL 8
             │  Agent 出站轮询（/api/agent/*）
             ▼
        节点 Agent（Go，systemd）── 内嵌 HTTP/3 隧道节点（UDP 443）
             │
             ▼
     桌面客户端（Wails v3 / CLI，Windows·macOS·Linux）
```

| 组件 | 语言/框架 | 产物 | 版本 |
|---|---|---|---|
| 面板 | Go 1.26 + MySQL 8 | `proxypilot-linux-{amd64,arm64}` | V35.3 |
| 前端 | React 18 + Ant Design 6 + esbuild | 内嵌进面板二进制 | — |
| Agent | Go 1.24（toolchain 1.27.3） | `proxypilot-agent-linux-*` | 2.1.1 |
| 隧道节点 | 同 masque 包，**内嵌进 Agent 进程** | 无独立二进制 | — |
| 客户端 | Go + Wails v3（桌面）/ CLI | `proxypilot-vpn-*-install.exe` 等 | 2.7 |

---

## 2. 仓库结构

```
proxypilot/
├── server/                  # 面板后端
│   ├── cmd/proxypilot/      # 入口（版本常量在这里）
│   ├── internal/api/        # 367 条路由 + 业务实现（最大的一块）
│   ├── internal/auth/       # JWT / PBKDF2 / TOTP
│   ├── internal/config/     # 环境变量解析
│   ├── internal/db/         # 连接池 + 迁移执行器
│   ├── internal/vpn/        # VPN 领域模型（CA、节点、授权）
│   ├── internal/warp/       # Cloudflare WARP 客户端
│   ├── migrations/          # 40 个增量 SQL（启动自动执行）
│   └── testdata/            # 黄金用例（Clash/sing-box/MTProto）
├── web-antd/                # 前端源码
│   ├── src/pages/           # 29 个页面
│   ├── src/shell/           # 布局与导航
│   ├── scripts/build.mjs    # esbuild 构建 + 同步进 server/internal/api/assets_web/
│   └── dist/                # 构建产物（gitignore）
├── agent/                   # 节点 Agent
│   ├── cmd/proxypilot-agent/
│   ├── internal/agent/      # 心跳、指标、任务循环（版本常量）
│   ├── internal/client/     # HTTP 客户端 + 任务执行
│   ├── internal/runner/     # deploy / exec 两类任务
│   ├── internal/vpnnode/    # 内嵌 HTTP/3 隧道节点
│   └── dist/                # 构建产物 + manifest.json
├── masque/                  # 隧道节点实现（被 agent 以 replace 引用）
│   └── node/                # server.go / stream.go / tun_*.go / jwt.go
├── vpnclient/               # 桌面 + CLI 客户端
│   ├── main.go              # CLI 入口、版本常量
│   ├── entry_desktop_windows.go  # Wails 入口（托盘、窗口）
│   ├── ui.go                # 本地控制台 HTTP 服务 + 连接状态机
│   ├── ui/index.html        # 客户端界面（单文件，内嵌）
│   ├── route_*.go           # 三平台网卡/路由/DNS
│   ├── tunnel.go / stream_tunnel.go / stream_dial.go  # 数据面
│   └── assets/              # app.ico、wintun dll
├── deploy/                  # 安装/升级/卸载脚本、web-guard、maintenance
├── scripts/build-release.ps1# 一体化发布构建
├── docs/                    # 本文档与专题
└── rule/                    # Clash / Mihomo 模板与规则
```

**四个独立 Go module**：`server`、`agent`、`masque`、`vpnclient`。
Agent 通过 `replace github.com/yzfn/proxypilot/masque => ../masque` 引用隧道实现，
改 masque 必须同时重建 Agent。

---

## 3. 本地开发环境

| 依赖 | 版本 | 说明 |
|---|---|---|
| Go | 1.27.3（toolchain） | server/vpnclient 声明 1.26，agent 声明 1.24 |
| MySQL | 8.0 | 面板必需 |
| Node | ≥ 18 | 前端构建（esbuild，无 webpack） |
| PowerShell | 7+ | 发布脚本 `build-release.ps1` |
| go-winres | 最新 | 生成 Windows 资源（图标/清单/版本号） |

```bash
# 1) 数据库
mysql -uroot -e "CREATE DATABASE proxypilot CHARACTER SET utf8mb4"

# 2) 后端（开发模式，自动迁移）
cd server
export PP_MYSQL_DSN='root@tcp(127.0.0.1:3306)/proxypilot?parseTime=true&loc=Local'
export PP_DEV=1 PP_JWT_SECRET=$(openssl rand -hex 32) PP_ADDR='127.0.0.1:8080'
go run ./cmd/proxypilot

# 3) 前端（开发时可用磁盘产物，免重编译后端）
cd web-antd && node scripts/build.mjs
# 或让后端直读磁盘：PP_WEB_DIST=/abs/path/web-antd/dist

# 4) Agent（本机调试，指向本地面板）
cd agent
go run ./cmd/proxypilot-agent -config ./test.conf -v
```

---

## 4. 后端（server）

### 4.1 启动流程（`server/cmd/proxypilot/main.go`）

```
读环境变量 → 连接 MySQL → 执行 migrations（PP_AUTO_MIGRATE）
   → api.Version = version            ← 版本注入点
   → EnsureAdminPassword              ← 首次生成 admin 口令
   → EnsurePersistentSecret(jwt_secret / agent_key_secret)
   → ReloadCDNPreferred
   → EnsureLocalServer                ← 本机服务器写入 local_server 表
   → StartLocalMonitor                ← 每 5s 采集本机指标
   → 启动 HTTP 服务
```

关键约定：

- **版本号单一来源**：`main.go` 的 `var version = "V35.3"`，前端 `meta.json` 与页面加载动画都从它派生
- `api.Version` 默认为空，运行时由 main 赋值（避免出现写死的旧版本）
- 密钥第一次启动时生成并写入 `app_settings`，重启不失效

### 4.2 目录分层

| 目录 | 职责 |
|---|---|
| `internal/api` | 路由注册（`router.go`）、业务 handler、WebSocket、内嵌前端资源 |
| `internal/auth` | JWT 签发/校验、PBKDF2 口令、TOTP |
| `internal/config` | 环境变量 → `config.Config` |
| `internal/db` | `sql.DB` 连接池、迁移执行器 |
| `internal/vpn` | VPN 领域模型：CA、节点、授权、令牌 |
| `internal/warp` | Cloudflare WARP 注册与配置生成 |

`internal/api` 内的文件按业务域切分，命名即职责：

```
agent*.go        Agent 注册/轮询/任务/升级
servers*.go      服务器 CRUD 与状态
singbox*.go      sing-box 节点与模板
mihomo*.go       mihomo 模板/节点/流量
mtproto*.go      MTProto 客户端与模板
vpn_admin.go     VPN 节点管理（增删改查、授权、部署）
vpn_connect.go   客户端换票接口
vpn_cert.go      ACME 证书签发与下发
vpn_ca.go        内部 CA
vpn_mesh*.go     组网成员与心跳
local*.go        本机服务器（独立表）
shell*.go        远程终端（WebSocket + PTY）
```

### 4.3 配置项（环境变量）

| 变量 | 必填 | 说明 |
|---|---|---|
| `PP_MYSQL_DSN` | ✅ | `user:pass@tcp(host:port)/db?parseTime=true` |
| `PP_ADDR` | | 监听地址，默认 `:80` |
| `PP_JWT_SECRET` | ✅（首次自动生成） | 后台会话签名密钥，≥32 字节 |
| `PP_AGENT_KEY_SECRET` | ✅（首次自动生成） | Agent 接入令牌密钥 |
| `PP_ADMIN_PASSWORD` | | 首次部署的 admin 口令 |
| `PP_PUBLIC_BASE_URL` | | 对外地址，用于 OIDC 回调与 Agent 分发推导 |
| `PP_AGENT_BINARY_BASE_URL` | | Agent 分发基址（当前指向存储桶 `.../agent`） |
| `PP_AGENT_DIST_DIR` | | 本地 Agent 分发目录，默认 `/opt/proxypilot/agent-dist` |
| `PP_BUCKET_DIR` | | 附件存储目录，默认 `/opt/proxypilot/bucket` |
| `PP_CDN_BASE_URL` | | CDN 优选下载基址 |
| `PP_R2_*` | | Cloudflare R2（附件与 Agent 发布） |
| `PP_CLOUDFLARE_API_TOKEN` | | ACME DNS-01 签发用 |
| `PP_OIDC_*` | | 单点登录（issuer/client/secret/redirect/scopes） |
| `PP_MAIL_*` | | SMTP 发信（邮箱验证码、登录告警） |
| `PP_LOGIN_ALERT_EMAIL/WEBHOOK` | | 异地登录告警 |
| `PP_AUTO_MIGRATE` | | 启动自动执行迁移（默认开） |
| `PP_DEV` | | 开发模式：放宽密钥长度等校验 |

### 4.4 数据库

**迁移机制**：`server/migrations/NNNN_name.sql`，启动时按序执行，
已执行记录在 `schema_migrations`。**只增不改**，历史文件不要编辑。

当前 40 个迁移。线上 48 张表，按域分组：

| 域 | 表 |
|---|---|
| 账号与安全 | `admin_users`、`admin_email_codes`、`admin_oidc_identities`、`mfa_backup_codes`、`api_keys` |
| 配置与模板 | `configs`、`config_templates`、`node_configs`、`node_templates`、`mihomo_templates`、`mihomo_template_versions`、`mtproto_templates` |
| 服务器与 Agent | `servers`、`agent_tasks`、`server_metrics`、`server_traffic_*`、`shell_sessions`、`local_server`、`local_port_rules` |
| 节点 | `singbox_nodes`、`mihomo_nodes`、`mtproto_nodes`、`node_push_logs`、`node_stats_snapshot`、`node_connections`、`node_user_counter` |
| 用户与流量 | `uuids`（由 configs 聚合）、`user_traffic`、`user_traffic_hourly`、`user_node_recent`、`config_access_logs` |
| VPN | `vpn_nodes`、`vpn_node_grants`、`vpn_node_links`、`vpn_devices`、`vpn_device_codes`、`vpn_sessions`、`vpn_settings`、`vpn_signing_keys`、`vpn_user_access` |
| 证书 | `vpn_ca`、`vpn_certificates`、`vpn_certs` |
| 组网 | `vpn_mesh_nodes` |
| 其他 | `app_settings`、`batch_jobs`、`batch_job_items`、`ip_geo`、`cdn_preferred_addresses`、`masque_warp_*`、`client_auth_codes`、`uuid_port_map`、`schema_migrations` |

**新增迁移的步骤**

1. 新建 `server/migrations/0041_xxx.sql`，用 `CREATE TABLE IF NOT EXISTS` / `ALTER TABLE ... ADD COLUMN`
2. 在 `migrations_test.go` 的用例里补一条（如适用）
3. 本地起服务确认自动执行，再上线

### 4.5 路由与鉴权

全部路由在 `internal/api/router.go`（367 条）。三种鉴权包装：

```go
mux.HandleFunc("GET /api/uuids", h.authed(authOpts{}, h.listUuids))                  // 任意管理员
mux.HandleFunc("POST /api/admin/users", h.authed(authOpts{superOnly: true}, h.xxx)) // 仅超管
mux.HandleFunc("GET /api/portal/me", h.portalAuthed(h.portalMe))                    // 用户端令牌
mux.HandleFunc("POST /api/agent/poll", h.agentAuthed(h.agentPoll))                  // Agent 令牌
```

| 令牌类型 | 前缀 | 可访问范围 |
|---|---|---|
| 管理员 | `/api/*`（除 portal/agent） | 后台全部；`superOnly` 仅超管 |
| 用户 | `/api/portal/*`、`/manage/*` | 仅自己 UUID 的数据 |
| Agent | `/api/agent/*` | 心跳、拉任务、回报结果 |

### 4.6 API 约定

统一 JSON 响应：

```json
{ "success": true, "data": {...} }
{ "error": "错误说明" }
```

- 列表统一 `{ "data": [...], "total": N }`
- 分页参数 `page` / `size`（默认 1 / 10，最大 200）
- 时间戳一律 **毫秒**（`nowMillis()`）
- 异步任务返回 `{"success":true,"async":true,"job_id":N}`，前端轮询 `/api/jobs/{id}`

### 4.7 关键子系统

**节点下发（Agent 任务队列）**

```
面板写 agent_tasks(status=pending) → wakeAgent(serverID)（WebSocket 唤醒）
   → Agent 轮询 /api/agent/poll 领任务 → 执行 → /api/agent/ack 回报
   → 面板更新 status=done/failed + output
```

注意：**让 Agent 重启自己的任务会掐断回报**，必须用 `nohup sh -c 'sleep 1; systemctl restart ...' &` 异步重启。

**批量任务**：`batch_jobs` / `batch_job_items`，`startBatchJob` 逐条下发，前端右上角「任务」抽屉展示进度。

**证书**：`vpn_ca.go`（内部 CA，ECDSA P-256，10 年）、`vpn_cert.go`（ACME DNS-01 通配证书 + 下发脚本）。

**VPN 节点部署**：`vpn_deploy.go` 生成脚本 →
写 `/etc/proxypilot/agent/vpn.json` + `/etc/proxypilot/masque/tls/{cert,key}.pem` → 异步重启 Agent。

**远程终端**：`shell.go` 用 WebSocket 承载 PTY，本机走 `/api/shell/local`，节点走 `/api/shell/browser/{id}`（Agent 隧道转发）。

---

## 5. 前端（web-antd）

### 5.1 技术栈与构建

- React 18 + Ant Design 6，**esbuild 直接打包**（无 Vite/webpack 配置负担）
- `node scripts/build.mjs` 产出 `dist/app.js`、`dist/app.css`，并**自动复制**到 `server/internal/api/assets_web/`
- 面板用 `go:embed` 内嵌，部署只传一个二进制
- 调试期用 `PP_WEB_DIST` 指向磁盘产物，改前端不必重编译后端

### 5.2 目录

```
src/
├── pages/         29 个页面（每个路由一个文件）
├── shell/         AppLayout（侧边栏、头部、任务抽屉）
├── components.jsx 通用组件：PageCard / StatCard / Toolbar / ErrorBlock / CopyText
├── portal/        用户中心（PC 单页 + 移动端 4 页签）
├── nav.jsx        路由元数据 + 分组（PAGE_META / NAV_GROUPS / NAV_ORDER）
├── App.jsx        PAGES 映射 + 权限拦截 + 主题
├── api.js         fetch 封装（自动带 token、统一错误）
├── hooks.js       useAsync / usePolling / useHashRoute / readForm
└── theme.js       色彩 token + LOGO_URL
```

### 5.3 新增一个页面

以「证书管理」为例（`pages/Certs.jsx`）：

1. 写页面组件，用 `PageCard` / `StatCard` 保持风格
2. `nav.jsx` 的 `PAGE_META` 加一项：

```jsx
certs: {
  title: '证书管理',
  subtitle: '本站 CA 与功能证书：签发、下发、下载',
  icon: <SafetyCertificateOutlined />,
  group: '系统',
  accent: '#0ea5e9',
  superOnly: true,
}
```

3. 把 key 加进 `NAV_ORDER` 与 `NAV_GROUPS[...].keys`
4. `App.jsx` 的 `PAGES` 里映射组件
5. `node scripts/build.mjs`

> 坑：图标**必须**在文件顶部 import，只写 JSX 不导入会导致整个页面白屏（React 渲染 undefined 组件）。

### 5.4 数据层约定

```js
// 列表
const { data, loading, error, reload } = useAsync(() => api('uuids', { query: { page, size } }), [page]);
// 轮询
usePolling(() => load(), 5000);
// 提交
await api('admin/vpn/nodes', { method: 'POST', body: {...} });
```

- `api()` 自动加 `Authorization: Bearer`，401 自动跳登录
- 异步任务用 `s1(res)` 判断并打开任务抽屉

---

## 6. Agent

### 6.1 构建与安装

```bash
cd agent
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w" -o dist/proxypilot-agent-linux-amd64 ./cmd/proxypilot-agent
```

- 安装：面板「服务器 → 接入」生成一次性安装命令（含 token），或 `deploy/agent-install.sh`
- 配置：`/etc/proxypilot-agent.conf`
- 服务：`systemctl status proxypilot-agent`
- 版本常量：`internal/agent/agent.go` 的 `const Version`

### 6.2 与面板的协议

```
POST /api/agent/keycheck   首次接入校验令牌
POST /api/agent/poll       每 60s 心跳 + 领任务（返回 tasks[]、配置版本、agent 最新版本）
POST /api/agent/ack        回报任务结果
GET  /api/agent/latest     查询最新 agent 版本与下载地址
WS   /api/agent/ws         长连接（用于终端隧道、即时任务唤醒）
```

心跳负载包含：CPU/内存/磁盘/负载/进程数/连接数、服务状态、组件版本、流量增量。

### 6.3 任务类型

| type | 用途 | 关键字段 |
|---|---|---|
| `exec` | 执行任意 shell 命令 | `command`、`timeout`、`system_action?`、`service?` |
| `deploy` | 下发配置文件 | `path`、`content`、`check_cmd`、`restart_cmd` |

任务在 `internal/runner/runner.go` 分发；`system_action` 触发 systemd 操作。

### 6.4 内嵌 VPN 节点

- 启动时读 `/etc/proxypilot/agent/vpn.json`，存在则 `vpnnode.Start()`
- 文件不存在则跳过（不影响 Agent 其他功能）
- 配置写入后需要**重启 Agent** 才生效（部署脚本已异步处理）
- 端口：UDP 443，TUN `ppvpn0`，需 `CAP_NET_ADMIN`

### 6.5 自升级

1. 面板 `/api/agent/latest` 返回 `{base, version, files{arch:{file,sha256,size}}}`
2. Agent 比对自身版本 → 下载到临时文件 → 校验 SHA256 → 原子替换
3. 分发基址当前为存储桶 `https://s3.proxypilot.yangzifun.org/agent`（见 `docs` 与系统设置）

---

## 7. 客户端（vpnclient）

### 7.1 三种构建形态

| 构建 | 命令 | 结果 |
|---|---|---|
| CLI | `go build ./cmd? `（默认） | 带 `-api/-token/-mesh-*` 参数的命令行客户端 |
| 桌面 | `go build -tags desktop` | Wails v3 窗口 + 托盘 + 本地控制台 |
| 安装器 | `go build -tags "desktop installer"` | 内嵌主程序的安装包 |

> 安装器用 `//go:embed assets/ProxyPilotApp.exe`，所以发布顺序必须是：
> 先构建主程序 → 复制到 `assets/ProxyPilotApp.exe` → 再构建安装器。`build-release.ps1` 已按此顺序执行。

### 7.2 运行时结构

```
main()（桌面入口）
 ├─ setupFileLog()       日志 → %LOCALAPPDATA%\ProxyPilot\logs\client.log（2MB 滚动）
 ├─ cleanStaleTunnel()   清理上次残留：路由、网卡地址、NRPT、防火墙 DNS 规则
 ├─ runUI()              本地 HTTP 控制台（127.0.0.1:随机端口），ui/index.html 内嵌
 └─ Wails 窗口 + 托盘（连接/断开/设置/退出）
```

**连接状态机**（`ui.go`）：

```
idle → connecting（拉节点 → 换票 → HTTP/3 鉴权 → 建流 → 接管网络 → 探测）
     → connected ─┬─ 用户断开 → idle
                  └─ 错误     → error
```

### 7.3 数据面

| 文件 | 职责 |
|---|---|
| `stream_dial.go` | `POST /tunnel` 建流，返回 `packetConn` |
| `stream_tunnel.go` | `[2B len][IP]` 分帧，`SendPacket`/`RecvPacket` |
| `tunnel.go` | `pumpUp`/`pumpDown` 协程、统计、生命周期 |
| `tunnel_probe.go` | 连通性探测（**发标准 DNS 查询**，不要手工拼 IP 头） |
| `route_windows.go` | 网卡地址/网关/路由/DNS + 清理 |
| `dns_guard_windows.go` | NRPT + 物理网卡 53 封禁 |
| `cleanup_windows.go` | 启动自愈 |

**Windows 接管要点**

- `0.0.0.0/1` + `128.0.0.0/1` 双半区覆盖全网（不改默认路由）
- 服务器 IP 必须加 `/32` 主机路由（经真实网关），否则 QUIC 回环
- 断开时必须删除上述路由 + NRPT 规则，否则整机断网

### 7.4 安装器

`ui_install.go` 的 8 个步骤：`check → copy → driver → adapter → registry → firewall → autostart → shortcut`，
每步在界面上实时显示；替换被占用的主程序时会先 `taskkill` 再重试。

---

## 8. 协议实现要点（开发视角）

完整字节级说明见 [readme 协议栈章节](../readme.md#vpn-与组网协议栈)，这里只记开发约束。

| 约束 | 原因 |
|---|---|
| 帧 = `[2B 大端长度][IP 报文]` | 流无消息边界，必须自带长度 |
| 单帧 ≤ 4096 | 防异常长度打爆内存 |
| 会话校验 `src == JWT.ip4` | 防地址伪造 |
| 隔离：仅 `src 与 dst 都是客户端` 才丢 | 早期误伤公网回包 |
| 写 TUN 失败只 `continue` | 早期 `return` 导致会话静默死亡 |
| **不要用 QUIC DATAGRAM 承载** | 不重传，跨境丢包即永久丢失 |
| 探测必须发纯 DNS 报文 | 手工拼 IP 头会被对端当乱码 |

节点侧并发模型：

```
每个会话 1 个 readStream 协程（读上行）+ 全局 tunLoop 分发下行
会话表 sessions[ip4] = *session，重复 IP 返回 409
```

---

## 9. 构建与发布

### 9.1 一键发布

```powershell
pwsh ./scripts/build-release.ps1            # 全量
pwsh ./scripts/build-release.ps1 -SkipTests # 跳过测试
```

流程：

1. 读三个版本常量（面板 `main.go`、Agent `agent.go`、客户端 `main.go`）
2. 构建前端 → 同步进 `server/internal/api/assets_web/`
3. 构建面板（linux amd64/arm64）
4. 构建 Agent（linux amd64/arm64）+ 生成 `manifest.json` / `SHA256SUMS`
5. 构建客户端：主程序 → `assets/ProxyPilotApp.exe` → 安装器
6. 输出到 `dist-release/`：二进制、脚本、`SHA256SUMS.txt`、`MANIFEST.txt`

### 9.2 版本注入

| 组件 | 常量位置 | 生效方式 |
|---|---|---|
| 面板 | `server/cmd/proxypilot/main.go` `var version` | 运行时赋给 `api.Version` |
| 前端 | 由 `build.mjs` 从上面读，写 `meta.json` | 页面加载动画与 `boot.version` |
| Agent | `agent/internal/agent/agent.go` `const Version` | 心跳上报 |
| 客户端 | `vpnclient/main.go` `const Version` | 窗口标题、安装包版本号 |

改版本号要同时更新 Windows 资源：`go-winres simply --file-version 2.7.0.0 ...`。

### 9.3 分发渠道

| 渠道 | 内容 | 命令 |
|---|---|---|
| 面板本地 | `/opt/proxypilot/agent-dist/` | 直接复制 |
| Cloudflare R2 | `agent/` 前缀 | `bash /root/publish-r2.sh` |
| GitHub Release | 面板二进制 + 脚本 | 打 tag `V*` 触发 CI |

推 Agent 到所有节点：

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"all":true}' "http://127.0.0.1/api/agent/push-update?refresh=1"
```

---

## 10. 测试

```bash
# 后端
cd server && go test ./... && go vet ./...

# 隧道节点（含隔离回归）
cd masque && go test ./node/ -v

# Agent
cd agent && go test ./...

# 客户端
cd vpnclient && go test ./...
```

> Windows 上客户端测试会因 `requireAdministrator` 清单报
> "requested operation requires elevation"。临时把 `rsrc_windows_*.syso` 重命名再跑，
> 跑完记得改回来。

黄金用例：`server/testdata/` 下的 Clash / sing-box / MTProto 快照，改动生成逻辑时对照。

冒烟脚本：`server/tools/smoke.sh https://<域名>`（登录、订阅、Agent、WebSSH 各跑一遍）。

---

## 11. 排障手册

| 现象 | 优先检查 | 命令 |
|---|---|---|
| 面板 502 / 白屏 | 服务与 DB | `systemctl status proxypilot`、`curl /healthz` |
| 节点离线 | Agent 心跳 | `systemctl status proxypilot-agent`、`journalctl -u proxypilot-agent -n 50` |
| 终端连不上 | `allow_exec`、Origin | `GET /api/shell/precheck/{id}` |
| VPN 连不上 | 节点 UDP 443、证书 | `ss -lunp \| grep :443`、`openssl x509 -in cert.pem -noout -subject` |
| VPN 能连但没流量 | 分帧/路由 | 客户端日志「隧道流量」、节点「流隧道流量」 |
| 断开后整机断网 | 残留 `/1` 路由、NRPT | `route print -4`、`Get-DnsClientNrptRule`；客户端「修复网络」 |
| DNS 泄露 | NRPT 是否生效 | 连接详情「DNS 泄露防护」 |
| 安装报 Access denied | 旧进程占用 | 安装器已自动 taskkill，仍失败手动结束进程 |

常用远程排查（面板 → 节点 exec）：

```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"server_id":16,"type":"exec","command":"systemctl status proxypilot-agent --no-pager","title":"检查"}' \
  http://127.0.0.1/api/agent/tasks
# 结果查 agent_tasks.output
```

---

## 12. 编码规范

**通用**

- 错误信息用中文且可执行（说清"哪个对象、什么原因、怎么修"）
- 不写死版本号、IP、域名；从配置或常量读
- 提交前 `go vet ./...` 必须干净

**Go**

- 包内文件按业务域命名，不要造 `utils.go` 垃圾桶
- handler 只做参数解析与响应，业务下沉到函数
- 所有 SQL 用占位符，禁止字符串拼接
- 后台协程必须接受 `ctx` 并能退出

**前端**

- 页面统一 `PageCard` + `StatCard` 布局，禁止自定义大色块
- 图标必须 import；未使用变量会导致构建体积和 lints 问题
- 数字格式化用 `fmtNum` / `fmtTime` / `fmtAgo`

**提交**

```
feat(scope): 摘要      新功能
fix(scope): 摘要       修复
docs(scope): 摘要      文档
refactor(scope): 摘要  重构
```

正文写：根因 → 改动 → 验证数据。一次提交只做一件事。

---

## 13. 常见开发任务

| 任务 | 步骤 |
|---|---|
| 加一个 API | `router.go` 注册 → 新建 `xxx.go` handler → 前端 `api('xxx')` |
| 加一个页面 | `pages/Xxx.jsx` → `nav.jsx` PAGE_META/NAV_GROUPS → `App.jsx` PAGES → 重新构建 |
| 加一个协议模板 | `internal/api/singbox*.go` 模板结构 + `rule/` 默认模板 + 前端协议页 |
| 加一个数据库字段 | 新建 migration → 更新 struct 与查询 → 前端展示 |
| 加一个 Agent 任务 | `internal/runner/runner.go` 加 case → 面板侧 `agentQueueTask` 下发 |
| 加一个客户端设置项 | `uiSettings` 加字段 → `handleState` 暴露 → `ui/index.html` 加控件 |
| 升级隧道协议 | `masque/node/` 与 `vpnclient/stream_*.go` 同步改 → 两端版本号一起升 |

---

## 14. 安全注意事项

| 项 | 规则 |
|---|---|
| 密钥 | 只存在 DB `app_settings`（加密）或环境变量，**禁止**写入代码/仓库 |
| JWT | 面板签发，节点用公钥验签；`node` 声明必须与节点 ID 比对 |
| Agent 令牌 | 一次性接入令牌，绑定 server_id，不能访问管理接口 |
| SSH 防护 | 节点只允许 `100.96.0.0/12`、`10.99.0.0/16`、`2606:4700:cf1:1000::/64`；**本机不做限制** |
| DNS 守卫 | NRPT + 物理网卡 53 封禁，断开必须撤销 |
| 终端 | 本机 WebSocket 终端以 root 运行，仅超管可用 |
| 日志 | 不打印令牌全文（只打前后各 4 位） |

---

## 附：文档索引

| 文档 | 内容 |
|---|---|
| [readme.md](../readme.md) | 产品能力、部署、协议栈（字节级） |
| [DNS-LEAK.md](DNS-LEAK.md) | DNS 泄露分析、NRPT 方案与验证 |
| [deploy/README.md](../deploy/README.md) | 安装/升级/卸载脚本细节 |
