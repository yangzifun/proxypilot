# ProxyPilot 1.4 发布说明（MASQUE 与协议选择）

日期：2026-11-16
版本：面板 V1.4 / Agent 1.4 / VPN 客户端 1.4
状态：**源码与构建完成，尚未部署**（MASQUE 为 PoC，需单节点灰度验证）。

## 新功能

### 1. 自研 MASQUE（标准 CONNECT-IP）服务端
- 自研 `tunnel/node` 新增标准 MASQUE CONNECT-IP 服务端（RFC 9484 + RFC 9297），**不使用 sing-box**：
  - 同一 UDP 443 HTTP/3 入口：`CONNECT` + `:protocol=connect-ip` + `/.well-known/masque/ip/*/*/`；
  - Bearer JWT（复用 PilotLink 隧道 token）；
  - ADDRESS_ASSIGN / ADDRESS_REQUEST / ROUTE_ADVERTISEMENT capsule；
  - HTTP Datagram 承载 IPv4 报文（Context ID 0）；
  - 复用现有 TUN/NAT/DNS relay/sessionSet；PilotLink（私有协议）行为不变。
- 已知限制：仅 IPv4；地址使用面板/JWT 分配的 ip4/32；无动态地址池；无 ICMP Too Big；未做公网链路验证。

### 2. VPN 客户端协议选择
- 新增 `vpnclient/internal/masque` 最小 HTTP/3 客户端（不 fork quic-go），实现 CONNECT-IP + capsule + HTTP Datagram；
- 客户端协议 `auto / private / masque`：
  - `auto`：先 PilotLink（私有协议），失败回退 MASQUE；
  - 设置页可选协议，连接页/详情显示当前协议与回退状态；
  - TUN/路由/DNS 守卫/断线清理复用原实现。

### 3. 认证失效显示未认证页面
- 客户端启动/运行中校验门户令牌：
  - 401/403：清除令牌并正常显示登录（未认证）页，不闪主界面；
  - 网络错误：保留令牌，显示“暂时无法验证登录状态”；
  - 登录成功后自动重新校验并刷新节点。

### 4. 面板协议能力
- 迁移 0058：`vpn_nodes` 增加 private/masque 能力字段，`vpn_sessions.protocol`；
- `/api/portal/vpn/nodes` 返回 `protocols` / `protocol_details`；
- `/api/portal/vpn/connect` 接受 `protocol`（默认 private），非法/未启用/未就绪明确拒绝并落库 protocol；
- 管理端节点编辑支持协议开关与 MASQUE 参数。

## 构建产物

`dist-release/`：面板 V1.4（amd64/arm64）、Agent 1.4（linux amd64/arm64）、VPN 客户端 1.4（Windows 安装包/CLI、Linux、macOS）、安装/升级脚本、SHA256SUMS、MANIFEST。

## 验证

- tunnel：`go build/vet/test` 通过，40 PASS（含真实 HTTP/3 CONNECT-IP e2e）；
- server：`go build/test` 通过；
- vpnclient：`go build`、`go build -tags desktop`、`go vet` 通过；masque/协议/认证单测通过；
- Agent：vendor 同步（含 tunnel masque），`go build/test/vet` 通过；
- 全量发布构建通过。

## 部署/验证顺序（建议）

1. 部署面板 V1.4（迁移 0058）；
2. 单节点（洛杉矶-1）升级 Agent 1.4，管理端开启 MASQUE 并置 `masque_state=ready`；
3. 安装客户端 1.4，选择 MASQUE 连接，验证 ADDRESS_ASSIGN、TCP/UDP/DNS；
4. 验证 Auto 回退（禁用/阻断 MASQUE 后回 PilotLink）；
5. 通过后再逐节点开启 MASQUE，不默认全量。

## 回滚

- 面板：保留 V1.3 二进制与 DB 备份；迁移 0058 为新增列/默认值，可回滚应用；
- Agent：保留 1.3 二进制（/opt/proxypilot/agent-dist 回滚目录）；
- 客户端：旧版本安装包（client-dist 备份）；
- 关闭 MASQUE 只需将节点 `masque_enabled=0` / `masque_state=disabled`。

