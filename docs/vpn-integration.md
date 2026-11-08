
## 版本号规范

| 组件 | 版本线 | 当前 |
|---|---|---|
| proxypilot 管理平台 | `V32.x` | `V32.5` |
| 节点 Agent | `2.0.x` | `2.0.0` |
| VPN 客户端/节点端 | `V1.x` | `pp-masque-node 1.0.1` |
# MASQUE-VPN 集成（实施中）

本文记录 ProxyPilot 接入自建 MASQUE-VPN 的第一阶段实现，以及后续待办。

## 1. 角色与边界

| 端 | 职责 |
|---|---|
| 服务端 | 节点/设备/用户资格/绑定授权、连接令牌、设置（WARP/自建管理网切换）、会话与审计 |
| 节点端（Agent + `pp-masque-node`） | 注册、心跳、证书、HTTP/3 接入、TUN/NAT、用户隔离 |
| 用户端 | 设备码登录、只显示已绑定节点、一键连接、仅上网 |

**硬边界**

- 节点之间不互通，服务端不转发节点间流量；
- 用户不能访问服务端/管理网，也不能访问节点内网；
- 用户只能经节点 NAT 上公网，默认客户端之间隔离。

## 2. 管理通道：WARP 优先，自建组网备用

`vpn_settings.management_transport`：

| 值 | 含义 |
|---|---|
| `warp`（默认） | 节点同步数据（注册/心跳/会话上报）走现有 Cloudflare WARP 组网 |
| `masque` | 走自建 MASQUE 管理网（星型，服务端为中心） |
| `auto` | WARP 可用则走 WARP，否则按 `masque_fallback` 回落到自建管理网 |

Agent 注册时上报 `warp: true/false`，服务端返回 `effective_transport`，节点据此选择同步链路。
后续可在后台直接切换，无需改节点配置。

## 3. 已实现（第一阶段）

### 数据库

`server/migrations/0032_vpn.sql`：

- `vpn_settings`：VPN 开关、管理通道、地址池、MTU、令牌 TTL；
- `vpn_nodes`：节点（server_id、接入地址、管理网 IP、客户端子网、公钥、状态、证书模式）；
- `vpn_devices` / `vpn_user_access` / `vpn_node_grants`：设备、用户资格、节点绑定；
- `vpn_sessions` / `vpn_node_links`：会话记账、管理链路状态；
- `vpn_certs` / `vpn_signing_keys` / `vpn_audit`：证书、签名密钥、审计。

### 服务端包 `server/internal/vpn`

- `vpn.go`：管理通道选择（warp/masque/auto + fallback）、设置校验；
- `token.go`：Ed25519 紧凑 JWT（签发/验签、kid、aud、过期、篡改检测）；
- `authz.go`：连接授权判定（用户资格 → 节点 → 绑定 → 设备 → 并发/配额），
  返回稳定错误码（`user_not_authorized` / `node_not_bound` / `grant_expired` / `device_limit`…）；
- `store.go`：MySQL 读写（设置、节点 CRUD、注册、绑定、用户资格、可见节点）；
- 单测：`token_test.go`、`authz_test.go`。

### HTTP API（已接路由）

管理端（`superOnly`）：

```
GET/PUT /api/admin/vpn/settings
GET/POST /api/admin/vpn/nodes
GET/PUT/DELETE /api/admin/vpn/nodes/{id}
POST /api/admin/vpn/nodes/{id}/action      # approve|enable|disable（自动分配 10.99.0.x / 10.88.N.0/24）
GET/POST /api/admin/vpn/nodes/{id}/grants  # 节点绑定用户
DELETE /api/admin/vpn/grants/{id}
GET/PUT /api/admin/vpn/users/{uuid}/access # 用户 VPN 资格
```

Agent（AgentKey）：

```
POST /api/agent/vpn/register   # 注册/更新节点，返回 effective_transport、mesh_ip4、TTL 等
POST /api/agent/vpn/heartbeat  # 链路状态、RTT、transport（warp|masque）
```

用户中心（portal JWT）：

