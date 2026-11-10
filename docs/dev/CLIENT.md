# 客户端开发详解

> 版本 2.7。源码 `vpnclient/`，模块 `github.com/yzfn/proxypilot/vpnclient`。
> 同一份代码产出三种形态：CLI、桌面（Wails v3）、安装器。

## 1. 三种构建形态

| 形态 | 命令 | 产物 | 入口 |
|---|---|---|---|
| CLI | `go build .` | `proxypilot-vpn-<ver>-windows-amd64-cli.exe` | `main.go` |
| 桌面 | `go build -tags desktop .` | 主程序（窗口 + 托盘） | `entry_desktop_windows.go` |
| 安装器 | `go build -tags "desktop installer" .` | `...-install.exe` | 同上（`installerBuild=true`） |

安装器内嵌主程序：

```go
//go:embed assets/ProxyPilotApp.exe
var embeddedApp []byte
```

**因此构建顺序不可颠倒**：主程序 → 复制到 `assets/ProxyPilotApp.exe` → 再构建安装器。
`scripts/build-release.ps1` 已固定这个顺序。

## 2. CLI 参数全表

| 参数 | 默认 | 说明 |
|---|---|---|
| `-api` | `https://proxypilot.yangzifun.org` | 面板地址 |
| `-token` | — | 门户登录令牌 |
| `-insecure` | false | 跳过节点证书校验 |
| `-ca` | — | 内部 CA 证书路径（PEM） |
| `-dns` | `1.1.1.1:53` | 连通性探测用 DNS |
| `-ui` / `-ui-addr` | false / `127.0.0.1:8899` | 启动本地控制台 |
| `-install` / `-uninstall` | — | 安装/卸载 |
| `-adapter-status` | — | 打印网卡状态 |
| `-diag` | — | 生成诊断报告 |
| `-tun-selftest` | — | 网卡接管自检 |
| `-mesh-token` | — | 组网 JWT（设置后进组网模式） |
| `-mesh-ip` / `-mesh-cidr` / `-mesh-tun` | — / `10.99.0.0/16` / `ppmesh0` | 组网地址与网卡 |
| `-mesh-endpoint` / `-mesh-sni` | `vpncenter.yzfn.top:443` | 组网中心 |
| `-mesh-api` / `-mesh-heartbeat` | 面板地址 / 20s | 组网心跳 |
| `-mesh-mode` | `mesh` | `mesh`=仅组网网段，`full`=全流量 |

## 3. 桌面运行时结构

```
main()
 ├─ setupFileLog()        日志 → %LOCALAPPDATA%\ProxyPilot\logs\client.log（2MB 滚动）
 ├─ cleanStaleTunnel()    清理上次残留：/1 路由、网卡地址、NRPT、DNS 防火墙规则
 ├─ ensureInstalledForApp()  未安装则弹提示
 ├─ runUI(ctx)            本地控制台（127.0.0.1:随机端口），内嵌 ui/index.html
 └─ Wails 应用：窗口 + 托盘菜单（显示/连接/断开/设置/退出）
```

退出路径（**必须断开**，2.7 起已修）：

```
窗口关闭 → OnShutdown → POST /api/disconnect → cancel()
托盘退出 → POST /api/disconnect → sleep 600ms → Quit
```

## 4. 连接状态机

```
idle
 └─ connect(nodeID)
     ├─ 校验配置（面板地址、令牌）
     ├─ 拉节点列表 GET /api/portal/vpn/nodes
     ├─ 选节点：nodeID > 0 ? 指定 : 记住的 SelectedNode : 第一个
     ├─ 换票 POST /api/portal/vpn/connect → token/ip4/mtu/dns
     ├─ QUIC 握手（TLS 1.3，ALPN=h3，SNI=节点域名）
     ├─ GET / 鉴权（Bearer）
     ├─ POST /tunnel 建流 → packetConn
     ├─ StartTunnel：建 Wintun、配地址/网关/路由/DNS、启 pumpUp/pumpDown
     ├─ runTunnelProbe：发 DNS 查询验证整条链路
     └─ connected
          ├─ 用户断开 → disconnect()
          └─ 任一步失败 → error（保留 message 供界面显示）
```

## 5. 数据面文件

| 文件 | 职责 | 关键点 |
|---|---|---|
| `stream_dial.go` | `POST /tunnel` 建流 | 返回 `*streamPacketConn`；acme 模式严格校验，internal_ca 拉 CA |
| `stream_tunnel.go` | `[2B len][IP]` 分帧 | 单帧 ≤4096；写失败只记一次日志 |
| `tunnel.go` | `pumpUp`/`pumpDown`/统计 | 写 TUN 失败 `continue`，不 break |
| `tunnel_probe.go` | 连通性探测 | **只发 DNS 消息**，不要拼 IP 头 |
| `route_windows.go` | 网卡/路由/DNS 配置与清理 | 双半区路由 + 服务器 `/32` 防环 |
| `dns_guard_windows.go` | NRPT + 物理网卡 53 封禁 | 断开必须撤销 |
| `cleanup_windows.go` | 启动自愈 | 清 `/1` 路由、残留地址、DNS 守卫 |
| `ca_trust.go` | 拉取平台 CA 并缓存 | `%APPDATA%\ProxyPilot\ca.pem` |