<｜DSML｜ calls>
<｜DSML｜ invoke name="write">
<｜DSML｜ parameter name="content" string="true"># ProxyPilot 1.4 发布说明（MASQUE 与协议选择）

日期：2026-11-16
版本：面板 V1.4 / Agent 1.4 / VPN 客户端 1.4
状态：**源码与构建完成，尚未部署**（MASQUE 为 PoC，需单节点灰度验证）。

## 新功能

### 1. 自研 MASQUE（标准 CONNECT-IP）服务端
- 自研 `tunnel/node` 新增标准 MASQUE CONNECT-IP 服务端（RFC 9484 + RFC 9297），**不使用 sing-box**：
  - 同一 UDP 443 HTTP/3 入口：`CONNECT` + `:protocol=connect-ip` + `/.well-known/masque/ip/*/*/`；
  - Bearer JWT（复用 PilotLink 隧道 token）；
  - ADDRESS_ASSIGN / ADDRESS_REQUEST / ROUTE_ADVERTISEMENT capsule；
  - HTTP Datagram 承载 IPv4 报文（Context ID 0）；
  - 复用现有 TUN/NAT/DNS relay/sessionSet；PilotLink（私有协议）行为不变。
- 已知限制：仅 IPv4；地址使用面板/JWT 分配的 ip4/32；无动态地址池；无 ICMP Too Big；未做公网链路验证。

### 2. VPN 客户端协议选择
- 新增 `vpnclient/internal/masque` 最小 HTTP/3 客户端（不 fork quic-go），实现 CONNECT-IP + capsule + HTTP Datagram；
- 客户端协议 `auto / private / masque`：
  - `auto`：先 PilotLink（私有协议），失败回退 MASQUE；
  - 设置页可选协议，连接页/详情显示当前协议与回退状态；
  - TUN/路由/DNS 守卫/断线清理复用原实现。

### 3. 认证失效显示未认证页面
- 客户端启动/运行中校验门户令牌：
  - 401/403：清除令牌并正常显示登录（未认证）页，不闪主界面；
  - 网络错误：保留令牌，显示“暂时无法验证登录状态”；
  - 登录成功后自动重新校验并刷新节点。

### 4. 面板协议能力
- 迁移 0058：`vpn_nodes` 增加 private/masque 能力字段，`vpn_sessions.protocol`；
- `/api/portal/vpn/nodes` 返回 `protocols` / `protocol_details`；
- `/api/portal/vpn/connect` 接受 `protocol`（默认 private），非法/未启用/未就绪明确拒绝并落库 protocol；
- 管理端节点编辑支持协议开关与 MASQUE 参数。

## 构建产物

`dist-release/`：面板 V1.4（amd64/arm64）、Agent 1.4（linux amd64/arm64）、VPN 客户端 1.4（Windows 安装包/CLI、Linux、macOS）、安装/升级脚本、SHA256SUMS、MANIFEST。

## 验证

- tunnel：`go build/vet/test` 通过，40 PASS（含真实 HTTP/3 CONNECT-IP e2e）；
- server：`go build/test` 通过；
- vpnclient：`go build`、`go build -tags desktop`、`go vet` 通过；masque/协议/认证单测通过；
- Agent：vendor 同步（含 tunnel masque），`go build/test/vet` 通过；
- 全量发布构建通过。

## 部署/验证顺序（建议）

1. 部署面板 V1.4（迁移 0058）；
2. 单节点（洛杉矶-1）升级 Agent 1.4，管理端开启 MASQUE 并置 `masque_state=ready`；
3. 安装客户端 1.4，选择 MASQUE 连接，验证 ADDRESS_ASSIGN、TCP/UDP/DNS；
4. 验证 Auto 回退（禁用/阻断 MASQUE 后回 PilotLink）；
5. 通过后再逐节点开启 MASQUE，不默认全量。

## 回滚

- 面板：保留 V1.3 二进制与 DB 备份；迁移 0058 为新增列/默认值，可回滚应用；
- Agent：保留 1.3 二进制（/opt/proxypilot/agent-dist 回滚目录）；
- 客户端：旧版本安装包（client-dist 备份）；
- 关闭 MASQUE 只需将节点 `masque_enabled=0` / `masque_state=disabled`。