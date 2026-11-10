# Windows 系统 VPN 集成（UWP VPN 插件）

> 结论先说：**可以做，但不是「加个电话簿条目」那种做法，而是一个独立的 UWP 应用 +
> 受限能力 + MSIX 签名**，而且 **本仓库当前的构建机做不了**（缺工具链，见第 7 节）。
> 本文给出完整可行的方案、精确的技术约束和分步计划。

---

## 1. 为什么不能直接在「添加 VPN 连接」里填

那个对话框（`控制面板 → 网络和共享中心 → 添加 VPN 连接` / 设置里的「添加 VPN」）
只能创建 **RAS 电话簿条目**（`rasphone.pbk`），`VPN 类型` 只接受
**PPTP / L2TP-IPsec / SSTP / IKEv2** 四种。

ProxyPilot 的数据面是 **HTTP/3 上的 MASQUE 可靠流**（QUIC/UDP 443），
不在这四种里，所以：

- 在那个对话框里填我们的域名和账号，**一定连不上**（协议对不上，不是配置问题）。
- 想让它出现在系统 VPN 列表，只有两条正式途径：
  1. **UWP VPN 插件**（`Windows.Networking.Vpn.IVpnPlugin`）—— Windows 10 1809+ 官方支持，本文采用。
  2. RAS 第三方 VPN 插件 DLL（`RASVPN`，Cisco/Juniper 那套 CMAK）——
     **已废弃、无文档、需要 32/64 位双份签名 DLL**，不考虑。

---

## 2. 目标形态

集成完成后：

- 设置 → 网络和 Internet → VPN 里出现 **ProxyPilot**，可用网络浮层一键连接/断开；
- 由 Windows 负责适配器、路由、按流量计费/应用过滤等系统级策略；
- ProxyPilot 客户端退化为后台服务 + 托盘，**不再自己建 Wintun 适配器**。

> 关键点：一个包只能装一个 VPN 插件实例，插件会**接管**适配器。
> 所以启用插件后必须停用现有的 Wintun 接管逻辑，二者不能并存。

---

## 3. 架构

```
┌───────────────────────────── Windows ─────────────────────────────┐
│  设置 / 网络浮层                                                   │
│        │ 点「连接」                                                 │
│        ▼                                                          │
│  ProxyPilot.Plugin（UWP 包，托管 IVpnPlugin）                       │
│        │ ① VpnChannel 建适配器、给下发明文 IP 包                    │
│        │ ② 把包写进命名管道                                        │
│        ▼                                                          │
│  \\.\pipe\proxypilot-vpn   （帧格式：2 字节大端长度 + 载荷）         │
│        ▲                                                          │
│        │                                                          │
│  ProxyPilot 客户端（现有 Go 核心，后台服务）                        │
│        │ ③ 登录门户 → 申请会话 → 建 HTTP/3 隧道                     │
│        ▼                                                          │
└────────┼──────────────────────────────────────────────────────────┘
         │ QUIC / UDP 443
         ▼
   节点（masque / proxypilot-agent）→ 公网
```

**职责划分**：

| 组件 | 负责 | 不负责 |
|---|---|---|
| UWP 插件（C#/C++） | 只搬包：VpnChannel ↔ 命名管道 | 加密、（不实现任何协议） |
| Go 核心 | 门户鉴权、会话、QUIC 隧道、NAT 侧 | 适配器与路由 |

这样 C# 侧代码量很小（~300 行），也不用在 C# 里重写 MASQUE。

---

## 4. UWP 插件工程

### 4.1 清单（`Package.appxmanifest`）

```xml
<Package ... IgnorableNamespaces="uap rescap">
  <Capabilities>
    <Capability Name="internetClient" />
    <!-- 受限能力：见 4.3 -->
    <rescap:Capability Name="networkingVpnPlugin" />
  </Capabilities>

  <Applications>
    <Application Id="ProxyPilotVpn" Executable="ProxyPilot.VpnPlugin.exe"
                 EntryPoint="ProxyPilot.VpnPlugin.App">
      <Extensions>
        <uap:Extension Category="windows.networking.vpnPlugin">
          <uap:VpnPlugin>
            <uap:PluginName>ProxyPilot</uap:PluginName>
          </uap:VpnPlugin>
        </uap:Extension>
      </Extensions>
    </Application>
  </Applications>
</Package>
```

`windows.networking.vpnPlugin` 这个扩展类别就是系统 VPN 列表里那一项的来源。

### 4.2 插件主体

实现 `Windows.Networking.Vpn.IVpnPlugin`：

```csharp
void Connect(VpnChannel channel, VpnParameters parameters);
void Disconnect(VpnChannel channel);
```

`Connect` 里要做的固定套路（顺序不能错）：