## 6. Windows 接管细节

```powershell
# 1) 清旧配置
netsh interface ipv4 set address name="ProxyPilot VPN" source=dhcp

# 2) 配地址（不带网关，避免写永久路由）
netsh interface ipv4 add address "ProxyPilot VPN" 10.88.1.12 255.255.255.0

# 3) 网关只写活动路由（ipconfig 可见但不持久化）
netsh interface ipv4 add route 0.0.0.0/0 "ProxyPilot VPN" 10.88.1.1 metric=60 store=active

# 4) 服务器主机路由（防环）：经真实网关
route add 82.152.91.4 mask 255.255.255.255 10.0.0.1 metric 1

# 5) 双半区接管
netsh interface ipv4 add route 0.0.0.0/1   "ProxyPilot VPN" 0.0.0.0 metric=5 store=active
netsh interface ipv4 add route 128.0.0.0/1 "ProxyPilot VPN" 0.0.0.0 metric=5 store=active

# 6) DNS（TUN 网卡）
netsh interface ipv4 set dnsservers name="ProxyPilot VPN" static 1.1.1.1 primary
netsh interface ipv4 add dnsservers name="ProxyPilot VPN" 8.8.8.8 index=2
```

清理顺序（`cleanupTun`）：

```
revertDNSGuard()
route delete 0.0.0.0   mask 128.0.0.0
route delete 128.0.0.0 mask 128.0.0.0
route delete 0.0.0.0   mask 0.0.0.0 <隧道网关>
逐条删除 res.Routes（含 /32）
netsh interface ipv4 delete address name=<网卡> addr=<ip>
netsh interface ipv4 set address name=<网卡> source=dhcp
route print -4 校验 → 仍有经隧道网关的路由则再删并记日志
```

## 7. DNS 泄露防护（2.6+）

问题：Windows 智能多宿主名称解析会并行问所有网卡，运营商 DNS 时延低必然先回。

解决两层：

```powershell
# 第一层：NRPT 强制所有域名走隧道 DNS
Add-DnsClientNrptRule -Namespace '.' -NameServers 1.1.1.1,8.8.8.8 -DisplayName 'ProxyPilot-VPN'

# 第二层：物理网卡封禁出站 53
New-NetFirewallRule -DisplayName 'ProxyPilot-DNS-WLAN-UDP' -Direction Outbound -Action Block `
  -Protocol UDP -RemotePort 53 -InterfaceAlias 'WLAN'
