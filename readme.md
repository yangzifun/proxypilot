# ProxyPilot

![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white) ![MySQL](https://img.shields.io/badge/MySQL-8.0-4479A1?logo=mysql&logoColor=white) ![React](https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=black) ![Ant Design](https://img.shields.io/badge/Ant%20Design-6-1677FF?logo=antdesign&logoColor=white) ![Agent](https://img.shields.io/badge/Agent-Go-00ADD8?logo=go&logoColor=white) ![Version](https://img.shields.io/badge/Version-V1.3-blue.svg) ![License](https://img.shields.io/badge/License-MIT-green.svg)

---

## 目录

- [界面预览](#界面预览)
- [架构总览](#架构总览)
- [技术栈](#技术栈)
- [快速开始](#快速开始单机直接安装不使用容器)
- [目录结构](#目录结构)
- [旧版代码](#旧版代码)
- [功能总览](#功能总览)
- [协议与端口](#协议与端口)
- [VPN 与组网协议栈](#vpn-与组网协议栈)
  - [1. 分层封装总览](#1-分层封装总览)
  - [2. 客户端数据面内部路径](#2-客户端数据面内部路径)
  - [3. 隧道帧格式（字节级）](#3-隧道帧格式字节级)
  - [4. HTTP/3 报文内容](#4-http3-报文内容)
  - [5. QUIC 传输层](#5-quic-传输层)
  - [6. TLS 与证书模式](#6-tls-与证书模式)
  - [7. JWT 令牌结构](#7-jwt-令牌结构)
  - [8. 一次 ping 的完整字节流](#8-一次-ping-1111-的完整字节流)
  - [9. 组网（Mesh）](#9-组网mesh)
  - [10. 抓包与调试](#10-抓包与调试)
  - [11. 性能与调优要点](#11-性能与调优要点)
  - [12. 版本对应](#12-版本对应2026-11)
- [权限模型](#权限模型)
- [生产部署](#生产部署单机直接安装)
- [使用指南](#使用指南)
- [Go Agent](#go-agent)
- [API 概览](#api-概览)
- [开发与测试](#开发与测试)
- [开发文档（docs/DEVELOPMENT.md）](docs/DEVELOPMENT.md)
  - [API 参考（367 条路由）](docs/dev/API.md)
  - [数据字典（61 张表）](docs/dev/DATABASE.md)
  - [协议实现详解](docs/dev/PROTOCOL.md)
  - [Agent 与任务协议](docs/dev/AGENT.md)
  - [客户端实现](docs/dev/CLIENT.md)
  - [前端开发](docs/dev/FRONTEND.md)
  - [运维手册](docs/dev/OPERATIONS.md)
- [故障排查](#故障排查)
- [安全说明](#安全说明)
- [许可证](#许可证)

---

## 界面预览

> 所有截图均使用脱敏数据：用户名、UUID、IP、域名均已替换为示例值。

### 管理后台

![管理后台 · 系统概览](docs/screenshots/admin-dashboard.png)

访问统计、趋势与最新订阅；左侧为完整功能分组（用户与业务 / 节点与网络 / 运维与安全）。

### 用户中心

![用户中心 · 邮箱验证码登录](docs/screenshots/portal-login.png)

邮箱验证码登录，无需密码；登录后查看订阅、用量与设备。

### 桌面客户端

| 主页面 | 连接详情 | 首选项 |
|---|---|---|
| ![客户端主页面](docs/screenshots/client-main.png) | ![客户端连接详情](docs/screenshots/client-details.png) | ![客户端首选项](docs/screenshots/client-settings.png) |

主页面只保留圆环状态与三行关键信息（节点 / 隧道流量 / 连通检测）；
分配地址、网卡、网关、路由与三项防护状态收进「连接详情」；
登录、断线保护、修复网络、退出登录集中在首选项。

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
                     │ 30+ 张表   │  │ agent/dist       │
                     └────────────┘  └──────────────────┘
                            ▲
                            │ Agent 出站：poll / ack / WS / shell
                     proxypilot-agent ──▶ sing-box / mtg
```

- 后端：`server/`（Go 1.24+，标准库 HTTP，180+ 路由）
- 前端：`web-antd/`（React 18 + Ant Design 6 + antd-mobile）
- 数据库：MySQL 8（`utf8mb4_0900_as_cs`，`server/migrations/`）
- Agent：`agent/`（Go，只出站；配置任务下发 + PTY 终端）
- 存储：本地对象桶 + Agent 分发目录（不再依赖 R2/S3）
- WebSSH：Go WebSocket 中继，不依赖 Durable Object
- 旧 Cloudflare Worker：已移除（V6 起转为 Go 后端）

## 技术栈

| 层 | 选型 |
|---|---|
| 前端 | React 18 + Ant Design 6 + antd-mobile，esbuild 构建并嵌入 Go 二进制 |
| 后端 | Go 1.24+（`net/http`，无 Web 框架；`database/sql` + go-sql-driver/mysql） |
| 数据库 | MySQL 8，30+ 张表，启动自动迁移 |
| 鉴权 | HS256 JWT、PBKDF2 口令、TOTP、X-API-Key、OIDC 单点登录 |
| 节点 Agent | Go（轮询 / WebSocket 唤醒 / PTY 终端 / 配置落盘） |
| 存储 | 本地目录：对象桶 + Agent 二进制分发 |

## 快速开始（单机直接安装，不使用容器）

ProxyPilot 只支持**直接安装在服务器上**：系统包管理器装 MySQL，主程序以二进制 + systemd 运行。

```bash
# 1) 构建二进制（开发机 / CI）
cd web-antd && node scripts/build.mjs && cd ..        # 前端产物嵌入 Go
cd server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w" -o ../dist/proxypilot-linux-amd64 ./cmd/proxypilot

# 2) 上传到服务器后一键安装（root）
sudo ./deploy/install.sh --binary ./proxypilot-linux-amd64 --port 80

# 3) 打开后台（脚本结束会打印地址与初始口令）
# http://<服务器IP>/admin     账号 admin
```

安装脚本会自动完成：装 MySQL → 建库建用户 → 生成 `.env` 与密钥 → 安装 systemd 单元 → 健康检查。

- 升级：`sudo ./deploy/upgrade.sh --binary ./proxypilot-linux-amd64`（失败自动回滚）
- 卸载：`sudo ./deploy/uninstall.sh`（`--purge` 连数据库一起删）
- 详细说明见 [deploy/README.md](deploy/README.md)

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
│   ├── migrations/             # MySQL DDL（增量迁移，启动自动执行）
│   ├── testdata/               # Clash / sing-box / MTProto 黄金用例
│   ├── internal/api/assets_web/# 内嵌前端快照（免构建兜底）
│   └── .env.example / Makefile
├── web-antd/                   # 前端源码：React 18 + Ant Design 6
│   ├── src/                    # 管理后台 + 用户中心 + 图表/样式
│   └── dist/                   # 构建产物（会同步进 server/internal/api/assets_web/）
├── deploy/                     # 单机安装 / 升级 / 卸载脚本与站点加固
├── agent/                      # Go Agent + install.sh + build.sh + dist/
├── tunnel/                     # 自研 HTTP/3 隧道节点（36.0 由 masque/ 更名）
├── vpnclient/                  # VPN 客户端（Wails v3 桌面端 + CLI，含组网模式）
├── .github/                    # CI：Release 构建与安全扫描
├── rule/                       # Clash / Mihomo 模板与规则
├── docs/                       # 架构与迁移文档
```

### 前端说明（Ant Design 为主）

- 源码唯一入口：`web-antd/src`，构建：`cd web-antd && node scripts/build.mjs`（esbuild）。
- 构建产物会**自动同步**到 `server/internal/api/assets_web/`，被 `go:embed` 打进二进制，部署只需一个可执行文件。
- 调试期也可设置 `PP_WEB_DIST=.../web-antd/dist` 让 Go 直接读磁盘产物，改前端无需重新编译后端。

## 旧版代码

- `workers/`、`wrangler.toml` 与依赖 Worker 的测试脚本已从本分支删除。
- 历史迁移方案、D1 导入工具仍保留在 `docs/migration/` 与 `server/tools/`，仅用于迁移旧数据。
- `PP_LEGACY_BASE` 配置项保留，作为迁移期可选兜底；新部署无需配置。
- 当前版本：V35.3（Go 1.24+ / MySQL 8 / React 18 + Ant Design 6 / Go Agent 2.1.1 / Wails v3 桌面客户端）。

## 功能总览

### 公共站点

| 入口 | 说明 |
| ---- | ---- |
| `/`、`/me` | **用户中心**：邮箱验证码 + Cloudflare Turnstile 登录，UUID 由登录邮箱决定（不再手动输入）。PC 端为单页（订阅卡 + 订阅生成 / 配置管理 / 访问统计 / 外部订阅源）；移动端为底部 4 个页签（我的订阅 / 配置管理 / 访问统计 / 我的），账号信息与退出登录收在「我的」 |
| `/manage` | 老书签兼容：302 → `/` |
| `/login` | 三种登录方式：账号密码 / 邮箱验证码 / OIDC 单点登录；支持 TOTP 双重验证与备份码 |
| `/sub`、`/clash-config` 等 | 面向客户端的订阅输出（UUID 鉴权 + 可选人机验证） |

### 管理后台（左侧菜单）

侧边栏按分组折叠，顺序与下面一致：

| 分组 | 页面 | 内容 |
| ---- | ---- | ---- |
| — | **系统概览** `#dash` | 在线率与关键指标、访问趋势、热门 UUID、最新订阅 |
| **用户与业务** | **用户管理** `#uuids` | 按 UUID 管理节点配置、订阅源、外部订阅合并、分组授权；**一个邮箱只能绑定一个 UUID**（重复返回 409） |
| | **用户流量** `#userst` | 全部用户 / 按节点 / **流量流向** / 在线用户四个页签；上行下行、活跃判定（近 2 分钟有流量）、Top 榜、G6 流向图 |
| | **订阅模板** `#tpls` | Clash / Mihomo 订阅模板（含默认模板），可直接浏览编辑 `rule/` 下的模板与地区配置 |
| | **协议模板** `#tplhub` | sing-box / MASQUE / MTProto 的协议模板集中管理 |
| **节点与网络** | **服务器** `#servers` | 服务器总览（在线/离线/服务异常、实时速率、资源均值）、列表与批量操作、Agent 接入与更新、SSH 安全防护、实时终端 |
| | **节点绑定** `#bindings` | 服务器 ↔ sing-box / mihomo / MTProto 三类节点的绑定与下发 |
| | **CDN 优选** `#cdnpref` | Cloudflare 优选 IP / 回源配置 |
| | **WARP** `#masque` | Cloudflare WARP 设备注册、按 UUID 授权、MASQUE 配置下载 |
| **运维与安全** | **本机服务器** `#local` | 控制服务器自身的 CPU/内存/磁盘、SSH 安全防护、cloudflared / WARP 更新、端口开放关闭、进程监控、文件上传、远程终端 |
| | **日志管理** `#logs` | 节点下发与命令执行日志、结果与耗时 |
| | **附件管理** `#attachments` | 对象存储附件上传、预览与清理 |
| | **备份管理** `#backup` | 数据库与配置备份、导出与恢复 |
| | **证书管理** `#certs` | 本站 CA（ECDSA P-256，10 年）与功能证书：签发/续签、下发到组网中心与全部节点、下载证书/链/私钥 |
| **安全中心** `#sec` | 本人改密、MFA、邮箱绑定（所有账号可用） |
| | **用户管理** `#sysusers` | 管理员账号增删、邮箱与双因素状态 —— **仅超管可见** |
| | **系统设置** `#syssettings` | 站点地址、登录方式、Turnstile、SMTP、OIDC、Agent 与分发基址 —— **仅超管可见** |

### 主要特性

- **订阅生成**：本地配置 + 外部订阅源合并，支持 Clash / Mihomo 与原始链接
- **用户中心（PC / 移动端）**：PC 单页、移动端 4 页签；订阅生成（原始 / Clash / 批量 + 模板 + 本机二维码）、配置管理（搜索 / 8 种协议 / 外部订阅源增删启停）、访问统计、账号信息与换绑邮箱
- **监控多地址**：公网 IPv4 / IPv6、内网 IP、WARP 组网 IPv4 / IPv6 分别落库（`last_ip4` `last_ip6` `last_ip_private` `last_mesh_ip4` `last_mesh_ip6`），页面分行展示
- **协议编排**：一份模板一个配置文件（sb.json），只由 sing-box 承载；Xray 已下线（V15.6），MASQUE 用 sing-box 的 `masque-server` endpoint
- **MTProto（Telegram）**：`mtg-multi` 单进程多 secret，按 UUID 授权、撤销只断该用户；配置独立成表，不写入 `configs`，也就不进任何订阅；门户可看二维码或发到邮箱
- **版本化下发**：内容未变不升版本；变更自动 +1；Agent 回执 applied_version
- **UUID 一致性巡检**：Cron 每 30 分钟比对「平台授权 ↔ 节点配置 ↔ 订阅配置」，发现漂移自动修复；也支持事件触发与手动检查
- **删除 UUID 级联**：从授权、节点用户、订阅配置、节点配置中一并清理
- **监控采集**：hostname / OS / 内核 / 负载 / 内存 / 磁盘 / CPU（**已排除云主机 steal**）/ 网络 / 磁盘 IO / 连接状态 / 进程 Top / 服务状态 / cloudflared / WARP / IPv4 + IPv6 + 组网地址
- **Agent 自动更新**：与本地面板 `/agent-dist/manifest.json` 比对，后台一键推送升级（支持批量）
- **安全**：JWT、可选 Turnstile、TOTP、API 密钥、占位 UUID 拦截、SSH 安全防护（仅组网网段）、站点端口白名单

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

## VPN 与组网协议栈

> 术语：面板里的「组网」早期按 MASQUE 命名（目录 `masque/`、JWT `aud=masque-node`），
> 但实现是**自研的 HTTP/3 可靠流隧道**，不实现 RFC 9484 CONNECT-IP 与 RFC 9297 HTTP Datagram。
> 下文统一称「隧道」，并在末尾说明与标准 MASQUE 的差异。

### 1. 分层封装总览

一次上网请求在客户端与节点之间会叠成 6 层，任何一层出问题都会表现为"连不上/很慢"：

```
      客户端                                          节点
┌──────────────────┐                        ┌──────────────────┐
│ 应用数据          │                        │                  │
│   ↓ socket       │                        │ 内核转发          │
│ IP 报文 (L3)      │                        │   ↓              │
│   ↓ Wintun TUN   │                        │ TUN ppvpn0       │
│ 隧道帧 [2B][IP]   │                        │   ↑              │
│   ↓               │   HTTP/3 双向流        │ 隧道帧 [2B][IP]   │
│ HTTP/3 DATA 帧    │ ═══════════════════▶  │   ↑              │
│   ↓               │                        │                  │
│ QUIC 1-RTT 包     │   UDP/443 + QUIC       │                  │
│   ↓               │ ◀═══════════════════  │                  │
│ UDP + IP + 以太网 │                        │                  │
└──────────────────┘                        └──────────────────┘
```

| 层次 | 协议/格式 | 由谁负责 |
|---|---|---|
| 应用层 | 任意 TCP/UDP 应用 | 用户程序 |
| 网络层 | IPv4 报文（IPv6 暂不支持） | 系统内核 → TUN |
| 隧道层 | `[2 字节大端长度][IP 报文]` | 客户端 `streamPacketConn` / 节点 `readStream` |
| 应用协议 | HTTP/3：`GET /` 鉴权、`POST /tunnel` 数据面 | `http3.Server` / `http3.Transport` |
| 传输层 | QUIC v1（TLS 1.3，ALPN `h3`） | `quic-go` |
| 承载层 | UDP 443 + IP | 操作系统 |

### 2. 客户端数据面内部路径

```
[上行] 应用 → 系统路由(0.0.0.0/1) → Wintun 适配器
        → pumpUp() 读到一个 IP 报文
        → streamPacketConn.SendPacket()
        → 写 io.Pipe → http3.Transport 的请求体
        → QUIC STREAM(DATA) 帧 → UDP 443 → 节点

[下行] 节点 QUIC STREAM → http3 响应体 → resp.Body
        → streamPacketConn.RecvPacket()
        → pumpDown() 写入 Wintun
        → 系统按 IP 头投递给应用
```

关键代码位置：

| 环节 | 文件 |
|---|---|
| 建流与 HTTP/3 客户端 | `vpnclient/stream_dial.go`（`POST /tunnel`） |
| 分帧收发 | `vpnclient/stream_tunnel.go`（`SendPacket`/`RecvPacket`） |
| 网卡与路由 | `vpnclient/route_windows.go` |
| 节点侧收流 | `masque/node/stream.go`（`readStream`） |
| 节点侧回写 | `masque/node/server.go`（`tunLoop`） |

### 3. 隧道帧格式（字节级）

每个 IP 报文前加 **2 字节大端长度**，无填充、无校验和（QUIC 已有 AEAD）：

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+----------------+------------------------------------------------...
|   Length (2B)  |              IP Packet (Length 字节)            ...
+----------------+------------------------------------------------...
```

以 `ping 1.1.1.1`（ICMP 32 字节负载）为例，帧内容：

```
00 3C                          ← Length = 60（IP 报文总长）
45 00 00 3C 1A 2B 00 00        ← IPv4 头：版本4/IHL5、TOS0、总长60、ID、Flags
40 01 F2 9E                    ← TTL 64、协议 1(ICMP)、头校验和
0A 58 01 0C 01 01 01 01        ← 源 10.88.1.12 → 目的 1.1.1.1
08 00 4D 5A 00 01 00 01        ← ICMP Echo Request：类型8/代码0/校验和/标识/序号
... 32 字节 payload ...        ← 实际 ping 数据
```

约束：

| 项 | 值 | 说明 |
|---|---|---|
| 长度字段 | 无符号 16 位大端 | 只表示 IP 报文长度，不含这 2 字节本身 |
| 单帧上限 | 4096 字节 | 防异常长度（`maxFrame`） |
| 帧边界 | 严格按长度切分 | 一条流里连续排列，无分隔符 |
| 校验 | 无 | 依赖 QUIC 的 AEAD 与 TCP/ICMP 自身校验 |

> 为什么不直接在流里裸写 IP 报文？因为 TCP/QUIC 是**字节流**，没有消息边界，
> 必须自带长度前缀才能切包。若改用 HTTP Datagram（RFC 9297）可省掉这 2 字节，但会失去重传保证。

### 4. HTTP/3 报文内容

#### 4.1 鉴权请求（GET /）

客户端在建立数据面前先做一次普通 HTTP/3 请求，用于验签与分配地址：

```
:method: GET
:scheme: https
:authority: taipei-node.yzfn.top:443      （SNI 与 :authority 一致）
:path: /
authorization: Bearer eyJhbGciOiJFZERTQSIsInR5cCI6IkpXVCJ9....
```

节点校验通过后返回：

```
:status: 200
x-vpn-auth: Success
x-vpn-assigned-ipv4: 10.88.1.12
x-vpn-max-datagram: 1360                  （历史字段，DATAGRAM 时代遗留）
cache-control: no-store
```

失败分支：

| 状态码 | 响应体 | 原因 |
|---|---|---|
| 401 | `unauthorized` | 缺 Bearer / 签名无效 / 过期 |
| 403 | `wrong node` | JWT 里的 `node` 与节点 ID 不符 |
| 403 | `ip not in client cidr` | JWT 里的 `ip4` 不在节点客户端子网 |
| 409 | `ip already in use` | 同一地址已有活跃会话 |

#### 4.2 数据面（POST /tunnel）

```
:method: POST
:scheme: https
:authority: taipei-node.yzfn.top:443
:path: /tunnel
authorization: Bearer eyJhbGciOiJFZERTQSIsInR5cCI6IkpXVCJ9....
content-type: application/octet-stream
```

- **请求体**：上行，持续写入 `[2B][IP]` 帧，一直到断开
- **响应体**：下行，节点持续写入同格式帧
- 响应头（在鉴权通过后立即返回，随后开始双向流）：

```
:status: 200
x-vpn-auth: Success
x-vpn-assigned-ipv4: 10.88.1.12
cache-control: no-store
content-type: application/octet-stream
```

- 这是**长连接**：一条流同时承载上下行，直到客户端断开或空闲超时
- 没有 `content-length`：长度未知，靠流结束（FIN）标识终止

#### 4.3 为什么用 POST 而不是 CONNECT

标准 MASQUE 用 HTTP/3 扩展 `CONNECT` + `:protocol=connect-ip`：
报文走 HTTP Datagram，地址路由用 Capsule 协商。
本项目用普通 `POST /tunnel`：报文作为**可靠流**传输，地址与路由由面板 JSON 下发。
好处是能复用现有 HTTP/3 基础设施与面板授权体系，代价是失去与标准 MASQUE 客户端的互通性。

### 5. QUIC 传输层

| 项 | 值 |
|---|---|
| 版本 | QUIC v1（RFC 9000） |
| 承载 | UDP 443（可配 `listen_port`） |
| TLS | 1.3，ALPN = `h3` |
| 握手 | 1-RTT（首连），支持会话票据复用 |
| keepalive | 客户端 10s / 节点 15s |
| 空闲超时 | 120s（两端） |
| 拥塞控制 | Cubic（quic-go 未内置 BBR） |
| 流控窗口 | 初值 512 KB，单流上限 6 MB，连接上限 15 MB（quic-go 默认） |

**1-RTT 短包结构**（数据面每个报文都套这一层）：

```
+--------+----------------+----------+-------------------+
| 1B 头  | DCID (8B)      | PN(1-2B) | AEAD 密文 + 16B 标签 |
+--------+----------------+----------+-------------------+
   ↑ 短包头     ↑ 连接 ID        ↑ 包号
```

**流控与 RTT 的关系**（决定单流吞吐上限）：

```
吞吐上限 ≈ 接收窗口 / RTT
```

实测（2026-11）：

| 节点 | RTT | 当前窗口下理论单流上限 |
|---|---|---|
| 台北 `82.152.91.4` | 376 ms | 512KB / 0.376s ≈ **10.9 Mbps** |
| 香港2 `38.147.187.60` | 119 ms | 512KB / 0.119s ≈ **34.4 Mbps** |

→ 跨高 RTT 链路时，**接收窗口是首要瓶颈**，可按 BDP 放大：

```go
quicConf := &quic.Config{
    InitialStreamReceiveWindow:     4 << 20,  // 4 MB
    MaxStreamReceiveWindow:         16 << 20,
    InitialConnectionReceiveWindow: 8 << 20,
    MaxConnectionReceiveWindow:     64 << 20,
    KeepAlivePeriod:                10 * time.Second,
    MaxIdleTimeout:                 120 * time.Second,
}
```

### 6. TLS 与证书模式

| 模式 | 节点使用什么证书 | 客户端校验策略 |
|---|---|---|
| `selfsigned` | 节点本地 `openssl` 自签 | **跳过校验**（无信任锚，仅兜底） |
| `internal_ca` | 平台 CA 现场签发（90 天） | 自动拉 `/api/vpn/ca.crt` 后**严格校验** |
| `acme` | 「证书管理」里已签发的通配证书（如 `*.yzfn.top`） | 系统根证书**严格校验** |

内部 CA 的信任链：

```
面板启动生成 CA（ECDSA P-256，10 年，私钥 AES-GCM 存库）
        ↓ 节点部署时签发 CN=<节点域名> 的 90 天证书
节点 TLS 1.3 握手出示该证书
        ↓
客户端 GET /api/vpn/ca.crt 拉取 CA 并缓存到 %APPDATA%\ProxyPilot\ca.pem
        ↓ 作为 RootCAs 严格校验
```

ACME 模式下部署会先校验通配证书是否覆盖节点域名，不覆盖直接报错，避免"部署了但客户端握手失败"。

### 7. JWT 令牌结构

面板签发（Ed25519），节点用公钥验签：

```json
{
  "alg": "EdDSA",
  "typ": "JWT"
}
{
  "iss": "proxypilot",
  "aud": "masque-node",
  "sub": "device:3",
  "uuid": "528a6d92-8112-4f6a-90c9-...",
  "node": 3,
  "grant": 42,
  "ip4": "10.88.1.12",
  "net": "10.88.1.0/24",
  "iat": 1794219956,
  "exp": 1794221156,
  "jti": "c1f3..."
}
```

| 声明 | 用途 |
|---|---|
| `node` | 与节点 ID 比对，防止拿 A 节点令牌连 B 节点 |
| `ip4` | 分配地址，同时用于会话源地址校验（防地址伪造） |
| `jti` | 会话唯一标识，用于并发去重与审计 |
| `exp` | 默认 20 分钟，过期需重新换票 |

### 8. 一次 `ping 1.1.1.1` 的完整字节流

以 MTU 1280、无丢包为例：

| 阶段 | 内容 | 字节数 |
|---|---|---|
| ① 应用生成 ICMP | 20B IP 头 + 8B ICMP 头 + 32B 负载 | 60 |
| ② 隧道分帧 | 追加 2B 长度 | 62 |
| ③ HTTP/3 DATA 帧 | 帧类型 + varint 长度 | +3 ≈ 65 |
| ④ QUIC STREAM 帧 | 帧类型 + 流 ID + 偏移 + 长度 | +6 ≈ 71 |
| ⑤ QUIC 1-RTT 包 | 短包头 + DCID + PN + AEAD 标签(16B) | +27 ≈ 98 |
| ⑥ UDP 头 | 源/目的端口 + 长度 + 校验和 | +8 = 106 |
| ⑦ IP 头 | 20B（IPv4） | **+20 = 126** |

**封装开销约 110%**（60 → 126 字节）。这解释了两件事：

1. 小包场景（游戏、DNS、TCP ACK）**隧道内开销远大于有效载荷**，pps 是瓶颈而不是带宽
2. 满 MTU 报文（1280B）时开销仅约 8%，所以大流量下载会更"划算"

节点侧转发路径：

```
readStream 收到 62B 帧 → StripPadding 校验长度与源地址
        ↓ 写入 TUN ppvpn0
内核路由 → iptables MASQUERADE（10.88.1.0/24 → eth0）
        ↓
1.1.1.1 回 ICMP Echo Reply
        ↓ 按 conntrack 回到 TUN
tunLoop 读到 → 查会话表 → sess.send() 写回 QUIC 流
```

### 9. 组网（Mesh）

用于**服务器之间**互通（运维面、内部服务调用），与终端用户的 VPN 是两套独立配置。

| 项 | 值 |
|---|---|
| 网段 | `10.99.0.0/16`（`vpn_settings.mesh_cidr4`） |
| 中心 | `vpncenter.yzfn.top:443`，网卡 `ppmesh0` = `10.99.0.1/16` |
| 成员 | 每台服务器一个固定地址（`10.99.0.x`），写入成员表 |
| 节点模型 | `disable_nat=true`（只配 TUN 地址，不做 NAT） |
| 互访 | `allow_client_to_client=true`（不做客户端隔离） |
| 心跳 | `POST /api/portal/vpn/mesh/heartbeat` → `rtt_ms` / `transport` / `state` / `endpoint` / `last_error` |
| 部署 | 面板下发 systemd 单元 `pp-mesh-client` |

启动命令（与 VPN 客户端同一二进制，走 CLI 组网模式）：

```bash
pp-mesh-client -mesh-token "$(cat /etc/proxypilot/mesh.token)" \
  -mesh-ip 10.99.0.11 -mesh-cidr 10.99.0.0/16 -mesh-tun ppmesh0 \
  -mesh-endpoint vpncenter.yzfn.top:443 -mesh-sni vpncenter.yzfn.top
```

组网与 VPN 的帧格式、HTTP/3 语义**完全相同**，差别只在节点配置：

| 维度 | VPN | 组网 |
|---|---|---|
| 服务对象 | 终端用户 | 服务器之间 |
| 网段 | 每节点独立 `10.88.x.0/24` | 统一 `10.99.0.0/16` |
| NAT | 开（`MASQUERADE`） | 关 |
| 客户端隔离 | 开（C2C 丢弃） | 关（允许互访） |
| 地址下发 | 面板 `/api/portal/vpn/connect` | 面板成员表 + `mesh.token` |

#### 组网心跳报文

```
POST /api/portal/vpn/mesh/heartbeat
content-type: application/json

{
  "rtt_ms": 157,
  "transport": "masque",
  "state": "up",
  "endpoint": "vpncenter.yzfn.top:443",
  "last_error": ""
}
```

面板据此在「组网」页展示 RTT 与状态，并可用于告警。

### 10. 抓包与调试

**Wireshark 过滤**

```
udp.port == 443                      # 只看隧道承载
quic                                 # QUIC 握手与连接
http3                                # HTTP/3 帧（需开启 TLS 解密）
http3.headers                       # 只看头部：可见 GET / 与 POST /tunnel
tls.handshake.extensions_alpn       # 确认 ALPN=h3
```

**节点侧**

```bash
# 抓 QUIC 流量（QUIC 加密，只能看包长与时序）
tcpdump -ni eth0 'udp port 443' -c 200 -vv

# 看 TUN 上的明文 IP 报文（能直接看到 ICMP/TCP 头）
tcpdump -ni ppvpn0 -c 50 -vv

# 会话日志
journalctl -u proxypilot-agent -f | grep -E '流隧道|会话'
```

**客户端日志**

`%LOCALAPPDATA%\ProxyPilot\logs\client.log`，关键字：

```
数据面：从网卡读到首个报文
数据面：首个报文已写入隧道流
隧道流量：网卡→隧道 N 包，隧道→网卡 M 包
隧道连通性：正常（xx ms）
DNS 守卫：已强制所有域名走 1.1.1.1, 8.8.8.8
```

### 11. 性能与调优要点

| 参数 | 当前 | 建议（高 RTT 链路） | 说明 |
|---|---|---|---|
| 隧道 MTU | **1380**（已实施） | — | 1280 时包量多约 8%；1420 在内层封装后接近 1500，遇 PPPoE 易黑洞 |
| 流控窗口 | **4MB / 16MB / 64MB**（已实施） | — | 按 BDP = 带宽 × RTT 计算，V35.3/2.2.0/2.8 起生效 |
| 拥塞控制 | Cubic | 节点出口开 BBR | 隧道本身仍是 Cubic |
| 节点内核 | 默认 | `fq` + `bbr` + 16MB 缓冲 | 影响节点出口的真实 TCP 连接 |
| 节点选择 | 取列表第一个 | 按实测 RTT | 台北 376ms vs 香港 119ms，差 3.2 倍 |

> 详细优化方案见 [docs/DNS-LEAK.md](docs/DNS-LEAK.md)（DNS 泄露）与本节「性能与调优要点」。

### 12. 版本对应（2026-11）

| 组件 | 版本 | 说明 |
|---|---|---|
| 面板 | **V35.4** | 签发令牌、分配 IP、会话治理、证书管理 |
| Agent | **2.2.0** | 内嵌 HTTP/3 隧道节点（4MB 流控窗口） |
| 客户端（桌面） | **2.13** | DNS/IPv6 泄露防护、Kill Switch、DPAPI 令牌加密 |
| 隧道协议 | 可靠流 v1 | `[2B len][IP]` over HTTP/3 stream |

---

## 权限模型

| 身份 | 入口 | 可用范围 |
| ---- | ---- | -------- |
| 超级管理员（默认 `admin`，可用 `PP_SUPER_ADMIN` 覆盖） | `/admin` | 全部功能（含「用户管理」「系统设置」） |
| 其他后台管理员 | `/admin` | 除「用户管理」「系统设置」外的全部页面与接口 |
| 用户 | `/`（= `/me`） | 邮箱验证码 + 人机验证登录后进入用户中心（PC 单页 / 移动端 4 页签），只能管理 **自己邮箱绑定的那个 UUID**（一个邮箱仅允许绑定一个 UUID）；`/manage/*` 数据接口校验令牌与 UUID 归属 |

- 非超管登录时「用户管理」「系统设置」入口不渲染，手输 `#sysusers` / `#syssettings` 会被前端拦回；
- 服务端同样强制：`/api/admin/users`、`/api/logs/summary`、`/api/logs/cleanup`、`/api/apikeys` 对非超管一律 403；
- 用户端令牌（JWT 带 `role=user`）只能访问 `/api/portal/*`，调用任何后台接口一律 403；
- 节点侧 Agent 令牌只能访问 `/api/agent/*` 与终端隧道，不能调用管理接口；
- 用户能看到哪些 UUID 由「订阅分组」里维护的**邮箱**决定（大小写不敏感），管理员改邮箱即改归属。

---

## 生产部署（单机直接安装）

ProxyPilot **只支持直接安装在服务器上**，不提供容器方案。完整说明见
[`deploy/README.md`](deploy/README.md)。

```bash
# 1) 构建（开发机 / CI）
cd web-antd && node scripts/build.mjs && cd ..
cd server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w" -o ../dist/proxypilot-linux-amd64 ./cmd/proxypilot

# 2) 上传 proxypilot-linux-amd64 与 deploy/ 到服务器，然后（root）
sudo ./deploy/install.sh --binary ./proxypilot-linux-amd64 --port 80 --agent-dist ./agent-dist

# 3) 升级（自动备份 / 健康检查 / 失败回滚）
sudo ./deploy/upgrade.sh --binary ./proxypilot-linux-amd64

# 4) 卸载（--purge 连数据库一起删）
sudo ./deploy/uninstall.sh
```

安装脚本做的事：

| 步骤 | 说明 |
|---|---|
| 系统识别 | Debian 12/13、Ubuntu 22.04+、Rocky/Alma 9 |
| MySQL | 系统包管理器安装并建库建用户（已有则复用，或 `--dsn` 指向外部） |
| 目录 | `/opt/proxypilot`（data/bucket、agent-dist、uploads），专属系统用户 |
| 配置 | 生成 `.env`，自动生成 JWT 密钥与管理员口令 |
| 托管 | 写入 `proxypilot.service`（systemd，开机自启，崩溃重启） |
| 验收 | 轮询 `/healthz` 直到就绪，打印后台地址与初始口令 |

部署后建议：

1. **反代**：Nginx/Caddy 终止 TLS，转发 `Host` / `X-Forwarded-Proto`，允许 WebSocket Upgrade。
2. **验收**：`server/tools/smoke.sh https://<域名>`；后台登录、订阅、Agent、WebSSH 各跑一遍。
3. **加固**：用 `deploy/web-guard/` 限制 80/443 仅允许组网与 Cloudflare 回源。

### Cloudflare Tunnel 部署（80 端口仅回环）

生产面板通过 Cloudflare Tunnel 对外，源站只监听本机：

1. **Tunnel**：`cloudflared` 与面板同机，Public Hostname 的 Service 指向 `http://127.0.0.1:80`
2. **禁止公网直连 80**：安装仓库内 `deploy/pp-port80-guard.sh` 与 `deploy/pp-port80-guard.service`
   （`iptables`/`ip6tables` 先放行 `lo`，再 DROP 其余 80/tcp，开机自启）
3. **验收**：
   - 公网 `curl http://<服务器IP>/` → 超时/拒绝
   - 本机 `curl http://127.0.0.1/healthz` → 200
   - 隧道域名 → 200
4. 如需临时直连排查，先 `systemctl stop pp-port80-guard && iptables -D INPUT -p tcp --dport 80 -j DROP`

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
| 4 | 协议模板 `#tplhub` → 节点绑定 `#bindings` | 先建协议模板（「填入示例」生成随机密钥与端口），再到节点绑定页绑定服务器 / 域名 / 授权 UUID |
| 5 | 节点绑定 | 点「下发」，Agent 经常驻长连接即时收到任务，写入配置并重启 |
| 6 | 用户管理 `#uuids` | 新建 UUID，把「邮箱」填成用户邮箱；一个邮箱只能绑一个 UUID |
| 7 | 服务器 `#servers` | 确认节点在线、CPU / 内存 / 磁盘有数、公网/内网/组网 IP 显示正常、无「服务异常」 |

> 「全局域名」用于生成订阅里的节点域名；协议模板里的 `{DOMAIN}` 会自动替换。

### 2. 日常运维

- **服务器**：顶部为在线 / 离线 / 服务异常、实时上下行与集群总量、资源均值；下方列表支持搜索、按状态筛选、批量探测 / 更新 Agent / 开关 SSH 防护 / 重启；点「详情」看内核、服务状态、Top 进程、任务队列与性能折线。
- **一键更新**：「服务器」页批量「更新 Agent」，与本地面板 `/agent-dist/manifest.json` 比对后自升级。
- **日志管理**：每次下发 / exec 都有 rc、耗时与输出，排障先看这里。
- **巡检与通知**：Cron 每 30 分钟比对「平台授权 ↔ 节点配置 ↔ 订阅配置」，漂移自动修复；「通知设置」可配告警邮箱。
- **API 密钥**：通过 `/api/apikeys` 按权限位创建，给脚本 / CI 用。

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
- 原理：浏览器 `wss://…/api/shell/browser/:id` ↔ **Go 进程内 WebSocket 中继** ↔ Agent 主动出站的 `wss://…/api/shell/agent/:id` ↔ 服务器上的 PTY（`/bin/bash -il`，没有 bash 时回落 `/bin/sh -i`）；
- 安全：浏览器侧校验后台 JWT（用户端令牌不可用）、服务器必须开 `allow_exec`；Agent 侧校验接入令牌；会话号一次性、120 秒未接入自动作废；同一服务器同时只保留一个终端会话。

常见状态：

| 现象 | 原因 / 处理 |
| ---- | ----------- |
| 终端连不上且提示 Agent 未接入 | 确认节点 Agent 在线（服务器页心跳「刚刚」）、版本 ≥ 1.10.8 |
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

- 版本：**1.10.8**，静态编译（`CGO_ENABLED=0`），支持 `linux/amd64`、`linux/arm64`
- 常驻 systemd：`proxypilot-agent.service`（`Restart=always`）
- 安装脚本会校验密钥（`/api/agent/keycheck`）、下载二进制并强制 sha256 校验

采集项（默认 2 秒一次经常驻 WS 推送，60 秒一次 HTTP 轮询兜底）：

```
hostname / os / kernel / platform / arch / cores / uptime / load
mem_used_mb / mem_total_mb / disk_percent
cpu_ticks / cpu_idle_ticks / cpu_steal_ticks   # 用 busy/(busy+idle) 计算真实占用
net（累计收发、包、错包、网卡数）
disk_io（累计读写、次数）
conn（TCP/UDP 状态计数）
proc（进程总数 + Top RSS）
services（systemd 服务状态；批量查询 + 15 秒缓存，含 warp-svc / proxypilot-agent）
versions（sing-box / mihomo / mtg / cloudflared / warp-cli / 自身版本，5 分钟缓存）
ssh（sshd / ssh 的 enabled / active 状态）
ip（公网 IPv4）/ ipv6（本机全局 IPv6）/ private_ip / mesh_ip4 / mesh_ip6（WARP 组网）
```

- 不再采集 CPU 温度（云主机普遍读不到）；
- 服务状态四态：`active` / `inactive` / `failed` / `not-installed`；服务器详情里同时显示已装组件的版本号（老 Agent 不上报则为空）；
- 一键更新：后台比对面板 /agent-dist/manifest.json，推送新二进制并重启 Agent（支持单台与批量）。
- 通信：常驻 WebSocket 长连接（内网明文优先，仅走组网），HTTP 轮询 **60 秒**一次兜底；连续 3 个周期没有心跳即判离线，页面动态判定，cron 负责落库与通知。
- 实时终端（1.3.0 起）：面板经常驻 WS 直接推送 shell 会话，Agent 收到即连 `wss://…/api/shell/agent/:id`，在 PTY 里跑 `/bin/bash -il`（没有 bash 时用 `/bin/sh -i`），浏览器输入输出由 Go 中继双向转发。

常用命令：

```bash
proxypilot-agent doctor -c /etc/proxypilot-agent.conf   # 自检
systemctl status proxypilot-agent
journalctl -u proxypilot-agent -n 50
curl -fsSL http://<面板内网地址>:80/api/agent/install.sh | sh -s -- --uninstall
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

要求 **Go 1.24+**（`server/go.mod` 指定 toolchain go1.27.3）、**Node.js ≥ 20**、**MySQL 8.0.13+**。

### 后端（Go）

```bash
cd server
go test ./...
go vet ./...
go build ./cmd/proxypilot
```

> Windows 上 `agent/internal/runner` 的部分单测依赖 Linux 路径白名单，会在本机失败，属预期（CI/部署机验证）。

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
node scripts/build.mjs   # esbuild 构建并自动同步 server/internal/api/assets_web/
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
| 实时终端提示「该服务器未开启允许执行命令」 | 该服务器 allow_exec 未开启；本机控制服务器固定关闭，请对节点使用，或直接在机器上执行 |
| 终端一直停在「等待 Agent 接入」 | Agent < 1.3.0 或离线；升级 Agent（一键更新）并确认心跳正常 |
| CPU 一直显示 100%，但机器并不卡 | 云主机 CPU 被宿主机抢占（steal）。V32.0 起已排除 steal，把面板升级到 V32.0、Agent 升到 1.10.8+ 即可正确显示 |
| 节点报「服务异常：sshd」 | 常见于 sshd 未被 systemd 托管（缺 /run/sshd 或被外部进程占用 22 端口）。执行 mkdir -p /run/sshd、杀掉非托管 sshd、systemctl reset-failed ssh、systemctl start ssh 即可 |
| 端口规则改完把自己关在外面 | PP_PORTS 链挂在 INPUT 首条且 DROP 优先；务必保留 22/80 放行，或从组网内网进入面板修正 |
| 换域名后登录态失效 | Origin 变化导致浏览器里既存 token 不再匹配，重新登录即可 |
| 后台看不到「用户管理」「系统设置」 | 当前登录账号不是超管（账号名需等于 PP_SUPER_ADMIN，默认 admin） |
| 一键更新失败 | /agent-dist/manifest.json 与二进制是否同步、PP_AGENT_BINARY_BASE_URL 是否正确 |

## 适用场景

- 自建节点（sing-box，含标准 MASQUE endpoint）统一编排与下发
- 多台服务器、多 UUID 的订阅分发与访问统计
- 想要「面板 + Agent」但不想开放管理端口的环境

## 安全说明

- 用户端令牌（`role=user`）与后台令牌完全隔离，越权调用后台接口一律 403；
- 面板建议只监听回环或组网地址，配合 `deploy/web-guard/` 把 80/443 限制为「组网 + Cloudflare 回源」；
- Agent 只主动出站（长连接 + 60s 保底轮询），节点侧无需开放入站端口；
- 订阅链接含 UUID，等同于密码，前端多处提示「请勿公开分享」；
- 后台可关掉密码登录只留 OIDC / 邮箱验证码；Turnstile 未配置时自动跳过；
- Agent 只做出站连接，服务器不开放管理端口；接入密钥可随时轮换，旧密钥立即失效；
- 公开示例配置里的密钥在保存时会被拦截，占位 UUID 同样拒绝入库。

## 许可证

MIT