```
GET /api/portal/vpn/entitlement  # 我的资格、设备数、配额
GET /api/portal/vpn/nodes        # 仅已绑定且资格有效的节点（不暴露管理网地址）
```

Agent 客户端：`agent/internal/client/vpn.go`（`VPNRegister` / `VPNHeartbeat` + 单测）。

## 4. 待办（下一阶段）

1. 设备码授权（OAuth Device Code）与设备管理 API；
2. `/api/portal/vpn/connect`：签发连接 JWT（Ed25519）、按设备固定 IP、写会话；
3. 管理后台页面：VPN 节点、绑定、用户资格、会话；
4. `pp-masque-node`：JWT 验签、IP 分配、TUN/NAT、隔离防火墙；
5. Agent 安装/配置/启停 + 证书签发（内部 CA/ACME）；
6. 服务端 `pp-mesh`（自建管理网备用）与切换开关；
7. 用户端（Windows Wails / Linux CLI）平台模式；
8. 会话/配额/吊销/审计闭环。

## 5. 验证

```bash
# server
cd server
go test ./internal/vpn/
go build ./...

# agent
cd agent
go test ./internal/client/
go build ./...
```
### 第二批（设备授权与连接）

- `POST /api/vpn/device/code`：设备码申请（OAuth Device Code，10 分钟有效）；
- `POST /api/vpn/device/token`：轮询换取门户登录令牌与设备 ID（未批准返回 428 `authorization_pending`）；
- `POST /api/portal/vpn/device/approve`：已登录用户在门户确认设备码；
- `GET /api/portal/vpn/devices`、`DELETE /api/portal/vpn/devices/{id}`：设备列表/吊销；
- `POST /api/portal/vpn/connect`：完整授权校验（资格/绑定/设备/并发/配额）→ 分配/复用设备 IP →
  签发 Ed25519 连接 JWT（10 分钟，含 node/grant/ip）→ 写会话 → 返回 profile；
- `POST /api/portal/vpn/disconnect`：结束会话；
- 签名私钥加密持久化（`vpn_signing_keys`，AES-256-GCM，密钥来自 AgentKeySecret/JWTSecret）；
- IP 分配：优先复用设备在该节点的历史 IP，否则在节点 `client_cidr4` 内从 `.10` 起找空闲；
- profile 只含 `0.0.0.0/0` 与本节点子网，**不含管理网/私网**。
### 节点二进制 `pp-masque-node`（新增模块 `masque/`）

- 独立模块 `github.com/yzfn/proxypilot/masque`，不污染 server/agent 的 go.mod；
- HTTP/3 监听 + Ed25519 JWT 验签（`iss`/`aud`/`exp`/`node`/`jti`）；
- 按 JWT 中的 `ip4` 绑定会话，重复 IP 拒绝（409）；
- 校验 DATAGRAM 源地址必须等于分配 IP，防伪造；
- TUN（Linux，`songgao/water`）+ 自动配置地址/MTU/`ip_forward`；
- NAT：只对客户端子网出公网做 MASQUERADE；
- **用户隔离**（`PPVPN` 链）：先 DROP 全部 RFC1918/保留地址，再允许出公网，最后默认拒绝；
  客户端 → 客户端在 TUN 读循环里直接丢弃；
- 退出时清理 iptables 链与 NAT 规则。

构建：

```bash
cd masque
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.Version=0.1.0" \
  -o dist/pp-masque-node ./cmd/pp-masque-node
```

配置示例：`masque/config.example.json`（需要把服务端 Ed25519 公钥填进 `public_key`）。
### 管理后台页面（第三批）

- 新增 `web-antd/src/pages/VpnPage.jsx`，菜单位置：**节点与网络 → VPN 节点**；
- 「VPN 设置」：启用开关、节点同步通道（WARP / 自建管理网 / 自动）、WARP 不可用回落、MTU、令牌 TTL；
- 「VPN 节点」表格：ID/名称/服务器/接入地址/管理网 IP/客户端子网/状态/心跳，
  操作：启用（审批并自动分配 10.99.0.x 与 10.88.N.0/24）、停用、授权、编辑、删除；