1. `channel.AssociateTransport(socket, null)` —— 把承载 socket 交给系统；
2. 建 `VpnRouteAssignment` / `VpnTrafficFilterAssignment`，把 `0.0.0.0/0`
   和 `::/0` 纳入隧道（这就是「全局接管」）；
3. `channel.StartWithTrafficFilter(routes, trafficFilter, interfaceId, routeAssignment, null, null, null, 0)`
   —— **这个重载的参数表以你安装的 Windows SDK 版本为准**，
   微软官方样例 `VpnPlugin` 用的是同一套，首次对接照抄最省事；
4. 起两个循环：
   - 下行：`channel.GetVpnReceivePacketBuffer()` → 填明文 →
     `channel.AppendVpnSendPacketBuffer(buf)` → `channel.FlushVpnSendPacketBuffer()`
   - 上行：从命名管道读包 → `channel.GetVpnSendPacketBuffer()` → 填 → flush

> 注意：插件不要自己做加解密。我们的加密在 Go 侧的 QUIC 里，
> 插件只当「明文包搬运工」，这样 `Encapsulate/Decapsulate` 都可以不用。

### 4.3 受限能力 `networkingVpnPlugin`

这是**受限能力（restricted capability）**：

- 上架 Microsoft Store：需要提交申请，由微软审核批准；
- 企业/自签名侧载：**可以**包含该能力，但要求
  - 包必须用受信任证书签名，
  - 目标机开启侧载或开发者模式，
  - 用 `Add-AppxPackage` 安装（不能双击）。
  
  局域网内分发建议：自签代码签名证书 → 导入「受信任的根证书颁发机构」与
  「受信任人」→ `Add-AppxPackage -Path ProxyPilot.Plugin.msix`。

### 4.4 与 Go 核心的管道协议

- 管道名：`\\.\pipe\proxypilot-vpn`（服务端 = Go 核心，客户端 = 插件）
- 帧格式（双向一致）：

```
+--------+--------+------------------+
| len_hi | len_lo | payload (IP 包)  |
+--------+--------+------------------+
    2 字节大端       len 字节
```

- 一次连接只服务一个会话；断开即表示 VPN 断开
- 缺少心跳时会话超时（Go 侧 30s 无数据视为断开）

---

## 5. Go 侧需要的改动

1. **新增「管道数据面」模式**：`--datapath=pipe`。
   - 不创建 Wintun、不下发路由、不装 DNS/防火墙规则（这些交给 Windows）；
   - 只做：门户鉴权 → 建 QUIC 隧道 → 在「隧道 ↔ 命名管道」之间搬包。
2. **命名管道服务端**：`\\.\pipe\proxypilot-vpn`，等待插件连接。
3. **端口/会话生命周期**：插件断开 → 结束服务端会话（已有 `releasePanelSession`）。
4. 现有 Wintun 路径保留，默认仍用它；只有检测到插件在用时切到 pipe 模式。

这部分是纯 Go，可在当前机器编译与测试（管道帧编解码可以单测）。

---

## 6. 分步计划与预估

| 步骤 | 内容 | 依赖 | 预估 |
|---|---|---|---|
| 1 | Go：管道帧编解码 + 服务端 + `--datapath=pipe` | 无 | 0.5 天（可在此机验证） |
| 2 | C# UWP 插件工程 + 清单 | VS2022 + Win SDK | 1 天 |
| 3 | 插件 ↔ 管道联调（本机 loopback） | 1、2 | 0.5 天 |
| 4 | MSIX 打包 + 自签证书 + 安装脚本 | Win SDK | 0.5 天 |
| 5 | 端到端：系统 VPN 列表出现、一键连接、全局接管 | 全部 | 1 天 |
| 6 | 与现有 Wintun 路径的开关与回退 | — | 0.5 天 |

合计约 **4 个工作日**，风险集中在 2 和 4（WinRT 重载与受限能力签名）。

---

## 7. 当前构建机的硬限制（已实测）

在本仓库的开发机上执行：

```
dotnet --list-sdks      → 未安装 dotnet
Windows Kits\10\bin     → 不存在
Visual Studio           → 无
WindowsAppRuntime 1.7   → 只有运行时，没有 SDK
```

**因此这台机器无法编译 UWP 工程，也无法生成 MSIX 并签名。**
C# 部分必须在一台装了 VS2022（含「通用 Windows 平台开发」工作负载）
+ Windows SDK 10.0.22621 的机器上完成；Go 部分不受影响。

---

## 8. 前置确认

接入前需要确认两件事，否则做完也装不上：

1. **分发方式**：只在少数自己人的机器上用（自签 + 侧载），
   还是要公开分发（必须走 Store 审核拿受限能力）？
2. **是否接受插件与 Wintun 二选一**：插件启用后，
   现有「客户端自己建网卡」的路径必须关掉，否则两个适配器会抢默认路由。