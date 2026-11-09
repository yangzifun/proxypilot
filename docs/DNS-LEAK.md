# DNS 泄露排查报告

## 一、现象（截图证据）

- 出口 IP：`23.141.204.54`（United States）→ 流量确实走了隧道
- 但 7 个 DNS 解析器里 **4 个是中国电信上海**（116.236.159.110 / .58 / .34 / .14），3 个是 Cloudflare 洛杉矶（104.23.250.x / 172.70.213.45）
- 结论：**DNS 查询一半漏到了本地运营商**

## 二、根因分析

### 1. 只给虚拟网卡配了 DNS

客户端当前代码（`route_windows.go`）只做了一件事：

```go
netsh interface ipv4 set dnsservers name="ProxyPilot VPN" static 1.1.1.1 primary
netsh interface ipv4 add dnsservers name="ProxyPilot VPN" 8.8.8.8 index=2
```

即：**只在隧道网卡上配置了 DNS**。

### 2. Windows「智能多宿主名称解析」会并行问所有网卡

Windows 的 DNS Client 默认启用 *Smart Multi-Homed Name Resolution*：

- 同一域名会**同时**向所有已连接网卡（WLAN 与虚拟网卡）的 DNS 服务器发出查询
- **谁先回包就用谁的结果**

WLAN 网卡上的 DNS 是运营商下发的 `10.0.0.1`（上游即中国电信 116.236.159.x），
本机到它的 RTT 只有 1-2 ms；而隧道内的 1.1.1.1 要走 380+ ms 的跨境链路。

→ 运营商 DNS 永远先回，结果就是**出口在国外、DNS 解析在国内**。

### 3. 为什么「连通检测」能通

连通检测是直接向隧道内 1.1.1.1 发 UDP 查询（绑定隧道 IP），
不经过 Windows 的解析器选择逻辑，所以它能通——**和系统 DNS 泄露是两回事**。

### 4. 泄露的实际危害

| 风险 | 说明 |
|---|---|
| 隐私 | 运营商/本地网络能看到你访问的全部域名 |
| 污染 | 部分域名被返回污染结果或本地就近节点 |
| 分片化 CDN | 拿到的是国内 CDN 节点，跨境流量反而变慢 |
| 指纹 | 出口在美、DNS 在中国，是很强的「VPN 用户」特征 |

## 三、修复方案（客户端 2.6 已实现）

两层防护，缺一不可。

### 第 1 层：NRPT 强制所有域名走隧道 DNS

```
Add-DnsClientNrptRule -Namespace "." -NameServers 1.1.1.1,8.8.8.8 -DisplayName "ProxyPilot-VPN"
```

- 命名空间 `.` = 所有域名，系统级生效
- 绕过「多网卡并行查询」逻辑，Windows 解析器只问指定服务器
- 断开时按 `DisplayName` 精确删除，不改动系统原有 DNS

### 第 2 层：封禁物理网卡的明文 DNS 出站

```
New-NetFirewallRule -DisplayName "ProxyPilot-DNS-WLAN-UDP" -Direction Outbound -Action Block \
  -Protocol UDP -RemotePort 53 -InterfaceAlias "WLAN"
（TCP 53 同理）
```

- 即使用户装了绕过 NRPT 的应用（硬编码 DNS），包也发不出去
- 只作用于**物理网卡**，隧道网卡的 DNS（1.1.1.1）不受影响
- 断开时按 `ProxyPilot-DNS-*` 前缀批量删除

### 第 3 层：崩溃自愈

`cleanStaleTunnel()` 启动时先 `revertDNSGuard()`：
即使上次是被强杀/蓝屏，重启客户端也会先清掉残留规则，
避免出现「能 ping 通 IP、所有域名都解析不了」的尴尬状态。

## 四、验证方法

连接后，在「连接详情」页新增了一行：

```
DNS 泄露防护   已启用（2 项）      ← 悬停可见 NRPT / FW:WLAN
```

或手工核对：

```powershell
# 1. NRPT 规则存在
Get-DnsClientNrptRule | Where-Object DisplayName -like 'ProxyPilot*' |
  Select-Object Namespace, NameServers

# 2. 物理网卡 DNS 被封
Get-NetFirewallRule -DisplayName 'ProxyPilot-DNS-*' | Select-Object DisplayName, Enabled, Action

# 3. 物理网卡只剩运营商 DNS，但已发不出去
Resolve-DnsName www.baidu.com -Server 10.0.0.1     # 应超时
Resolve-DnsName www.baidu.com                       # 应返回 Cloudflare 结果

# 4. 断开后再查一次，规则应全部消失
Get-DnsClientNrptRule | Where-Object DisplayName -like 'ProxyPilot*'
```

浏览器再访问 DNS 泄露测试站，应只剩隧道 DNS 的解析器。

## 五、封堵后的预期

| 项目 | 修复前 | 修复后 |
|---|---|---|
| DNS 解析器 | 4 个中国电信 + 3 个 Cloudflare | 全部 Cloudflare/隧道 DNS |
| 系统解析路径 | WLAN 10.0.0.1 | NRPT → 1.1.1.1（走隧道） |
| 明文 DNS 出站 | 允许 | 物理网卡已封禁 |
| 崩溃残留 | 可能锁死 DNS | 启动自动清理 |

## 六、注意事项

1. **DoH 不受影响**：Chrome/Edge 开启「安全 DNS」时会走 TLS 443，这类查询会正常进入隧道（不泄露），无需额外处理。
2. **局域网域名**：NRPT 规则覆盖所有域名，连接期间 `router.local` 之类内网域名会解析失败——这是 VPN 全隧道的正常表现，断开即恢复。
3. **权限**：NRPT 与防火墙规则都需要管理员权限，客户端本身即以 `requireAdministrator` 运行，无需额外提权。
4. **macOS/Linux**：Linux 使用 systemd-resolved 的 `resolvectl domain <if> ~.` 已达成同等效果；macOS 目前仍依赖系统默认行为，后续可用 `scutil --dns` 补一层。