- 「授权」弹窗：按用户 UUID 绑定/解绑，支持有效天数、并发设备、流量配额；
- 「用户资格」弹窗：按 UUID 查询/开通，设置启用、有效期、最大设备数、总配额、备注；
- 对接接口：`/api/admin/vpn/settings`、`/nodes`、`/nodes/{id}/action`、`/nodes/{id}/grants`、`/users/{uuid}/access`。

> 本机没有安装 `web-antd/node_modules`，完整前端构建需在部署机执行 `npm install && npm run build`；
> 已用 esbuild 对 `VpnPage.jsx`、`nav.jsx`、`App.jsx` 做 JSX 语法校验（通过）。
### 节点部署（Agent 一键下发）

- 管理后台节点行新增「部署」按钮 → `POST /api/admin/vpn/nodes/{id}/deploy`；
- 服务端把一段固定脚本作为系统级 `exec` 任务入队（`system_action=vpn_apply`，不受 allow_exec 限制）；
- 脚本内容：下载 `pp-masque-node` → 写 `/etc/proxypilot/masque/config.json` →
  生成自签证书（若缺失）→ 写 systemd unit → `daemon-reload` + `enable --now` → 输出状态；
- 节点配置包含：node_id、client_cidr4、gateway、TUN、MTU、max_datagram、服务端 Ed25519 公钥；
- 默认二进制地址：`<面板地址>/agent-dist/pp-masque-node-linux-amd64`，也可在请求体用 `binary_url` 覆盖；
- 部署前需把交叉编译好的节点二进制放到服务器 `/opt/proxypilot/agent-dist/`。
## 部署记录（2026-11-08）

- 目标：`23.254.172.198`（`racknerd-b3c2b64`，Ubuntu，MySQL 8.4）；
- 版本：`V32.0`（源码 tag V32.0 + VPN 模块，构建时使用默认版本号），`CGO_ENABLED=0` Linux amd64；
- 迁移：自动执行 `0032_vpn`（仅新增 `vpn_*` 表，未改动既有表）；
- 备份：`/opt/proxypilot/proxypilot.bak.1794147680`（部署前原始二进制，V32.0）；
- 节点二进制：`/opt/proxypilot/agent-dist/pp-masque-node-linux-amd64`（6.5MB）；
- Agent 版本：`1.10.8`（`agent-dist/manifest.json` 未改动）；
- 部署后验证：
  - `systemctl is-active proxypilot` = active，`/healthz` 200，`/`、`/admin` 200；
  - 既有数据：servers=19、uuid_info=16、configs=639 未变；Agent 轮询正常；
  - 新接口：`GET/PUT /api/admin/vpn/settings` 200；节点 create → approve（分配 `10.99.0.11` / `10.88.1.0/24`）→ get → delete 全部 200，测试数据已清理；
  - 无 ERROR/PANIC 日志；
- 回滚：`cp /opt/proxypilot/proxypilot.bak.1794147680 /opt/proxypilot/proxypilot && systemctl restart proxypilot`；
- 磁盘：清理旧备份与 journal 后约 2.0GB 可用；
- **尚未部署前端**：当前二进制内嵌的是仓库已有 `assets_web`（不含 VPN 页面），
  管理后台「VPN 节点」页面需要重新构建前端并再次发布二进制后才会出现；后端 API 已可用。
## 版本与联调记录（2026-11-08 晚）

按新版本规划：

| 组件 | 版本 | 状态 |
|---|---|---|
| 管理平台 | **V32.4** | 已部署，含 VPN 管理页（前端已重新构建） |
| 节点 Agent | **2.0.0** | 已构建并放入 `agent-dist`，**未推送升级**（需后台逐台推送） |
| VPN 节点 `pp-masque-node` | **1.0.1** | 已部署到测试节点「台北」并运行 |