```

撤销按 `DisplayName` 前缀精确清理；启动时也清理一次（崩溃自愈）。

## 7.5 IPv6 泄露防护（2.9+）

问题：TUN 只有 IPv4，接管路由是 `0.0.0.0/1` + `128.0.0.0/1`；
物理网卡若拿到公网 IPv6（如电信 `240e::/20`）并存在默认路由 `::/0`，
浏览器访问支持 v6 的站点会**绕过隧道直连**，真实 IPv6 暴露。

实测（修复前）：

```
curl -6 https://ipv6.icanhazip.com
→ 240e:46c:8300:47c4:...      ← 家宽真实地址
```

处理（fail-closed）：

```powershell
# 连接时：关闭物理网卡的 IPv6 绑定
Disable-NetAdapterBinding -Name 'WLAN' -ComponentID ms_tcpip6
# 断开时：按记录恢复
Enable-NetAdapterBinding  -Name 'WLAN' -ComponentID ms_tcpip6
```

- 被关闭的网卡列表写入 `%APPDATA%\ppvpn\ipv6-disabled.json`
- 启动时 `restoreIPv6FromMarker()` 恢复（防崩溃/断电后一直没 v6）
- 连接详情页显示「IPv6 泄露防护：已封禁（WLAN）」

Linux/macOS 尚未实现（需 `sysctl net.ipv6.conf.<if>.disable_ipv6` 与
`networksetup -setv6off`），界面上显示「未封禁」。

## 7.6 本地控制台来源校验（2.9+）

本地控制台监听 127.0.0.1，浏览器里任何网页都能向它发 `no-cors` 的
`text/plain` POST。若不加校验，恶意页面可静默调用 `/api/settings`
把面板地址改成攻击者服务器（令牌保留），下次连接就把 Bearer 令牌外传。

`localGuard` 三条规则：

| 规则 | 拒绝结果 |
|---|---|
| Host 必须是 `127.0.0.1`/`localhost`/`::1` 且端口匹配 | 403 forbidden host |
| 带 Origin 的请求必须同源 | 403 forbidden origin |
| 有 body 的写请求 Content-Type 必须是 `application/json` | 415 |

实测：

```
GET  /api/state                        → 200
GET  /api/state  Host: evil            → 403
POST /api/settings Origin: evil        → 403
POST /api/settings text/plain          → 415
POST /api/settings 同源 JSON           → 200
POST /api/disconnect（空 body，托盘）  → 200
```

## 7.7 令牌加密存储（2.10+）

问题：`%APPDATA%\ppvpn\config.json` 里是**明文 JWT**（168 字符），
且 `os.WriteFile(..., 0o600)` 在 Windows 上权限位无效（走继承 ACL），
任何以当前用户身份运行的程序都能读走令牌。

处理：Windows 用 DPAPI（`CryptProtectData`，当前用户作用域）加密：

```json
{ "api": "https://...", "token_enc": "AQAAANCMnd8BFdERjHoAwE/Cl+...", ... }
```

- 落盘只有 `token_enc`，内存中才有明文 `Token`
- 换 Windows 用户或换机器无法解密（读不出来会提示重新登录）
- 旧版明文配置在启动时自动迁移：读 → 加密 → 重写
- 写完后 `icacls /inheritance:r /grant:r "%USERNAME%":F` 收紧 ACL
- `/api/state` 新增 `secret_storage: "dpapi"` 便于排查

实测（隔离配置目录）：

```
迁移前字段: api, token, ...
迁移后字段: api, token_enc（520 字符 base64）, ...   token 明文长度 0
/api/state: logged_in=true, token_hint=eyJh…nPnE, secret_storage=dpapi
重启后再查: logged_in 仍为 true（密文可解密）
```

Linux/macOS 暂无等价实现（`secret_unix.go` 直通，文件权限 0600）。

## 8. 本地控制台 API

`ui.go` 暴露的本地接口（仅 127.0.0.1，不需要鉴权）：

| 路径 | 方法 | 作用 |
|---|---|---|
| `/api/state` | GET | 会话状态全量快照 |
| `/api/settings` | POST | 保存面板地址/令牌/insecure |
| `/api/nodes` | GET | 拉可用节点 |
| `/api/select-node` | POST | 记住选中节点 |
| `/api/connect` | POST | 连接（可指定 node_id） |
| `/api/disconnect` | POST | 断开并清理 |
| `/api/logout` | POST | 清除令牌 |
| `/api/repair` | POST | 修复网络（清残留路由/DNS） |
| `/api/logs` | GET | 日志尾部 |
| `/api/install/*` | POST | 安装向导 |

状态字段（`/api/state` 关键项）：

```json
{
  "status": "connected", "message": "…",
  "node_id": 4, "node_name": "香港-2", "ip4": "10.88.2.10", "mtu": 1280,
  "adapter": "ProxyPilot VPN", "gateway": "10.88.2.1",
  "routes": ["38.147.187.60/32", "0.0.0.0/1", "128.0.0.0/1"],
  "rx": 434400, "tx": 302900, "rtt_ms": 178,
  "net_ok": true, "net_rtt_ms": 176, "net_error": "",
  "probe_ok": true, "duration": 51,
  "tun_error": "", "dns_guard": ["NRPT", "FW:WLAN"],
  "selected_node": 4, "logged_in": true
}
```

## 9. 安装器

`ui_install.go` 的 8 个步骤：

```
check → copy → driver → adapter → registry → firewall → autostart → shortcut
```

细节：

- 替换被占用的主程序：先 `taskkill /F /IM ProxyPilot.exe`，再 20 次重试 rename
- 创建快捷方式：生成纯 ASCII 的临时 ps1（UTF-8 BOM），避免 PowerShell 解析中文报错
- 防火墙：`netsh advfirewall` 加 in/out 规则与 ICMP 规则
- 卸载登记：写 `HKLM\...\Uninstall\ProxyPilot`

## 10. 版本与发布

- 版本常量：`main.go` → `const Version = "2.7"`
- Windows 资源：`go-winres simply --file-version 2.7.0.0 --icon assets/app.ico`
- 产物由 `scripts/build-release.ps1` 统一输出到 `dist-release/`

## 11. 常见问题

| 现象 | 原因 | 处理 |
|---|---|---|
| 装了旧图标 | Windows 图标缓存 | `ie4uinit.exe -show` 或注销 |
| 断开后上不了网 | 残留 `/1` 路由或 NRPT | 设置页「修复网络」 |
| 连通检测不通但能上网 | 探测报文拼错（历史 bug） | 已修，确认 ≥2.4 |
| 切换节点无反应 | 旧版本会重连失败 | ≥2.4 已改为手动生效 |
| 安装报 Access denied | 旧进程占用 | 安装器自动 taskkill，仍失败手动结束 |
| 证书校验失败 | acme 证书不覆盖域名 | 面板「证书管理」重新签发 |
