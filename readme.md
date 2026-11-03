# ProxyPilot

![Go](https://img.shields.io/badge/Go-1.23-00ADD8?logo=go&logoColor=white) ![MySQL](https://img.shields.io/badge/MySQL-8.0-4479A1?logo=mysql&logoColor=white) ![React](https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=black) ![Ant Design](https://img.shields.io/badge/Ant%20Design-6-1677FF?logo=antdesign&logoColor=white) ![Agent](https://img.shields.io/badge/Agent-Go-00ADD8?logo=go&logoColor=white) ![Version](https://img.shields.io/badge/Version-V15.34-blue.svg) ![License](https://img.shields.io/badge/License-MIT-green.svg)

---

## 目录

- [架构总览](#架构总览)
- [技术栈](#技术栈)
- [快速开始](#快速开始go--mysql)
- [目录结构](#目录结构)
- [旧版代码](#旧版代码)
- [功能总览](#功能总览)
- [协议与端口](#协议与端口)
- [权限模型](#权限模型)
- [生产部署](#生产部署)
- [使用指南](#使用指南)
- [Go Agent](#go-agent)
- [API 概览](#api-概览)
- [开发与测试](#开发与测试)
- [故障排查](#故障排查)
- [更新日志](#更新日志)
- [安全说明](#安全说明)
- [许可证](#许可证)

---

## 架构总览

```text
                    ┌─────────────────────────────┐
浏览器 ──HTTPS/WS──▶ │  Go server (server/)         │
  /  /admin         │  前端 + REST + 订阅 + WebSSH │
  /api/*            └───────┬─────────────┬────────┘
  /sub /clash               │             │
                    MySQL 8 │             │ 本地目录（桶 / Agent 分发）
                            ▼             ▼
                     ┌────────────┐  ┌──────────────────┐
                     │ 业务数据   │  │ data/bucket      │
                     │ 26+ 张表   │  │ agent/dist       │
                     └────────────┘  └──────────────────┘
                            ▲
                            │ Agent 出站：poll / ack / WS / shell
                     proxypilot-agent ──▶ sing-box / mtg
```

- 后端：`server/`（Go 1.23，标准库 HTTP，150 条路径）
- 前端：`web-antd/`（React 18 + Ant Design 6 + antd-mobile）
- 数据库：MySQL 8（`utf8mb4_0900_as_cs`，`server/migrations/`）
- Agent：`agent/`（Go，只出站；配置任务下发 + PTY 终端）
- 存储：本地对象桶 + Agent 分发目录（不再依赖 R2/S3）
- WebSSH：Go WebSocket 中继，不依赖 Durable Object
- 旧 Cloudflare Worker：已移除

## 技术栈

| 层 | 选型 |
|---|---|
| 前端 | React 18 + Ant Design 6 + antd-mobile，Vite 构建 |
| 后端 | Go 1.23（`net/http`，无 Web 框架；`database/sql` + go-sql-driver/mysql） |
| 数据库 | MySQL 8，26+ 张表，启动自动迁移 |
| 鉴权 | HS256 JWT（与 Worker 同算法/载荷）、PBKDF2 口令、TOTP、X-API-Key、OIDC |
| 节点 Agent | Go（轮询 / WebSocket 唤醒 / PTY 终端 / 配置落盘） |
| 存储 | 本地目录：对象桶 + Agent 二进制分发 |

## 快速开始（Go + MySQL）

```bash
# 1) 配置
cp server/.env.example server/.env
# 必改：PP_JWT_SECRET、PP_ADMIN_PASSWORD、MYSQL_ROOT_PASSWORD、PP_PUBLIC_BASE_URL

# 2) Agent 产物（可选，用于节点接入）
cd agent && ./build.sh && cd ..

# 3) 启动（MySQL + Go，自动迁移）
cd server && docker compose up -d --build

# 4) 打开后台
# http://127.0.0.1:8080/admin  （admin / 你在 .env 里设置的口令）
# 运行中配置：/admin/settings（登录方式 / Turnstile / SMTP / OIDC / 站点地址）
```

反向代理 + TLS + 切换域名：见 [server/README.md](server/README.md) 的「生产部署（替换域名）」。
部署后冒烟：`server/tools/smoke.sh https://<域名>`。

## 目录结构

```text
proxypilot/
├── server/                     # 主后端（Go + MySQL）
│   ├── cmd/proxypilot/         # 入口
│   ├── internal/api/           # 路由 / 业务 / WebSocket / 设置
│   ├── internal/auth/          # JWT / PBKDF2 / TOTP
│   ├── internal/db/            # 连接池 + 迁移执行器
│   ├── migrations/             # MySQL DDL（26+ 表）
│   ├── testdata/               # Clash / sing-box / MTProto 黄金用例
│   ├── internal/api/assets_web/# 内嵌前端快照（免构建兜底）
│   └── Dockerfile / docker-compose.yml / .env.example / Makefile
├── web-antd/                   # 前端源码：React 18 + Ant Design 6
│   ├── src/                    # 管理后台 + 用户中心 + 图表/样式
│   └── dist/                   # 构建产物（PP_WEB_DIST 指向这里）
├── agent/                      # Go Agent + install.sh + build.sh + dist/
├── rule/                       # Clash / Mihomo 模板与规则
├── docs/                       # 架构与迁移文档
```

### 前端说明（Ant Design 为主）

- 源码唯一入口：`web-antd/src`。生产推荐 `npm ci && npm run build`，设置 `PP_WEB_DIST=.../web-antd/dist`。
- 未设置 `PP_WEB_DIST` 时，Go 使用内嵌 UI；`node web-antd/scripts/build.mjs` 会自动同步到 `server/internal/api/assets_web/`。

## 旧版代码

- `workers/`、`wrangler.toml` 与依赖 Worker 的测试脚本已从本分支删除。
- 历史迁移方案、D1 导入工具仍保留在 `docs/migration/` 与 `server/tools/`，仅用于迁移旧数据。
- `PP_LEGACY_BASE` 配置项保留，作为迁移期可选兜底；新部署无需配置。
- 当前版本：Go 1.23 + MySQL 8 + React 18 / Ant Design 6 + Go Agent。

## 功能总览

### 公共站点

| 入口 | 说明 |
| ---- | ---- |
| `/`、`/me` | **用户中心**：邮箱验证码 + Cloudflare Turnstile 登录，UUID 由登录邮箱决定（不再手动输入）。PC 端为单页（订阅卡 + 订阅生成 / 配置管理 / 访问统计 / 外部订阅源）；移动端为底部 4 个页签（我的订阅 / 配置管理 / 访问统计 / 我的），账号信息与退出登录收在「我的」 |
| `/manage` | 老书签兼容：302 → `/` |
| `/login` | 三种登录方式：账号密码 / 邮箱验证码 / OIDC 单点登录；支持 TOTP 双重验证与备份码 |
| `/sub`、`/clash-config` 等 | 面向客户端的订阅输出（UUID 鉴权 + 可选人机验证） |

### 管理后台（左侧菜单）

| 页面 | 内容 |
| ---- | ---- |
| **系统概览** `#dash` | 访问统计、趋势图、热门 UUID、最新订阅 |
| **订阅分组** `#uuids` | 按 UUID 管理节点配置、订阅源、外部订阅合并、分组授权；**一个邮箱只能绑定一个 UUID**（重复绑定返回 409） |
| **订阅模板** `#tpls` | Clash / Mihomo 订阅模板（含默认模板）；页面底部可直接浏览/编辑对象存储 `rule/` 下的模板与地区配置（R2 绑定 `bucket`） |
| **服务器管理** `#servers` | Agent 接入、安装命令（**可选安装后停用 SSH**）、**SSH 状态列与单台 / 批量启停**、密钥轮换、一键更新、任务队列、**实时终端（WebSSH）** |
| **普通代理** `#normal` | 协议模板（sb.json；MASQUE 由 sb.json 的 masque-server endpoint 承载；**可选 sing-box 正式版或预览版**）+ 节点绑定（服务器 / 域名 / 授权 UUID）+ 编排下发；**V15.6 起移除 Xray**（vless-xhttp 下线，改由 sing-box 的 vless-reality 承接） |
| **流量统计** `#traffic` | **按用户的上行 / 下行 / 活跃状态 / 趋势**，以及**节点实时连接明细**（源 IP、目标域名、协议、上下行、持续时间）；数据来自 sing-box 的 `clash_api` / `v2ray_api` |
| **MTProto** `#mtproto` | mtg-multi 模板 + 节点两级、按用户授权，链接不进订阅 |
| **WARP** `#masque` | Cloudflare WARP 设备注册、按 UUID 授权、MASQUE 配置下载 |
| **日志管理** `#logs` | 节点下发与命令执行日志、结果与耗时 |
| **服务器监控** `#status` | 顶部三张等高卡片：**集群在线率**（在线率环图 + 服务异常 / 未安装）、**资源均值**（CPU / 内存仪表 + 磁盘进度）、**全球分布**（Natural Earth 底图，按经纬度打点、点击进详情；可开大图，滚轮缩放 / 拖动平移；小图悬停圆点查看地区与资源信息，大图直接在地图内标注地区名）+ 卡片式面板（CPU / 内存 / 磁盘 / 负载 / 网络 / 磁盘 IO / 连接 / 进程数 / **IPv4 + IPv6 分行显示**）；「详情」内分区展示概览、**运行信息（内核 / 平台 / Tunnel / Top 进程）**、服务状态（可一键重启）、下发配置、任务队列与性能折线图 |
| **系统管理** `#sys` | 用户管理、访问日志维护、API 密钥 —— **仅 `admin` 可见** |
| **安全中心** `#sec` | 本人改密、MFA、邮箱绑定（所有账号可用） |

### 主要特性

- **订阅生成**：本地配置 + 外部订阅源合并，支持 Clash / Mihomo 与原始链接
- **用户中心（PC / 移动端）**：PC 单页、移动端 4 页签；订阅生成（原始 / Clash / 批量 + 模板 + 本机二维码）、配置管理（搜索 / 8 种协议 / 外部订阅源增删启停）、访问统计、账号信息与换绑邮箱
- **监控双栈地址**：Agent 上报的本机 IPv4 / IPv6 分别落库（`last_ip4` / `last_ip6`），页面按有无各显示一行，不再只剩 IPv6 或同一地址显示两遍
- **协议编排**：一份模板一个配置文件（sb.json），只由 sing-box 承载；Xray 已下线（V15.6），MASQUE 用 sing-box 的 `masque-server` endpoint
- **MTProto（Telegram）**：`mtg-multi` 单进程多 secret，按 UUID 授权、撤销只断该用户；配置独立成表，不写入 `configs`，也就不进任何订阅；门户可看二维码或发到邮箱
- **版本化下发**：内容未变不升版本；变更自动 +1；Agent 回执 applied_version
- **UUID 一致性巡检**：Cron 每 30 分钟比对「平台授权 ↔ 节点配置 ↔ 订阅配置」，发现漂移自动修复；也支持事件触发与手动检查
- **删除 UUID 级联**：从授权、节点用户、订阅配置、节点配置中一并清理
- **监控采集**：hostname / OS / 内核 / 负载 / 内存 / 磁盘 / CPU / 网络 / 磁盘 IO / 连接状态 / 进程 Top / 服务状态 / cloudflared Tunnel / IPv4 + IPv6
- **Agent 自动更新**：与对象存储 `manifest.json` 比对，后台一键推送升级
- **安全**：JWT、可选 Turnstile、TOTP、API 密钥（细粒度权限位）、占位 UUID 拦截、公开示例密钥自动轮换

---

## 协议与端口

端口基线默认 `13700`，每个协议固定偏移（可在协议模板里逐项改端口）：

| 协议 | 引擎 | 端口 | 传输 | 说明 |
| ---- | ---- | ---- | ---- | ---- |
| hysteria2 | sing-box | 13701 | QUIC / UDP | 自签 TLS，弱网表现好 |
| shadowsocks 2022 | sing-box | 13702 | TCP | `2022-blake3-aes-256-gcm`，用户密钥由服务器密钥 + UUID 派生 |
| anytls | sing-box | 13703 | TLS | 抗探测，证书与其他 TLS 协议共用 |
| TUIC v5 | sing-box | 13704 | QUIC / UDP | bbr，0-RTT 关闭，uuid 双填 |
| vless-reality | sing-box | 13705 | TCP + REALITY | `xtls-rprx-vision`，无需自有证书 |
| vless-ws | sing-box | 13706 | TLS + WebSocket | 路径 `/vlws`，可直接接 CDN |
| MASQUE | sing-box（masque-server） | 13707 | HTTP/3 · CONNECT-IP | 随 sb.json 下发（endpoint），需 sing-box ≥ 1.15.0；默认模板不启用，不生成订阅链接 |
| MTProto（Telegram） | mtg-multi | 13709 | TCP | 仅 TG 使用，**不进订阅**；每用户独立 secret，SIGHUP 热加载 |

补充：

- 所有 sing-box 入站监听 `::`（IPv4 + IPv6 双栈）；
- sb.json 的域名解析策略为 `prefer_ipv4`：优先 IPv4，IPv6-only 目标自动回落；
- 内部字段 `enable_vmess` 承载 TUIC 槽位（历史兼容），对外显示为 `tuic`；
- MASQUE 自 sing-box 1.15.0 起由 `masque-server` endpoint 提供（HTTP Basic 用户 + TLS），不再单独起进程；1.15 稳定前默认模板不启用，模板启用时后端会按节点上报的 sing-box 版本拦截；
- MTProto 不需要证书：FakeTLS 的伪装域名只写进 secret（`ee` + 16 字节随机 + hex(域名)），证书由被伪装的真实站点提供。

---

## 权限模型

| 身份 | 入口 | 可用范围 |
| ---- | ---- | -------- |
| 超级管理员（默认 `admin`，可用 `SUPER_ADMIN_USERNAME` 覆盖） | `/admin` | 全部功能（含「系统管理」） |
| 其他后台管理员 | `/admin` | 除「系统管理」外的全部页面与接口 |
| 用户 | `/`（= `/me`） | 邮箱验证码 + 人机验证登录后进入用户中心（PC 单页 / 移动端 4 页签），只能管理 **自己邮箱绑定的那个 UUID**（一个邮箱仅允许绑定一个 UUID）；`/manage/*` 数据接口校验令牌与 UUID 归属 |

- 非超管登录时「系统管理」入口不渲染，手输 `#sys` 也会被前端拦回；
- 服务端同样强制：`/api/admin/users`、`/api/logs/summary`、`/api/logs/cleanup`、`/api/apikeys` 对非超管一律 403；
- 用户端令牌（JWT 带 `role=user`）只能访问 `/api/portal/*`，调用任何后台接口一律 403；
- 用户能看到哪些 UUID 由「订阅分组」里维护的**邮箱**决定（大小写不敏感），管理员改邮箱即改归属。

---

## 生产部署

目标：把域名直接指向 Go 服务，功能与 Worker 对齐。完整步骤见
[`server/README.md`](server/README.md) 的「生产部署（替换域名）」。

1. **数据**：D1 导出后用 `server/tools/d1_to_mysql.py` 导入 MySQL；或空库启动自动建表。
2. **配置**：`server/.env`；`PP_JWT_SECRET`、`PP_AGENT_KEY_SECRET` 必须与旧 Worker 相同。
3. **启动**：`cd server && docker compose up -d --build`（MySQL + Go，自动迁移）。
4. **反代**：Caddy/Nginx 终止 TLS，转发 `Host` / `X-Forwarded-Proto`，并允许 WebSocket Upgrade。
5. **验收**：`server/tools/smoke.sh https://<域名>`；后台登录、订阅、Agent、WebSSH 各跑一遍。
6. **切流/回滚**：DNS 指向新服务；旧系统可短期保留，必要时回滚 DNS。

> 已知限制：批量任务同步执行；WebSSH 会话在进程内存（多实例需按 server_id 粘性）。
> MySQL 与真实 Agent 的端到端回归需在部署机完成。

---

## 使用指南

### 1. 管理员首次配置顺序

按这个顺序建，依赖关系不会打架：

| 顺序 | 页面 | 做什么 |
| ---- | ---- | ------ |
| 1 | 安全中心 `#sec` | 改掉默认密码，建议开 TOTP；绑定收件邮箱（邮箱验证码登录要用） |
| 2 | 订阅模板 `#tpls` | 有默认模板就能直接生成 Clash 订阅；想自定义分组 / 分流就在这里改 YAML |
| 3 | 服务器管理 `#servers` | 新建服务器 → 生成接入密钥 → 复制安装命令到机器上执行 |
| 4 | 节点管理 `#singbox` | 新建协议模板（「填入示例」会生成随机密钥与端口）→ 新建节点，绑定服务器 / 域名 / 授权 UUID |
| 5 | 节点管理 | 点「下发」，等 Agent 下一轮 poll（≤30 秒）写入配置并重启 |
| 6 | 订阅分组 `#uuids` | 新建 UUID，把「邮箱」填成用户邮箱；一个邮箱只能绑一个 UUID |
| 7 | 服务器监控 `#status` | 确认节点在线、CPU / 内存 / 磁盘有数、IPv4 / IPv6 显示正常 |

> 「全局域名」用于生成订阅里的节点域名；协议模板里的 `{DOMAIN}` 会自动替换。

### 2. 日常运维

- **服务器监控**：顶部三卡（在线率 / 资源均值 / 全球分布）等高排布；下面「各节点资源占用」「服务器明细」卡片点进详情能看内核、服务状态、Top 进程、任务队列与性能折线。
- **一键更新**：「服务器管理」→ 一键更新，Agent 与对象存储 `manifest.json` 比对后自升级。
- **日志管理**：每次下发 / exec 都有 rc、耗时与输出，排障先看这里。
- **巡检与通知**：Cron 每 30 分钟比对「平台授权 ↔ 节点配置 ↔ 订阅配置」，漂移自动修复；「通知设置」可配告警邮箱。
- **API 密钥**：「系统管理 → API 密钥」按权限位创建，给脚本 / CI 用。

### 3. 用户中心（PC）

地址 `https://<你的域名>/`（`/me` 等价），邮箱验证码 + 人机验证登录后是单页：

| 区块 | 用法 |
| ---- | ---- |
| 订阅卡 | 显示 UUID、状态、节点数；多 UUID 时点卡片切换；点左上头像看账号信息 |
| 订阅生成 | 选「原始 / Clash / 批量」→ Clash 可挑模板 → 生成后复制链接、下载文件、看二维码，或点 App 名称一键唤起 |
| 配置管理 | 只读列表：搜索备注 / 配置内容、按 8 种协议筛选、每行复制节点链接 |
| 外部订阅源 | 填 `https://` 地址 + 备注添加，启用后下次生成自动合并；可停用 / 删除 |
| 访问统计 | 总访问 / 今日访问 / 订阅拉取 / 接口生成，近 30 天趋势 + 访问构成；「最近访问」点按钮看弹窗明细（时间 / 类型 / IP / 客户端） |
| 账号与信息 | 改所有者 / 联系方式 / 备注；换绑邮箱（验证码发到新邮箱，成功后令牌自动换发） |

### 4. 用户中心（手机）

底部 4 个页签：

| 页签 | 内容 |
| ---- | ---- |
| 我的订阅 | 顶部订阅卡（状态 / 节点数 / UUID 复制，多个 UUID 横滑切换）→ 选格式 → 生成 → 复制 / 下载 / 扫码 / 一键导入 |
| 配置管理 | 搜索 + 协议胶囊筛选 + 下拉刷新 + 加载更多；点「外部订阅源」弹层里可新增 / 启停 / 删除 |
| 访问统计 | 四个指标块 + 趋势图 + 访问构成 + 最近访问；支持下拉刷新与失败重试 |
| 我的 | 账号卡、登录邮箱（换绑）、UUID 复制、用户信息编辑、「退出登录」 |

### 5. 客户端导入

| 格式 | 适合 | 导入方式 |
| ---- | ---- | -------- |
| 原始（Base64） | v2rayN / Shadowrocket / NekoBox 等通用客户端 | 复制订阅链接粘到「添加订阅」，或扫码 |
| Clash 订阅 | Clash Verge / ClashX / Mihomo / Stash | 同上；可套用订阅模板改变分组与分流 |
| 批量配置 | 一次性导出 / 脚本处理 | 下载 `.txt`，一行一个节点链接 |

生成结果里的 App 按钮走 URL scheme（`clash://install-config`、`shadowrocket://add/sub`、`sing-box://import-remote-profile`、`v2rayng://install-sub`），手机上点一下直接跳到客户端。节点变动后客户端「更新订阅」即可，链接不变。

### 6. 邮箱绑定规则

- **一个邮箱只能绑定一个 UUID**：重复绑定返回 `409 email_taken`；
- 用户看到哪些订阅完全由「订阅分组 → 邮箱」决定（大小写不敏感），管理员改邮箱即改归属；
- 换绑邮箱后立即生效，旧令牌失效并换发新令牌，当前页面无需重新登录。

### 7. API 调用示例

```bash
# 后台登录拿 JWT
TOKEN=$(curl -s https://<域名>/api/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<密码>"}' | jq -r .token)

# 查服务器列表
curl -s https://<域名>/api/servers -H "Authorization: Bearer $TOKEN"

# 用 API 密钥调接口（只开放 配置分组 / 配置管理 / 配置模板 三类，按 permissions 校验）
curl -s https://<域名>/api/uuids -H 'X-API-Key: pk_xxx'
```

> `X-API-Key` 只能访问 `/api/uuids`、`/api/uuid-configs`、`/api/templates/manage`；其余接口（服务器、Agent、账号、日志等）必须用 JWT。

用户端接口走用户令牌（`localStorage.pp_portal_token` 那个），只能访问 `/api/portal/*` 与自己的 `/manage/*`。

### 8. 备份与恢复

后台「系统设置 → 数据备份」可一键导出 ZIP（`schema.sql` + 全表 JSON + R2/本地桶文件）。

命令行等价方式：

```bash
mysqldump --single-transaction --routines --triggers proxypilot > backup-$(date +%F).sql
# 本地桶
tar czf bucket-$(date +%F).tar.gz -C /opt/proxypilot data/bucket
# R2 桶（rclone 配置后）
rclone sync r2:proxypilot r2-backup:proxypilot
```

恢复：先建库并执行 `schema.sql`，再按表导入 JSON；对象桶文件同步回 `PP_BUCKET_DIR` 或 R2。

### 9. 远程终端（WebSSH）

「服务器管理 → 命令」现在是**实时交互式终端**，打开即建立会话，不是逐条推送命令：

| 前置条件 | 说明 |
| -------- | ---- |
| Agent ≥ **1.3.0** 或本地服务器 | 本地服务器由 Go 直接开 PTY，无需 Agent |
| 服务器开启「允许远程命令」 | 服务器编辑弹窗里的 `allow_exec` |

打开后的状态流转：

```
连接中 → 等待 Agent 接入（Agent 下一次 poll，≤30 秒）→ 已连接
```

- 已连接后就是真正的 shell：支持 `vim` / `top` / `htop` 等交互程序、颜色、窗口大小变化、Tab 补全；
- 浏览器用 `@xterm/xterm` 渲染，窗口自适应（FitAddon），没有 canvas 的环境退化成纯文本输出；
- 「重新连接」会重新登记会话并重新拨号；关闭弹窗即结束远端 shell 进程；
- 原理：浏览器 `wss://…/api/shell/browser/:id` ↔ **Durable Object** ↔ Agent 主动出站的 `wss://…/api/shell/agent/:id` ↔ 服务器上的 PTY（`/bin/bash -il`，没有 bash 时回落 `/bin/sh -i`）；
- 安全：浏览器侧校验后台 JWT（用户端令牌不可用）、服务器必须开 `allow_exec`；Agent 侧校验接入令牌；会话号一次性、120 秒未接入自动作废；同一服务器同时只保留一个终端会话。

常见状态：

| 现象 | 原因 / 处理 |
| ---- | ----------- |
| 提示「服务端没有配置 Durable Object 绑定 SHELL」 | 按部署第 3 步加 `SHELL` 绑定并重新部署 |
| 一直「等待 Agent 接入」 | Agent 版本低于 1.3.0，或 Agent 离线；升级 Agent 后重试 |
| 打开即断开 | 检查服务器是否开启「允许远程命令」、Agent 是否在线 |

---

### 10. Telegram（MTProto）代理

1. 「节点管理 → MTProto → 协议模板」先确认模板：监听端口默认 `13709`、伪装域名（如 `storage.googleapis.com`，不能填 IP）、授权模式；改完保存会自动重渲染全部绑定节点；
2. 「MTProto → 节点绑定」新建节点：选服务器 + 模板，域名 / 端口留空即继承模板（需要时再覆盖）；
3. 保存后自动渲染 `/etc/mtg/config.toml` 并排队下发；Agent 首次会自动安装 `mtg-multi` 并写 `mtg.service`（`ExecReload` = SIGHUP）；
4. 打开「授权用户」抽屉 → 选 UUID → 授权：每人生成独立 secret，撤销 / 轮换只影响该用户；
5. 用户中心「Telegram 代理」可看二维码、一键发到绑定邮箱；
6. 链接**不会**出现在 `/sub`、`/batch-configs`、`/clash-config` 里——授权数据在 `mtproto_clients`，从不写 `configs`。

> 「整节点共享」模式（`secret_mode=single`）下所有用户共用一条链接，轮换 / 撤销会影响全部用户，仅适合临时场景。

## Go Agent

- 版本：**1.6.0**，静态编译（`CGO_ENABLED=0`），支持 `linux/amd64`、`linux/arm64`
- 常驻 systemd：`proxypilot-agent.service`（`Restart=always`）
- 安装脚本会校验密钥（`/api/agent/keycheck`）、下载二进制并强制 sha256 校验

采集项（每轮 poll 上报）：

```
hostname / os / kernel / platform / arch / cores / uptime / load
mem_used_mb / mem_total_mb / disk_percent / cpu_ticks
net（累计收发、包、错包、网卡数）
disk_io（累计读写、次数）
conn（TCP/UDP 状态计数）
proc（进程总数 + Top RSS）
services（systemd 服务状态，含 mtg / cloudflared）
versions（sing-box / mtg-multi 的实际版本号，5 分钟缓存，供「服务状态」展示）
ssh（sshd / ssh 的 enabled / active 状态，供「服务器管理」与监控详情只读展示）
ip（IPv4）/ ipv6（本机全局 IPv6）
```

- 不再采集 CPU 温度（云主机普遍读不到）；
- 服务状态四态：`active` / `inactive` / `failed` / `not-installed`；服务器详情里同时显示已装组件的版本号（老 Agent 不上报则为空）；
- 一键更新：后台比对对象存储 manifest.json，推送新二进制并重启 Agent。
- 轮询：默认 **10 秒**一轮；连续 **3 个周期**（默认 30 秒）没有心跳即判离线——页面读取状态时动态判定，cron 负责落库 / 邮件通知。
- 实时终端（1.3.0 起）：poll 拿到 `shell` 指令后主动出站连 `wss://…/api/shell/agent/:id`，在 PTY 里跑 `/bin/bash -il`（没有 bash 时用 `/bin/sh -i`），浏览器输入输出经 Durable Object 双向转发。

常用命令：

```bash
proxypilot-agent doctor -c /etc/proxypilot-agent/config.json   # 自检
systemctl status proxypilot-agent
journalctl -u proxypilot-agent -n 50
curl -fsSL https://<域名>/api/agent/install.sh | sh -s -- --uninstall
```

---

## API 概览

鉴权两种：

- 后台：`Authorization: Bearer <JWT>`（登录后获取）
- 脚本：`X-API-Key: pk_...`（权限位受限于分组 / 配置 / 模板）

| 分组 | 代表路径 |
| ---- | -------- |
| 用户端（邮箱登录） | `/api/portal/send-code`、`/api/portal/login`、`/api/portal/uuids`、`/api/portal/email/send-code`、`/api/portal/email/verify` |
| 登录 / 会话 | `/api/login`、`/api/login/email-code*`、`/api/oidc/*`、`/api/mfa/*` |
| UUID 与订阅 | `/api/uuids`、`/api/uuids/:uuid/nodes`、`/api/uuid-configs`、`/api/sub-sources` |
| 附件管理（R2） | `GET /api/attachments`、`POST /api/attachments/upload`、`GET /api/attachments/download`、`DELETE /api/attachments` |
| 配置与模板 | `/api/batch-configs`、`/api/templates/manage`、`/api/configs` |
| 服务器 | `/api/servers`、`/api/servers/:id/agent-key`、`/api/servers/monitor`、`/api/servers/:id/metrics` |
| 节点下发 | `/api/node-templates`、`/api/node-configs`、`/api/node-configs/logs` |
| 协议编排 | `/api/singbox/templates`、`/api/singbox/nodes`、`/api/singbox/nodes/:id/preview`、`/api/singbox/push` |
| 后台任务 | `/api/jobs`、`/api/jobs/:id`、`/api/jobs/:id/cancel`、`/api/jobs/:id/retry`（批量操作默认转后台，`?sync=1` 可走同步） |
| MTProto | `/api/mtproto/templates`、`/api/mtproto/templates/:id/apply`、`/api/mtproto/nodes`、`/api/mtproto/nodes/:id/clients`、`/api/mtproto/clients/:id/{link,rotate,revoke}`、`/api/portal/mtproto` |
| 巡检 / 监控 | `/api/monitor/uuid-check`、`/api/monitor/notify` |
| Agent（机器侧） | `/api/agent/poll`、`/api/agent/ack`、`/api/agent/install.sh`、`/api/agent/latest`、`/api/agent/push-update` |

完整路由与参数以 `server/internal/api/router.go` 为准。

---

## 开发与测试

要求 **Go 1.23+**、**Node.js ≥ 22**、**MySQL 8.0.13+**。

### 后端（Go）

```bash
cd server
go test ./...
go vet ./...
go build ./cmd/proxypilot
```

### Agent（Go）

```bash
cd agent
go vet ./...
go test ./...
```

### 前端（web-antd）

```bash
cd web-antd
npm install
npm run build      # 构建并自动同步 server/internal/api/assets_web/
npm run preview    # 本地预览
npm run check      # build + render-check + e2e
```

## 故障排查

| 现象 | 排查方向 |
| ---- | -------- |
| 下发显示成功但配置没生效 | `restart_cmd` 必须是 `systemctl restart <svc>`（`enable --now` 不会重启已运行的服务）；看「日志管理」的 rc 与校验输出 |
| sing-box 起不来：`bad key` | SS2022 密钥必须是 32 字节 base64 |
| `decode private key` | REALITY 私钥 43 字符 base64url，不能带 `=` |
| 客户端连上但一直超时 | REALITY 的 `pbk` 与服务器私钥不配对；或端口未放行（hysteria2 / TUIC 走 UDP） |
| 订阅 404 | 该 UUID 既没有本地配置也没有外部订阅源 |
| 监控数据全为 0 | Agent 版本过低（< 1.1.0）；升级后重启 Agent |
| 「命令」提示实时终端不可用（未配置 SHELL） | Worker 没有绑定 Durable Object；按 [部署 第 3 步](#部署) 加 `SHELL` / `ShellSession` 后重新部署 |
| 终端一直停在「等待 Agent 接入」 | Agent < 1.3.0 或离线；升级 Agent（一键更新）并确认心跳正常 |
| 后台看不到「系统管理」 | 当前登录账号不是超管（账号名需等于 `SUPER_ADMIN_USERNAME`） |
| 一键更新失败 | 对象存储里的 `manifest.json` 与二进制是否同步、基址是否正确 |
| `install.sh` 返回 **403** / `cf-mitigated: challenge`（阿里云等机房 IP 常见） | **Cloudflare 对机房 IP 发起了 Managed Challenge**，请求没到 Worker（`/cdn-cgi/trace` 能通、`/api/*` 被挑战就是典型特征）：<br>1) Security → Bots → 关闭 **Bot Fight Mode**（Pro+ 的 Super Bot Fight Mode 把 “Definitely automated” 设为 Allow）<br>2) Security → WAF → Tools → **IP Access Rules** 加该服务器出口 IP（或 ASN）→ Action **Allow**<br>3) 或 Rules → **Configuration Rules**：`starts_with(http.request.uri.path, "/api/agent/")` → Security Level = Essentially Off / Bot Fight Mode = Off<br>4) 检查 WAF → Custom rules 里是否有匹配 `/api/*` 的挑战规则<br>5) 临时绕过：改用 Worker 的 `*.workers.dev` 地址（不经过自定义域名的 WAF），安装时 `--url https://<name>.<sub>.workers.dev`<br>6) 纯下载问题也可走对象存储：`node workers/export_agent_script.mjs --static` + `publish.ps1` 上传后设置 `AGENT_INSTALL_BASE`<br>⚠️ 注意：即使脚本下载绕过成功，**agent 轮询 `/api/agent/poll` 仍会被挑战**，所以必须让 Cloudflare 放行，或统一改用不被挑战的域名 |

---

## 更新日志

### V15.24（2026-11-03）

- **CDN 专用入站固定到 13707**：`vless-ws-cdn` 改为监听 `127.0.0.1:13707`（明文 WS，仅回环，供 Cloudflare Tunnel 回源）；13706 保持原样（直连用 TLS），不再混用。MASQUE 占用时自动退到 13716，也可用模板 `vlws.cdn_port` 覆盖。
- Cloudflare Tunnel 的 Public Hostname Service 指向 `http://127.0.0.1:13707` 即可。
### V15.23（2026-11-03）

- **修正 CDN 回源方式为 Cloudflare Tunnel 友好**：隧道在源站以**明文 HTTP** 访问本地服务，因此 `cdn_domain` 非空时改为生成 `vless-ws-cdn` 入站：`127.0.0.1:13716`（无 TLS，仅回环；可用模板 `vlws.cdn_port` 覆盖），而不是 443 的 TLS 入站。V15.22 的 443 方案仅适用于 Cloudflare 代理 + Origin Rule 回源。
- CDN 链接说明更新为「隧道回源 http://127.0.0.1:13716」；隧道 Public Hostname 的 Service 需指向该地址。
### V15.22（2026-11-03）

- **修复 CDN 回源**：CDN 域名只会回源 443，而此前只有 13706 的 vless-ws 监听，导致 CDN 链接不可用。现在节点配置了 `cdn_domain` 时，会额外生成 `vless-ws-cdn` 入站监听 **443**（同一 TLS/WS 路径，模板可用 `vlws.cdn_port` 覆盖），CDN 链接回源到该端口。
- CDN 链接说明改为「CDN 回源 443（vless-ws-cdn）」；Cloudflare 侧 SSL 模式需为 **Full**（源站自签证书）。
### V15.21（2026-11-03）

- **「节点配置」包含 MTProto**：用户详情的节点配置页在 sing-box 配置表下方新增「MTProto 配置」表（节点、主机:端口、密钥掩码、状态、创建时间、复制 tg 链接），`GET /api/uuids/{uuid}/configs` 响应新增 `mtproto` 数组（含已撤销客户端）。
### V15.20（2026-11-03）

- **移动端用户卡全局可见**：名称 / 节点数 / UUID / 状态卡（`ppm-hero`）抽成 `renderHero()`，在「我的订阅」「代理」「配置管理」「访问统计」「我的」全部页签展示，不再只在订阅页出现；多 UUID 切换 chip 一并保留。
### V15.19（2026-11-03）

- **移动端用户中心补齐「我的流量」**：在「我的订阅」页顶部与「访问统计」页顶部展示今日 / 近 7 天 / 累计用量与在线状态（组件 `MobileTraffic`，移动端样式），数据同 `/api/portal/traffic`。
### V15.18（2026-11-03）

- **用户用量详情支持时间范围**：弹窗新增「24 小时 / 7 天 / 30 天 / 自定义」切换，自定义用 RangePicker 精确选起止时间。
- **接口**：`GET /api/stats/traffic/users/{uuid}` 支持 `from` / `to`（毫秒时间戳，含边界），返回 `start` / `end`；按节点分布同样受时间范围约束。
- **图表**：跨度超过 3 天时横轴按日期（MM-DD）显示，避免小时标签挤在一起。
### V15.17（2026-11-03）

- **用户中心显示本人流量**：UUID 详情页新增「我的流量」条（今日 / 近 7 天 / 累计 + 在线状态），数据来自新增的 `GET /api/portal/traffic?uuid=`，仅允许查看登录邮箱名下 UUID（越权返回 403）。
- **管理后台清理记录**：用户流量页新增「清理记录」按钮（清空全部用户流量、小时聚合与连接记录）与详情弹窗里的「清理该用户记录」；对应 `POST /api/stats/traffic/clear`（支持 `uuid` 定向清理）。
### V15.16（2026-11-03）

- **版本发布**：本批次改动汇总——统一「授权服务」（sing-box + MTProto）、新增节点继承服务器已授权 UUID、授权动作后自动三向复核、离线节点最终一致、附件管理增强（新建/重命名/复制/预览/递归删除/更多可编辑类型）、MTProto `bootstrap_secret` 修复、删除配置/节点联动清理订阅。
- 服务端版本号对齐至 V15.16；Agent 1.8.7 不变。
### V15.15（2026-11-03）

- **新节点继承机器已授权用户**：在已授权过 UUID 的服务器上新增普通代理（sing-box）或 MTProto 节点时，自动继承该服务器已授权的 UUID 集合（sing-box `uuid_list` ∪ MTProto active 客户端）。
- **sing-box**：写入新节点 `uuid_list` → 重渲染 → 生成订阅配置 → 排队下发 → 授权后一致性复核。
- **MTProto**：为每个继承 UUID 创建 active 客户端（按节点伪装域名生成 secret）→ 重渲染下发。
- 离线服务器同样适用：下发排队，Agent 上线后补齐并在应用成功后触发复核（最终一致）；日志 `node-inherit` 记录继承数量。
### V15.14（2026-11-03）

- **「授权节点」改为「授权服务」**：同一台服务器的 sing-box 与 MTProto 服务统一列出，勾选即同时授权/撤销两类服务。
- **接口**：新增 `GET/PUT /api/uuids/{uuid}/services`，返回 `{kind, id, name, server_name, template_name, users, authorized}`；PUT 接收 `services:[{kind,id}]`，按目标集合增删。
- **MTProto 授权联动**：授权时自动为 UUID 创建 active 客户端（按节点伪装域名生成 secret），撤销时置为 revoked，并重渲染下发节点配置。
- **sing-box 授权联动**：沿用 uuid_list + 重渲染 + 订阅配置同步 + 授权后一致性复核。
### V15.13（2026-11-03）

- **修复 MTProto 创建报错「无法生成 bootstrap_secret：请先填写有效伪装域名」**：生成 secret 时误用了空的 `bootstrap_secret` 字段而不是节点填写的 `domain`，导致填了合法域名仍被拒绝；现改为优先用节点伪装域名，未填时才回落模板域名。
### V15.12（2026-11-03）

- **离线节点最终一致**：授权/解除授权等操作后的复核若发现节点离线，不再等待探测超时，而是直接按授权重渲染并把 `deploy` 排入队列（日志 `uuid-verify` 标记 pending）；Agent 上线后自动补下发。
- **上线即复核**：Agent 成功应用 deploy 后，对该服务器上的 sing-box 节点强制触发一次三向一致性复核，确保离线期间累积的变更全部收敛。
### V15.11（2026-11-03）

- **授权操作后自动复核一致性**：保存授权 / 解除授权 / 一键清理 / 删除单条配置后，异步对受影响节点做一次三向校验（机器实际配置、`uuid_list`、`configs`）；发现漂移立即重渲染、重建订阅配置并排队下发，日志记录 `uuid-verify`。
- 校验并发限制 4 个节点，避免批量操作时压垮 Agent。
### V15.10（2026-11-03）

- **删除节点配置联动解除授权**：单条/批量删除节点配置时，若该 UUID 在该节点已无任何剩余配置，自动从节点 `uuid_list` 移除、重渲染下发，并回收该 UUID 的订阅配置。
- **取消单个授权节点联动清理**：授权节点取消勾选后立即回收该节点的订阅配置；即使下发失败也先清理，避免用户侧留下失效配置。
- **删除节点绑定联动清理**：删除节点绑定时同步删除 `configs` 中该节点生成的全部用户配置与 `node_configs` 记录，返回 `deleted_configs`。
- **前端提示**：删除配置后提示实际解除授权的节点数量。
- **巡检三向一致**：同时核对「节点机器实际配置的用户」「数据库 `uuid_list` 授权」「`configs` 订阅配置」：缺配置、多配置、指向已删除节点的孤儿配置都会计入漂移并修复（孤儿配置直接清理）。
### V15.9（2026-11-03）

- **附件管理增强**：新增「上一级」返回、可点击面包屑、新建文件夹、重命名/移动、复制副本、图片预览、递归删除目录。
- **更多类型可在线编辑**：不再只认固定扩展名，只要是合法 UTF-8 文本（无 NUL 字节、≤2 MiB）即可编辑；图片/音视频/PDF/压缩包等仍只下载。
- **接口**：新增 `POST /api/attachments/folder`、`POST /api/attachments/move`、`POST /api/attachments/copy`；`DELETE /api/attachments?prefix=` 支持递归删除目录；目录占位对象不再显示为文件。
- **R2 客户端**：新增 `listAll`（自动翻页）与 `copy`（读取+写入）能力。
### V15.8（2026-11-03）

- **UUID 一致性巡检改为核对节点机器上的真实配置**：巡检先让每台节点的 Agent 以只读 `inspect` 动作读取实际 `sb.json`（返回 sha256、大小、去重后的用户名列表），再与数据库 `uuid_list`（平台授权）逐节点比对；漂移时按数据库授权重渲染、重建订阅配置并下发。
- **Agent 1.8.7**：新增 `inspect` 动作（只读检查配置文件，返回 sha256 / 用户名列表），不修改任何文件。
- **巡检任务**：结果新增 `probed` / `probe_failed`，任务详情显示「检查 N 个，读取节点配置 M 个（失败 K），漂移 D，修复 F」；读取失败的节点回退到数据库渲染内容比对。
- **修复**：巡检 SQL 未给 `uuid_list` 取别名导致解析为空、进而把节点用户列表重渲染为空的问题（已恢复全部节点并验证与授权完全一致）。
- **授权联动**：一键清理同步解除节点授权；重新授权时按最新用户列表重建订阅配置（`configs`），授权即新增、解除即回收。

### V15.7（2026-11-02）

- **自建 sing-box 正式上线**：`1.14.4-proxypilot.1`（官方默认标签 + `with_v2ray_api`，官方 release 不含该标签）。新增「系统设置 → 高级设置 → sing-box 分发基址」（`component_base_url`），组件走独立目录 `singbox/`，与 `agent/` 分开；Agent 按 `<base>/sing-box-<ver>-linux-<arch>.tar.gz` 下载，404 回落 GitHub Release。
- **业务 → 用户流量**（`#userst`）：在线用户 / 今日流量 / 区间流量 / 累计流量；按 UUID、归属、邮箱搜索与排序；详情含小时趋势与**按节点分布**；`stats/traffic/users` 增加 `up_today` / `down_today`，详情增加 `nodes[]`。
- **服务器监控**新增「节点连接与采集统计」区块：节点采集状态、连接数、实时上下行、累计流量，以及选定节点的实时连接明细（源 IP、目标、协议、入站、上下行、持续时长）。原独立「节点连接明细」页移除。
- **修复「更换 sing-box 后节点不可用」**：`applySingboxNode` 在未显式传入用户时优先按节点 `uuid_list` 渲染（与 UUID 巡检同口径），不再用模板占位用户覆盖线上授权。
- **Agent 1.8.6**：部署时**先切组件版本再写配置**；组件二进制改为「临时文件 + rename」（修复 `text file busy`）；解包改**流式写盘**（修复小内存节点 OOM）；安装失败把下载地址 / HTTP 状态带进任务输出。
- **修复统计上报中断**：Agent 轮询间隔由 2s 调整为 30s，消除 429 限流导致的统计空窗。
- **界面**：侧栏品牌区固定 64px 与顶部栏等高，菜单区独立滚动，标题不再被裁切。

### V15.6（2026-11-02）

- **移除 Xray**：节点管理只剩 sing-box 单一引擎；`xray.json`、Xray 版本选择、`vless-xhttp`（13708）与 `spiderX` 逻辑一并下线。同类场景改用 sing-box 的 **vless-reality（13705）**。
- **Agent 侧**：不再自动安装 / 切换 xray，服务探测名单去掉 xray（`sing-box / mtg / cloudflared`）；下发仍兼容老模板（历史 `enable_xray` 不再生效）。
- **数据清理**：迁移 `0010_remove_xray` 关掉模板的 xray 开关、删除 `node_configs` 里的 xray 记录与节点管理生成的 xhttp 订阅配置，并清空节点指纹触发重新对账。
- **API**：`/api/singbox/releases?engine=xray` 返回 400；模板/节点接口不再返回 `xray_*` 字段；`from-config` 只接受 sb.json。
- **新增「流量统计」页**（`#traffic`）：按用户展示上行 / 下行 / 活跃状态 / 小时趋势，并按节点展示实时连接明细（源 IP、目标域名、协议、上下行、持续时间）。
- **统计链路**：下发 sb.json 时注入 `experimental`（`clash_api` + `v2ray_api.stats`），**只绑 127.0.0.1** 并带每节点随机 secret；统计开启时入站用户名切成 UUID（稳定唯一，重排不串号）。Agent 每轮 poll 读本机两个接口，快照随下一次 poll 上报，服务端差分后写入 `user_traffic` / `user_traffic_hourly` / `node_connections`。
- **新接口**：`GET /api/stats/traffic/users`、`/api/stats/traffic/users/{uuid}`、`/api/stats/traffic/nodes`、`/api/nodes/{id}/connections`；小时聚合默认保留 90 天（`traffic_retention_days`）。

### V15.5（2026-11-02）

- **WARP（Cloudflare WARP）**：新增「运维 → 节点运维 → WARP」页面，控制面直接注册免费 WARP 设备，按 UUID 授权；每 UUID 一份凭据，支持 MASQUE 配置下载、刷新、撤销；节点服务器无需部署，不进入 sing-box 订阅。
- **用户中心**：一级入口改为「代理服务」，包含「Telegram 代理」与「WARP」；WARP 卡片支持下载 mihomo 配置。
- **管理端导航**：运维拆分为「服务器运维」（服务器管理、服务器监控）与「节点运维」（普通代理、MTProto、WARP）。
- **修复**：WARP 设置读取列顺序、授权列表与 uuid_info 的排序规则冲突。

### V15.4（2026-11-01）

- **系统管理合并**：系统管理与系统设置合并为单一「系统管理」入口，仅超级管理员可访问；旧 `#settings` 自动进入系统设置页签。
- **登录配置整合**：SSO / OIDC 并入「登录与验证」；原「站点 / 巡检 / 保留」更名为「高级设置」。
- **Agent 解锁检测**：新增 Agent 内置 `unlock` 任务，支持 `allow_exec=0` 的服务器；集成 CommonMediaTests 与 RegionRestrictionCheck，输出上限提升至 16 KB。
- **解锁结果修复**：HTTP 200 可达按已解锁展示；修复 Netflix、Disney+ 截断，Gemini 误报与地区跳转识别。
- **组件版本**：Agent 1.7.3 上报 sing-box / xray / mtg / cloudflared 版本；cloudflared 支持一键更新并重启。
- **性能**：图表改用自绘 SVG，首屏 JS 由约 1122 KB 降至 678 KB（gzip/br），降低约 40%。
- **Agent 分发**：统一使用对象存储 `https://s3.proxypilot.yangzifun.org/agent`，后台可配置。
- **体验修复**：发行版品牌图标、服务器卡片指标完整显示、详情状态映射、移动端账号状态、订阅模板列表与访问统计。

### V15.3（2026-11-01）

- **解锁详情**：服务器卡片新增 AI / 影视解锁检测；Netflix、Disney+ 使用 CommonMediaTests 实测，其余服务标记待确认，避免把 HTTP 200 误报为已解锁。
- **组件版本**：Agent 1.7.1 上报 sing-box / xray / mtg / cloudflared 版本，本机模式同步采集。
- **cloudflared 更新**：服务器详情支持一键下载最新 cloudflared、替换并重启服务。
- **系统图标**：监控卡片展示 Ubuntu / Debian / CentOS / Rocky / AlmaLinux / Fedora / Arch / Alpine / openSUSE 等发行版图标。
- **体验修复**：服务器详情状态映射、卡片指标完整显示、移动端账号状态、订阅模板列表、用户端访问统计与 UUID 信息接口。
- **Agent 分发**：统一使用对象存储 `https://s3.proxypilot.yangzifun.org/agent`，后台分发基址可配置。
- **主线清理**：仅保留 `main` 分支，历史功能分支已合并并删除。

### V15.2（2026-11-01）

- **备份管理**：独立「系统 → 备份管理」页，支持完整 ZIP 导出与事务式导入恢复。
- **Cloudflare 检查**：一键检查 Token、R2 读写与 Email Sending 权限，并自动清理测试对象。
- **本机 SSH**：控制服务器上报 SSH 状态，支持启用 / 停用；本机隐藏安装 Agent、探测与轮换密钥。
- **NAT 公网 IP**：内网 IPv4 自动替换为连接来源公网 IPv4；仅有 IPv6 时隐藏内网地址并显示公网 IPv6。
- **节点导航**：拆分「普通节点」与「MTProto（Telegram）」，各自包含节点绑定与协议模板两个子页面。
- **订阅修复**：支持 CRLF 模板，修复重复 `proxies` 键导致 Clash 订阅不可用。
- **用户端修复**：补齐访问统计与 UUID 信息 JSON 接口；移除用户信息中重复的「所有者」。
- **体验**：全局路由与页面加载动画、后台 favicon、移除旧页脚。

### V15.1（2026-11-01）

- **附件管理**：独立「附件管理」页，R2 全桶浏览、上传、下载、删除；文本文件在线读取/编辑（≤2 MiB）。
- **CDN**：支持配置 R2 公共自定义域，文件列表生成 `cdn_url`，可一键复制外链。
- **Cloudflare 统一凭证**：一个 API Token 同时驱动 R2 与 Email Sending；R2 S3 凭证由 Token 自动派生。
- **本机直控**：控制服务器自动登记为本地节点，无需 Agent 即可监控、执行、WebSSH、下发模板。
- **Agent 1.7.0**：心跳最短 2 秒；离线阈值 `max(60s, 4×心跳)`，修复 Cron 列别名导致的误判离线。
- **监控**：5 秒实时刷新，集群实时下行/上行/总流量卡片，单机实时网络与磁盘速率。
- **修复**：监控卡片主机名/运行时间/最后心跳缺失。

### V15.0（2026-11-01）

- **架构切换**：Go + MySQL 8 + Ant Design 成为生产主架构；旧 Cloudflare Worker 已从本分支移除；`PP_LEGACY_BASE` 仅作迁移期可选兜底。
- **路由**：150/150（含动态子路由）；内嵌 AntD bundle + boot 注入，无需 npm 构建即可打开后台。
- **WebSSH**：Go WebSocket 中继替代 Durable Object；Agent WS 唤醒 + 真实长轮询。
- **后台设置**：登录方式、Turnstile、SMTP / Cloudflare Email Sending、OIDC、站点/Agent 地址、离线通知与保留期。
- **Worker 能力补齐**：7 类批量任务、Cron（离线通知/清理/巡检）、管理员 OIDC、UUID 一致性修复。
- **部署**：本地对象存储与 Agent 分发、Dockerfile / docker-compose / smoke.sh / 生产部署文档。
- **质量**：108 个单测（Clash / sing-box / MTProto 黄金用例）；MySQL、Docker、真实 Agent 回归仍需在部署机完成。

### V14.2（2026-10-30）

- **Cloudflare Paid 能力落地**：批量任务改走 Queues（25s 预算、预算耗尽退避续投、cron 兜底、7 天/200 条保留期清理、单目标自动重试 3 次）；Agent 长轮询用 Durable Object 唤醒，任务与 WebSSH 秒级下发；补 D1 热点索引。
- **轮询成本优化**：默认轮询 10s → 60s，Agent 1.5.5 长轮询覆盖完整周期；后台可配置且不再被旧 Agent 回写覆盖（旧 Agent 服务端仍降级为 10s）。
- **DO Duration 优化**：Agent 1.6.0 改用 WebSocket（DO Hibernation），空闲时 DO 不再按长轮询挂起时长计费；连接断开时自动回退长轮询。
- **CPU 优化**：订阅链接生成把模板级计算移出用户循环，并按私钥缓存 X25519 公钥，同场景 CPU 降低约 200 倍。
- **移动端**：用户中心登录页重构为全屏 Hero + 白面板（≤899px），发送验证码内联、步骤单行、CTA 沉底；「我的订阅」移除重复的 Telegram 卡片。
- **监控**：全球分布小图改为聚合气泡（显示台数）+ 地区数量条，大图保留精细散点、标签与缩放。
- 测试：e2e 192 项通过，Worker 各测试全绿；Agent **1.6.0** 已发布。

### V14.1（2026-10-29）

- **批量操作后台化**：一键下发、批量授权、Agent 批量更新、UUID 巡检等改为「提交即返回」，后台分片执行（`waitUntil` 先跑 15s，跑不完由 cron 续跑），顶栏「任务」里看进度、取消、只重试失败项；
- 新增 `batch_jobs` / `batch_job_items` 两张表与 `/api/jobs` 接口；旧接口默认返回 `{async:true, job_id}`，`?sync=1` 保留同步行为；
- **效率优化**：订阅链接按「链接指纹」判断是否需要重写（链接没变直接跳过）、链接写入/删除改为 `DB.batch` 批量提交、同一任务加锁防并发；
- 新增 `workers/test_jobs_api.mjs`（26 项：物化 / 进度 / 部分失败 / 取消 / 重试 / 锁 / 预算续跑）。
- 监控页去掉「升级 Agent」与「通知设置」：Agent 升级统一在「服务器管理 → 更新 Agent」，通知设置也移到服务器管理页。
- 节点可填 **CDN 域名**：VLESS-WS 会同时生成「直连源站」和「CDN:443」两条链接，同一 UUID 两种用法；服务端端口不变，CDN 转发由使用者自行配置。

### V14.0（2026-10-29）

本次是一个功能版本，汇总：

- **MTProto（Telegram）部署**：模板 + 节点两级、按 UUID 独立 secret、撤销只断一人；配置独立成表，不进任何订阅；用户中心可看二维码 / 发到邮箱；
- **MASQUE 并入 sing-box**：改为 `masque-server` endpoint 随 sb.json 下发，不再独立进程；默认模板不启用，低于 sing-box 1.15 有版本闸门；
- **引擎版本可选**：协议模板能选 sing-box / Xray 的正式版或预览版（也可手填 tag），下发时 Agent 自动安装并切换；
- **组件版本采集**：Agent 上报 sing-box / xray / mtg-multi 版本，服务器详情直接显示；
- **安装可停用 SSH**：安装命令支持 `--disable-ssh`（Agent 运行后才执行）；
- **SSH 启停集中到「服务器管理」**：列表新增 SSH 状态列（来自 Agent 上报），支持单台与勾选后批量启用 / 停用，统一走后台任务；**服务器监控详情只读显示 SSH 状态**，不再提供操作按钮；
- **协议模板补说明**：8 个协议的端口 / 传输 / 用途在模板页与标签提示里可见；
- Agent 升至 **1.5.1**：新增 `mtg` 服务监控、SSH 服务状态上报（`ssh.enabled/active`）。
- Agent **1.5.2**：部署前自动补 systemd 单元；配置未变化但服务未运行时自动拉起（修复 MTProto 下发后「未启动」）。
- Agent **1.5.3**：SSH 状态同时检查 `ssh.service` 与 `ssh.socket`（Ubuntu 套接字激活），并区分「运行中但未自启」，修复 SSH 实际已启用却显示已停用。
- Agent **1.5.4**：长轮询等待任务（Durable Object 唤醒），下发延迟从轮询间隔降到秒级；旧 Worker 无 `AGENT_HUB` 时自动回退普通轮询。
- Agent **1.5.5**：长轮询覆盖完整轮询周期（默认 60s），在降低请求量的同时保持任务下发 / WebSSH 秒级唤醒；打开 WebSSH 时服务端会主动 push 唤醒 Agent。
- Agent **1.6.0**：WebSocket + DO Hibernation，空闲时不再持续挂起长轮询（DO Duration 接近 0）；断线自动回退长轮询。

> 升级注意：需先发布并推送 Agent 1.5.0，版本选择与组件版本展示才会生效；旧 Agent 会忽略新增字段。

### V13.8（2026-10-29）

- **安装时可停用 SSH**：服务器「安装命令」弹窗新增开关，安装命令追加 `--disable-ssh`；安装脚本在 **Agent 服务确认运行后** 才执行 `systemctl disable --now sshd|ssh`，装失败或 Agent 没起来会跳过并打日志，避免失联；
- 服务器详情「服务状态」旁新增「停用 SSH / 启用 SSH」：复用 restart 任务下发 `systemctl enable --now sshd|ssh`，可在面板里随时恢复（也可用 WebSSH 执行）；
- 安装命令默认不带该参数，行为与之前一致。

### V13.7（2026-10-29）

- **协议模板可选引擎版本**：模板里能选 sing-box / Xray 的具体版本（下拉同时列出正式版与预览版，也可手填 tag；留空跟随 Agent 内置默认）；
- 「下发」时把版本随配置带给 Agent：部署载荷新增第 15 个 wire 字段 `install_versions`（老 Agent 会忽略，兼容），Agent 比对已装版本，不一致就下载安装并切换，再跑 `sing-box check` 与重启；
- 新增 `GET /api/singbox/releases?engine=singbox|xray`（GitHub Release，D1 缓存 6 小时；拉取失败回落缓存）；
- Agent 升到 **1.5.0**；老 Agent 不认这个字段，配了版本的模板需要先升级 Agent 才会真正切换。

### V13.6（2026-10-29）

- **MASQUE 并入 sing-box**：模板启用后生成 `endpoints[{ type: "masque-server" }]` 写进 sb.json（TLS 复用模板证书、用户取模板 users），不再下发独立 `masque.json`、不再起独立进程；旧模板的 `masque_json` 字段保留但不再使用；
- 版本闸门：按 Agent 上报的 sing-box 版本拦截——低于 1.15.0 时明确报错，不会把 1.14.x 节点打挂；默认模板不启用 MASQUE；
- 模板编辑器改为「开关 + URI 路径」，从粘贴的 sb.json 里也能反推 `masque-server`；
- **协议模板补说明**：模板页新增可折叠的「协议说明（端口 / 传输 / 用途）」，协议标签 hover 也能看到；补上 `mtg` 服务监控。

### V13.5（2026-10-29）

- **采集组件版本**：Agent 每轮上报 `versions`（`sing-box version` / `xray version` / `mtg-multi version`，5 分钟缓存），服务器详情「服务状态」直接显示 `v1.14.4` 这类版本号；
- 服务监控名单补上 `mtg`（MTProto 用），与 sing-box / xray / cloudflared 一致可重启；
- Agent 升到 **1.4.1**（需重新发布二进制后，一键更新才会推送）。

### V13.4（2026-10-29）

- **MTProto 改为「模板 + 节点」两级**：新增 `mtproto_templates`（监听端口默认 `13709`、伪装域名、模式、并发、黑名单等），节点只存覆盖项（`0` / `空` / `-1` = 继承）；
- 模板可编辑，保存后自动按新参数重渲染全部绑定节点并排队下发；模板行另有「下发」按钮，节点页有「全部下发」；
- 节点端可覆盖域名 / 端口 / 模式等，覆盖优先于模板；页面用「继承」标签标出来源；
- 老库自动补 `mtproto_nodes.template_id` 列，既有节点保持原行为（自身值即覆盖值）。

### V13.3（2026-10-29）

- **新增 MTProto（Telegram）部署**：节点管理多一个页签，引擎 `mtg-multi`（单进程多 secret），默认端口 `13709`；
- 新增 `mtproto_nodes` / `mtproto_clients` 两表，按 UUID 授权、每用户独立 secret；撤销 / 轮换只断该用户，SIGHUP 热加载；
- 配置独立于订阅：不写 `configs`，`/sub`、`/batch-configs`、`/clash-config` 里不会出现 `tg://`；
- 用户中心新增「Telegram 代理」卡片：二维码本机渲染 + 一键发送到绑定邮箱（60 秒 / 每日 5 次限流）；
- Agent 自动安装 `mtg-multi`（GitHub Release v1.13.0）并补 `ExecReload`；服务监控新增 `mtg`；
- 新增 `workers/test_mtproto_api.mjs`（98 项）。

### V13.2（2026-10-29）

- **Ant Design 6**：升级到 antd **6.6.8** + @ant-design/icons **6.3.4**（React 18.3 满足 v6 要求）；侧边栏底部由「V13.1 · AntD 5」改为「V13.2 · AntD 6」，登录页文案同步为 Ant Design 6
- **Ant Design Charts 全覆盖**：柱状图 / 环形图 / 折线面积 / 迷你趋势改用 **@ant-design/plots 2.6.8**（G2 5）；「全球分布」点位用 **Scatter** 按经纬度/国家中心点投影到内置 1000×400 世界底图（无需 Mapbox / AMap key）；仪表盘用 antd `Progress type="dashboard"`
- **图表稳定性**：关闭入场动画，修复折线/面积首帧空白；无 canvas 环境（jsdom / 老浏览器）自动回退轻量 SVG
- **全球分布**：小图悬停圆点显示地区 / CPU / 内存，大图把地区名直接写在地图上（无卡片、无引线），点击圆点进详情
- 清理 antd v6 弃用 API：`destroyOnClose`→`destroyOnHidden`、`destroyInactiveTabPane`→`destroyOnHidden`、Table `size="middle"`→`"medium"`、Space `direction`→`orientation`
- 版本号：`APP_VERSION` / `UI_VERSION` / `web-antd/package.json` → **13.2**
- 测试：smoke 22 / render 38 / e2e 180 全绿

### V13.1（2026-10-29 增补）

- **前端依赖升级**：antd 5.22 → **6.6.8**、@ant-design/icons 5.5 → **6.3.4**（React 18.3 满足 v6 要求）；清理 v6 弃用 API（`destroyOnClose`→`destroyOnHidden`、`destroyInactiveTabPane`→`destroyOnHidden`、`Table size="middle"`→`"medium"`、`Space direction`→`orientation`），e2e 180 全绿
- **Ant Design 图表**：柱状图 / 环形图 / 折线面积 / 迷你趋势改用 **@ant-design/plots 2.6.8**（G2 5）渲染，仪表盘改用 antd `Progress type="dashboard"`；「全球分布」地图的点位改用 **Scatter** 打在 1000×400 世界底图上（直接按经纬度/国家中心点投影，无需 Mapbox/AMap key），无 canvas 环境回退 HTML 圆点。构建体积约 1.28 MB gzip，占 64 MiB 上限约 2%
- **移动端用户中心重做**：底部 4 个页签（我的订阅 / 配置管理 / 访问统计 / 我的），账号信息与退出登录收进「我的」，顶部导航栏不再放头像 / 退出；顶部渐变订阅卡（名称 / 状态 / 节点数 / UUID 复制 + 多 UUID chip 切换），订阅生成、配置列表、统计 KPI、账号表单统一视觉
- **访问统计**：「最近访问」由表格改为「一行摘要 + 弹窗明细」；移动端支持下拉刷新与失败重试；修复移动端趋势图恒为 0（`AreaChart` 读行上扁平字段，旧代码塞在 `values` 里）、环形图中心恒为 0（`DonutChart` 内部 `Number()` 与 `fmtNum` 的千分位冲突）、折线图末位 x 轴标签与前一个挤在一起
- **服务器监控**：顶部三卡（集群在线率 / 资源均值 / 全球分布）改为 grid 等高；双栈机器 IPv4 / IPv6 分行显示，新增 `servers.last_ip4`（Agent 上报的本机 IPv4，幂等补列，老 Agent 也生效），只有一种就只显示一行；`/api/servers`、`/api/servers/status`、`/api/servers/monitor` 均带出 `last_ip4` / `last_ip6`
- **全球分布**：支持大图弹窗（滚轮缩放 1x–4x / 拖动平移 / 重置），每个地点带地区标签与引线，标签自动避让圆点与彼此
- **远程终端**：「服务器管理 → 命令」由一次性执行改为交互式终端（连续输入、输出追加、↑/↓ 历史、`cd` 跨命令保持、Ctrl+L 清屏）；`/exec` 返回 `task_id`，前端轮询任务队列取回输出
- **实时终端（WebSSH）**：浏览器 ↔ Durable Object ↔ Agent 出站 WebSocket ↔ PTY，打开即连、支持 vim / top 等交互程序；Agent 1.3.0 新增 `internal/shell`（PTY + 最小 WebSocket 客户端），Worker 新增 `ShellSession` Durable Object 与 `/api/shell/*` 路由
- **配置管理**：协议 / 备注定宽、配置内容列吃满整页并完整换行，去掉 64 字截断与横向滚动
- **外部订阅源**：移动端 Popup 接入新增 / 启停 / 删除（与 PC 能力对齐）
- **README 重写**：补齐快速开始、目录结构、部署（绑定 / 密钥 / cron / 构建 / 升级回滚）、使用指南（管理员配置顺序、PC / 手机用户中心、客户端导入、API 示例）、环境变量按 bindings / 变量分类
- 测试：e2e 137 → 168、Agent 单元 203 项，smoke 22 / render 38 全绿
- 版本号：**V13.1**（本轮为功能增补，`APP_VERSION` / `UI_VERSION` 未变）

### V13.1（2026-10-28）

- **单文件交付**：旧版内置页面（登录页 / 用户中心 / 管理后台 / 公共生成器）已从 `workers/worker.js` 移除；前端改为 React 18 + Ant Design 6，`npm run build` 直接把产物写进 `workers/worker.js` 的 `GENERATED ASSETS` 区间，一个文件即可部署（当前约 1.28 MB gzip，约占 64 MiB 上限的 2%）
- **入口收敛**：`/admin` 管理后台、`/` 与 `/me` 用户中心；`/login`、`/manage` 302 跳转；`/sub`、`/clash-config`、`/batch-configs` 等订阅输出与 `/manage/*` API 完全不变
- **用户中心**：PC 单页（账号卡两栏：订阅 + 用户信息 / 登录邮箱、订阅生成、访问统计、配置管理、外部订阅源），移动端三页 + 底部导航（10-29 增补为 4 页签）；订阅模板按 `name` 生效且仅作用于 Clash
- **服务器监控**：KPI 并入「集群在线率」卡；告警改为弹窗，仅在有告警时出现
- **IP 记录**：`getClientIp()` 依次取 `cf-connecting-ip` / `cf-pseudo-ipv4` / `true-client-ip` / `x-real-ip` / `x-client-ip` / `x-forwarded-for`，并清洗 `::ffff:`、端口与 `unknown`
- 版本号 V12.28 → V13.1

### V12.11（2026-10-28）

- **安装脚本多渠道**：新增免 `/api` 别名 `GET /install.sh`（`/agent/install.sh` 亦可），规避只拦 `/api/*` 的 WAF 规则
- **对象存储分发**：`agent/dist/install.sh` 由 `node workers/export_agent_script.mjs --static` 生成（`ORIGIN` 留空、二进制基址写死），`publish.ps1` 一并上传；新增环境变量 `AGENT_INSTALL_BASE`，设置后安装命令自动变成 `curl ... <base>/install.sh | sudo sh -s -- --key <key> --url <本站>`
- **排查**：机房 IP 访问 Worker 域名被 Cloudflare 403 时，按 readme 故障排查表处理（放行 IP / 关 Bot Fight Mode / 走对象存储）
- 版本号 V12.10 → V12.11

### V12.28（2026-10-28）

- **后台按钮与同行控件高度对齐**：`card-head` / `inline-form` / `chart-controls` 里的下拉、输入框、按钮统一 34px；
  分页条（`page-ctrl`）统一 30px；表格里的 `.nav-btn.small` 保持紧凑不受影响
- 覆盖：订阅模板（模板前缀下拉 + 刷新 / 新建文件）、监控（自动刷新 + 通知设置）、
  访问日志（保留期限 + 预览待删除数量）、分页（每页下拉 + 上一页 / 下一页）、统计范围 / 图表视图
- 浏览器探针实测：修复前 7 处高度差（24 vs 43 / 28 等），修复后 0 处；版本号 V12.27 → V12.28

### V12.27（2026-10-28）

- **用户中心登录页对齐**：邮箱 / 验证码输入框与右侧「发送验证码」「登录」按钮统一 44px 等高，按钮不再被移动端 `width:100%` 拉满
- check_pages 增加 `portal-login-row` / `portal-login-btn` / `height: 44px` 标记；版本号 V12.26 → V12.27

### V12.26（2026-10-28）

- **修复后台「订阅分组」查看外部订阅源报「请先用邮箱登录」**
  · 原因：`/manage/uuid-sub-sources` 只认用户端邮箱令牌，而后台页面用 admin JWT 且没带 Authorization
  · 服务端新增 `adminSessionOf` / `portalOrAdminGate` / `portalOrAdminGateByRow`：admin 令牌放行任意 UUID，
    用户令牌仍按归属校验（跨账号 403）
  · 后台 SPA 的 `subSourceApi` 与外部订阅源列表请求补上 `Authorization: Bearer <admin token>`
- test_portal 46 → 51：admin 令牌放行 / 无令牌 401 / 用户跨账号 403 / 按行放行 / 记录不存在 404
- 版本号 V12.25 → V12.26

### V12.25（2026-10-28）

- **性能优化（前端传输 / 订阅 / Agent 轮询）**
  · HTML 页面（`/`、`/manage`、`/login`、`/admin`）加 `ETag` + `Cache-Control: private, no-cache`，命中 `If-None-Match` 直接 304：`/admin` 从每次重下 353KB 变为 0 字节、本地 24ms → 1.7ms
  · Chart.js 从首屏移除（`/admin`、`/manage`），改为首次画图时动态加载：首屏少 210KB 阻塞下载；顺带移除未使用的 xterm addon
  · 订阅访问统计：`INSERT` 日志 + `UPDATE access_count` 合并成一次 `DB.batch`，并去掉每次请求的 `sqlite_master` 表存在性检查
  · 外部订阅源结果用 Cache API 缓存 120 秒，客户端刷新不再每次外呼第三方
  · Agent 轮询：任务回收 / 过期维护从每次 poll 改为每 5 次（首次必跑）；`provisionSingboxOnAgentInstall` 只在首次接入 / 前两次 / 每小时自愈时检查
- check_pages 新增 ETag/304、首屏资源断言；版本号 V12.24 → V12.25

### V12.24（2026-10-28）

- **修复 worker.js 的 TypeScript 报错**（`Property 'vlws'/'xhttp' does not exist on type 'object'`、`'warp'` 联合类型）
  · `singboxProtoOptions` 标注 `Record<string, any>`，`singboxPorts` 标注 `Record<string, number>`
  · `singboxTemplateFromSb` / xray 解析分支补全 `warp`、`xhttp` 字段与 `ports/paths/ss/hy2` 的类型，消除结构联合
  · `singboxApplyNode` 失败分支不再参与 files 类型推断（此前 `applied.files` 被推断成预览/下发两种结构）
  · 顺带修复重复的 `warp_base` 字段（后者覆盖前者，导致表单里的 warp_base 被忽略）
- 新增 `workers/tsconfig.json` + `workers/worker-types.d.ts`（cloudflare:sockets / cf / URLPattern 声明），`npx tsc -p workers` 0 error
- 版本号 V12.23 → V12.24

### V12.23（2026-10-28）

- **统一 sing-box 配置文件返回结构**：预览与下发此前字段不一致（预览是 `key/name/engine/path/service_name/check_cmd/content`，下发是 `node_config_id/name/changed/version/path`），调用方按联合类型处理会报 `Property 'changed' does not exist`（ts(2339)）
  · 新增 `singboxFileView` / `singboxFilesView`，所有 `files` 统一为 10 个字段：`key/name/engine/path/service_name/check_cmd/content/node_config_id/version/changed`
  · 预览项固定 `changed=false`、`version=0`、`node_config_id=0`，下发项返回真实值与内容
  · `/apply`、`/deploy`、`/deploy`（默认配置）、`/apply`（force）等响应新增顶层 `changed` 计数，前端不再自己遍历 `files`
- 版本号 V12.22 → V12.23

### V12.22（2026-10-28）

- **订阅模板页新增「存储桶文件」区块**：直接浏览、编辑对象存储里的模板（`rule/configs/*.yaml`）与地区配置（`rule/*.list`）
  · 前缀切换：`rule/configs/`（模板）/ `rule/`（全部）
  · 文件列表显示大小与更新时间，点「编辑」弹出编辑器，保存写回存储桶
  · 「新建文件」可在当前前缀下创建新文件
  · 订阅模板行如果 URL 指向存储桶，会多一个「编辑源文件」按钮，一键跳到对应文件
- 新增接口：`GET /api/bucket/files`、`GET /api/bucket/file`、`PUT/POST /api/bucket/file`、`DELETE /api/bucket/file`
  · 依赖 R2 绑定 `bucket`（`r2_buckets`），未绑定时返回 503 并提示
  · 只允许 `BUCKET_PREFIX`（默认 `rule/`）下的文本文件；拒绝 `..`、前缀外路径；单文件上限 512KB
  · 写入时按扩展名设置 Content-Type（yaml / json / md / txt）
- 版本号 V12.21 → V12.22

### V12.21（2026-10-28）

- **协议配置列表拆分端口列**：原来「启用协议」单元格是「协议 :端口」两段，现拆成两列
  · 启用协议：ss / anytls / hy2 / tuic / reality / vlws …
  · 端口：与协议逐行对齐（尊重模板里按协议自定义的端口）
  · 端口列悬停显示 `ss:13702 · anytls:13703 …` 的完整对照
- 版本号 V12.20 → V12.21

### V12.20（2026-10-28）

- **协议配置列表去掉「内置用户」列**：模板只展示 模板 / 启用协议 / 更新时间 / 操作
- 绑定节点弹窗里选择协议模板时也只显示模板名（不再带任何用户信息）
- 「默认配置」推送的确认文案改为「模板 sb.json 里自带的用户」，避免与账号概念混淆
- 版本号 V12.19 → V12.20

### V12.19（2026-10-28）

- **协议配置的「用户」列改为「内置用户」**：它来自模板 `sb.json` 的 `inbounds.users`（新建模板时示例里是 `user01`），仅用于预览/示例，**不参与实际下发**
- 悬停该列可看到内置用户名单与说明；绑定节点的模板下拉不再显示「N 用户」
- 真正下发的用户来自「节点配置 → 授权节点」里的 UUID（下发时会整体替换 `users_json`）
- 版本号 V12.18 → V12.19

### V12.18（2026-10-28）

- **Agent 分发基址迁移**到新对象存储：`https://s3.proxypilot.yangzifun.org/agent`（示例：`https://s3.proxypilot.yangzifun.org/agent/proxypilot-agent-linux-amd64`）
- `AGENT_BINARY_BASE_URL` 默认值、`install.sh` 导出示例、`publish.ps1` 默认 Endpoint/Bucket、`build.sh` 提示、readme 全部同步
- 旧桶仍可用：需要回退时设置环境变量 `AGENT_BINARY_BASE_URL=https://s3.yangzifun.org/proxypilot`
- **`rule/` 链接迁移**：`configs/*.yaml` 的 `rule-providers` 与 README 中的下载地址从 Git 仓库链接改为 `https://s3.proxypilot.yangzifun.org/rule/`（不再随 commit 变化而失效）；新增 `workers/check_rule_links.mjs` 自检
- 版本号 V12.17 → V12.18

### V12.17（2026-10-28）

- **监控页预留区上线**「集群概览」：
  · 在线率环形图（在线 / 总数）
  · CPU Top 5 横向条形（按在线服务器 CPU 排序，>75 橙 / >90 红）
  · 告警列表：离线、状态未知、CPU≥80/90、内存≥85/90、磁盘≥85/90、负载 > 核数、服务 failed、agent 版本过低；按严重度排序，最多 8 条，点击直接打开该服务器详情
- 右侧地图保持 380px；窄屏（≤1240px）自动隐藏 CPU 条形，≤900px 上下堆叠
- 版本号 V12.16 → V12.17

### V12.16（2026-10-28）

- **监控卡片精简**：`内核 / 平台`、`Tunnel`、`Top 进程` 从卡片移除，卡片只保留 CPU / 内存 / 磁盘 / 负载 / 网络 / 磁盘 IO / 连接 / 进程数
- **详情新增「运行信息」分区**：内核 · 平台、Tunnel 状态、Top 6 进程（名称 / 内存 / CPU / pid）
- 版本号 V12.15 → V12.16

### V12.15（2026-10-28）

- **右上角显示用户名**（`uuid_info.owner`，未设置时回退邮箱）；展开下拉显示「名称 + 邮箱」
- **自助修改绑定邮箱**：下拉 → 修改邮箱 → 向新邮箱发 6 位验证码（10 分钟有效、60 秒重发间隔）→ 校验通过后立即生效；邮箱唯一性照样校验（已绑定他人返回 409）
- 改绑成功后服务端换发新令牌（旧令牌的 `sub` 是旧邮箱），页面自动替换，无需重新登录
- 新接口：`POST /api/portal/email/send-code`、`POST /api/portal/email/verify`
- 版本号 V12.14 → V12.15

### V12.14（2026-10-28）

- 配置管理右上角：显示登录邮箱（按钮内只放邮箱地址，宽度 200px / 280px，超长省略；悬停 tooltip 与下拉里的「邮箱：」块显示完整地址）
- 版本号 V12.13 → V12.14

### V12.13（2026-10-28）

- **删除用户端列表页**：`/` 只剩邮箱登录，登录成功直接进 `/manage`；不再有 UUID 卡片 / 订阅卡片
- **取消手动输入 UUID**：`/manage` 的「配置管理入口」弹窗、`生成UUID`、`编辑UUID信息` 全部下线；没有登录令牌直接跳回 `/`
- **右上角显示邮箱**（原显示 `UUID: xxxxxxxx...`），退出登录清除令牌后回登录页
- **服务端强制**：`/manage/configs|stats|uuid-info|uuid-check|uuid-sub-sources|all-uuid-info` 等接口要求 `Authorization: Bearer <用户令牌>` 且 UUID 必须属于该邮箱（401 / 403）；`/manage/entry`、`/manage/generate-uuid` 关闭
- 订阅输出 `/sub/:uuid`、`/batch-configs/:uuid`、`/clash-config` 保持公开（客户端要用）
- 版本号 V12.11 → V12.13

### V12.10（2026-10-28）

- **一个账号一个 UUID**：`uuid_info.email` 改为「邮箱唯一」（大小写不敏感），后台新建 / 改绑到已被占用的邮箱返回 **409 `email_taken`**，公开的 UUID 信息接口同样拦截
- 数据库补 `uuid_info(lower(email)) WHERE email <> ''` 唯一索引（历史数据已有重复时索引创建会跳过，写入仍被应用层拦住）
- 空邮箱不受限制（方便先建分组后补信息）
- 用户中心因此最多只列 1 个 UUID，登录直达 `/manage?uuid=` 成为常态
- 版本号 V12.9 → V12.10

### V12.9（2026-10-28）

- **登录直达**：只有一个可用 UUID 的账号，用户中心登录成功后直接 `location.replace('/manage?uuid=<uuid>')`；已有令牌再次进入同样直达
- 多 UUID 账号仍显示列表选择；UUID 为「停用」时不跳转
- 想强制停留在列表：访问 `/?list=1`
- 版本号 V12.8 → V12.9

### V12.8（2026-10-28）

- **用户中心 → 配置管理**：每个 UUID 卡片新增「查看配置 / 管理订阅」入口，直达 `/manage?uuid=<uuid>`
- **`/manage` 深链**：URL 带 `?uuid=` 时自动写入本地、解除入口门禁并切到「配置管理」标签加载该 UUID（原来的生成器 / 配置管理 / 订阅管理功能全部保留）
- 版本号 V12.7 → V12.8

### V12.7（2026-10-28）

- **监控页布局**：全球分布地图缩小到右上角（380px 紧凑卡片），同一排左侧留出「预留区域」，供后续放置图表 / 告警 / 用量概览
- 窄屏（≤900px）自动上下堆叠

### V12.6（2026-10-28）

- **用户中心登录页**：与后台 `/login` 共用同一套样式（`panelCss` + 页脚），登录框、按钮、输入框、页脚版本号完全一致
- **人机验证**：用户端「发送验证码」接入 Cloudflare Turnstile（与后台登录同一套校验；未配置 `TURNSTILE_SECRET_KEY` 时自动跳过）
- **一致性修复**：取消节点授权 / 删除 UUID / 一键清理配置，都会同步重渲染节点配置、删除订阅配置并排队下发（含"最后一个用户"场景）
- 版本号 V12.5 → V12.6

### V12.5（2026-10-28）

- **用户端**：新增 `/`（= `/me`）用户中心 —— 邮箱验证码登录 → 列出该邮箱绑定的 UUID → 生成订阅（原始 / Base64 / Clash）与二维码；用户令牌与后台账号隔离
- **页面分工**：`/` 改为用户中心，原「批量生成器」移到 `/manage`；`/login`、`/admin` 不变
- **监控**：全球分布地图（经纬度打点）、CPU / 内存 / 磁盘环形图、负载柱状图、连接堆积条；服务状态矩阵下线，服务状态与重启并入服务器详情（分区卡片）
- **性能**：授权节点同步 SQL 从「用户 × 链接」逐条往返（约 260 次）降为常数级（约 3 次）；多节点授权并行、UUID 巡检改为后台任务
- **配置管理**：支持批量勾选删除与一键清理某个 UUID 的全部节点配置
- Agent 版本 1.2.7

### V12.0（2026-10-28）

- **权限收敛**：仅 `admin` 可进「系统管理」，其余账号开放服务器管理 / 节点管理 / 监控 / 日志
- **协议**：新增 **TUIC v5** 取代 vmess-ws（端口 13704）；协议引导补全 TUIC 与「其他协议」章节
- **监控**：新增 **IPv6 采集与展示**；移除 CPU 温度；卡片不再重复服务状态点阵与内核
- **安全性**：Xray 配置改为双栈监听、随机 UUID、派生 spiderX；占位 UUID 保存即拦截
- **配置**：sb.json 域名解析改 `prefer_ipv4`（双栈，优先 v4）
- **示例**：「填入示例」产出与生成器一致（含 TUIC、随机凭证、端口 +8）
- Agent 版本 1.2.7

### 历史要点

- V11.x：sing-box 协议统一编排、MASQUE、Agent 版本化下发、服务器监控面板、UUID 一致性巡检、TUIC
- V10.x：后台改版（左侧菜单 + 顶部工具栏）、节点配置模板
- V8.x：弃用 SSH，改为 Go Agent 主动接入；对象存储直连分发二进制
- V7.x：多管理员账号、JWT、MFA、API 密钥、OIDC/邮箱登录

---

## 适用场景

- 自建节点（sing-box / MASQUE）统一编排与下发
- 多台服务器、多 UUID 的订阅分发与访问统计
- 想要「面板 + Agent」但不想开放管理端口的环境

## 安全说明

- 用户端令牌（`role=user`）与后台令牌完全隔离，越权调用后台接口一律 403；
- 订阅链接含 UUID，等同于密码，前端多处提示「请勿公开分享」；
- 后台可关掉密码登录只留 OIDC / 邮箱验证码；Turnstile 未配置时自动跳过；
- Agent 只做出站连接，服务器不开放管理端口；接入密钥可随时轮换，旧密钥立即失效；
- 公开示例配置里的密钥在保存时会被拦截，占位 UUID 同样拒绝入库。

## 许可证

MIT