- 前端：`web-antd` 构建后同步 `server/internal/api/assets_web`，后台新增「节点与网络 → VPN 节点」；
- 节点部署修复：
  - `agentQueueTask` 第二个返回值不是成功标志，部署接口改为按 `task_id > 0` 判断；
  - Debian 13 最小系统只有 `nft` 没有 `iptables`：`pp-masque-node` 增加 **nftables 后端**
    （独立 `ip ppvpn` / `ip ppvpn_nat` 表，退出时整表删除），优先 iptables、缺失时自动 nft；
  - 部署脚本在服务未启动时输出 journal 并返回失败（不再 `|| true` 掩盖错误）。
- 测试节点：「台北-VPN测试」`vpn_nodes.id=3`，`10.99.0.11` / `10.88.1.0/24`，
  服务 `proxypilot-masque` active，监听 UDP 443。
- Agent 2.0.0 含 VPN 心跳（`POST /api/agent/vpn/heartbeat`），升级后「组网」页链路状态才会实时。

### 尚未完成

- Agent 2.0.0 尚未逐台推送；
- 客户端平台模式（设备码/节点列表/连接）未实现；
- 证书 CA/ACME、会话流量上报、配额实时扣减、`pp-mesh` 自建管理网。
## 界面调整与节点域名（V32.5）

- 「VPN 节点」页面改为 **「组网」**（节点与网络组）：只展示服务端 ↔ 节点端自建组网
  （中心域名 `vpncenter.yzfn.top`、中心 IP、网段、通道、链路、RTT、心跳），
  不再包含节点 CRUD 与授权；
- VPN 节点的创建/审批/部署/授权移到 **「节点绑定」** 页面底部「VPN 节点绑定」；
- 「协议模板」更名为 **「协议管理」** 并移入「节点与网络」组，新增 **VPN** 页签，
  用于维护 VPN 协议参数（启用、MTU、令牌 TTL、用户端地址池、组网网段、服务端组网 IP）；
- 「系统设置 → Agent」新增：
  - **备用 Agent 访问地址**（`agent_fallback_url`）；
  - **节点同步通道**（`vpn_sync_transport`：WARP 优先 / 自建管理网 / 自动）；
  - **WARP 不可用回落自建管理网**（`vpn_sync_fallback`）；
  - **组网中心域名**（`vpn_center_domain`，默认 `vpncenter.yzfn.top`）。
- 节点绑定必须填写 **节点域名**（`vpn_nodes.domain`，迁移 `0033`）：
  - 用作证书 CN/SAN（自签/内部 CA/ACME 都以此为准）；
  - 客户端连接 profile 的 `tls.server_name` 使用该域名；
  - `endpoint` 留空时自动取 `域名:监听端口`。

## Agent 直接集成组网 / VPN（待实施）

目标：Agent 进程内同时承担 ① 组网客户端 ② MASQUE 节点服务，不再单独安装 `pp-masque-node`。

建议实现路径：

1. 把 `masque/internal/node` 提升为公共包 `masque/node`；
2. Agent 增加 `internal/vpnnode`：读取 `/etc/proxypilot/agent/vpn.json`，
   进程内 `node.New(...).Run(ctx)`，并提供 Start/Stop/Status；
3. Agent systemd unit 增加 `CAP_NET_ADMIN/CAP_NET_RAW/CAP_NET_BIND_SERVICE`，
   写入 TUN/NAT/nft 规则；
4. 服务端 `vpn_apply` 任务改为写 Agent 配置 + `systemctl restart proxypilot-agent`，
   证书仍由任务生成（或后续由平台 CA 签发）；
5. 组网（WARP 客户端/自建管理网）由 Agent 内置管理，节点同步优先 WARP，备用自建。
### Agent 内嵌 VPN 节点（2.0.0）

- `masque/internal/node` 已提升为公共包 `masque/node`，Agent 可直接 import；
- Agent 新增 `internal/vpnnode`：读取 `/etc/proxypilot/agent/vpn.json`，
  进程内启动 `node.Server`，随 Agent 退出而停止；可用 `-vpn-config` 指定路径；
- Agent 构建（需要模块模式，仓库 vendor 尚未包含新依赖）：
  ```bash
  cd agent
  go build -mod=mod -ldflags "-X main.version=2.0.0" ./cmd/proxypilot-agent
  ```
- 内嵌后 Agent 二进制约 9.5MB（原 7.6MB）；
- 部署前需给 Agent 的 systemd 单元加 `CAP_NET_ADMIN/CAP_NET_RAW/CAP_NET_BIND_SERVICE`。
- 台北节点域名验证：`taipei-node.yzfn.top` → `82.152.91.4`，
  证书 `CN=taipei-node.yzfn.top`、`SAN=DNS:taipei-node.yzfn.top`，服务 active、UDP 443 监听。
### 台北节点已切换为 Agent 内嵌模式（2026-11-08）

- Agent 2.0.0（内嵌 VPN，9.98MB）已安装到 `/usr/local/bin/proxypilot-agent`，
  旧版本备份为 `/usr/local/bin/proxypilot-agent.bak`；
- 节点配置：`/etc/proxypilot/agent/vpn.json`（node_id=3，taipei-node.yzfn.top 证书，
  client_cidr=10.88.1.0/24，TUN=ppvpn0，configure=true）；
- 原独立服务 `proxypilot-masque` 已停止并 disable，UDP 443 由 Agent 进程持有；
- 验证：
  - `proxypilot-agent` active，`agent_version=2.0.0`；
  - `vpn_node_links`：node 3，transport=warp，state=up；
  - 节点 `last_seen` 持续更新，Agent 日志无错误。
## 组网与 VPN 解耦（V32.6）

- **组网**：所有服务器 ↔ 服务端，不要求绑定 VPN；组网页展示全部服务器
  （组网 IP、Agent 版本/状态、通道、链路、RTT、心跳、已绑定的 VPN 域名）；
  组网设置（中心域名 `vpncenter.yzfn.top`、中心组网 IP、组网网段、同步通道、
  WARP 回落）直接放在「组网」页；
- **VPN 节点绑定**：在「节点绑定」页，仅保留 VPN 相关字段（域名、接入地址、
  客户端子网、状态、部署/授权），组网 IP 与心跳不再在此展示；
- **VPN 设置**：在「协议管理 → VPN」，仅保留协议参数（启用、MTU、令牌 TTL、
  用户端地址池）；
- 新增迁移 `0034_vpn_mesh_domain`（`vpn_settings.server_mesh_domain`），默认
  `vpncenter.yzfn.top`；
- `GET /api/admin/vpn/mesh` 改为返回全部服务器（LEFT JOIN vpn_nodes / vpn_node_links），
  Agent 2 分钟内有心跳即标记链路 up。
## VPN 客户端 1.0（联调完成）

- 模块：`vpnclient/`（`github.com/yzfn/proxypilot/vpnclient`，quic-go v0.55）；
- 流程：门户令牌 → `/api/portal/vpn/nodes` → `/api/portal/vpn/connect` →
  HTTP/3 鉴权（`X-VPN-Auth`）→ 复用同一 QUIC 连接收发 DATAGRAM；
- 身份证：`-token`（门户 JWT）、`-device`（设备 ID）、`-node`、`-insecure`（自签节点）；
- 探测：构造 IPv4/UDP DNS 查询经隧道发往 `1.1.1.1:53`，校验返回报文。

联调结果（台北节点 `taipei-node.yzfn.top`）：

```
选择节点 #3 台北-VPN测试 endpoint=taipei-node.yzfn.top:443 online=true
连接成功：session=2 ip4=10.88.1.11 sni=taipei-node.yzfn.top mtu=1280
HTTP/3 鉴权成功，节点确认客户端 IP=10.88.1.11
DNS 探测成功：收到 89 字节响应（1.1.1.1:53）
```

说明：客户端 ↔ 节点（quic-go 0.55 ↔ 0.42）HTTP/3 与 DATAGRAM 互操作正常；
平台 connect API、JWT、节点鉴权、TUN/NAT 数据面全链路已打通。
