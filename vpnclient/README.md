# ProxyPilot VPN 客户端

桌面（Wails v3）/ 命令行两用的 VPN 客户端。连接后会**接管系统网络**：创建虚拟网卡、配置地址与 MTU、按模式下发路由；断开时自动清理。

## 运行要求

| 平台 | 能力 | 权限 | 驱动 |
|---|---|---|---|
| Windows 10/11 | Wintun 虚拟网卡 + `route.exe` 路由 | **管理员**（清单已 `requireAdministrator`） | `wintun.dll` **已内置**；安装时释放到安装目录并注册网卡 |
| Linux | `/dev/net/tun` + `ip` | root 或 `CAP_NET_ADMIN` | 内核自带 |
| macOS | `utun` + `route` | root（或用 sudo 启动） | 系统自带 |

## Windows 安装（先安装，再使用）

Windows 版**必须先安装**：安装阶段才会释放 Wintun 驱动并**注册虚拟网卡**，之后连接时直接复用。

```
dist-release/
├── ProxyPilot-V35.2-windows-amd64.exe     ← 主程序（GUI，清单要求管理员）
├── install.cmd                            ← 双击安装（自动提权）
├── install.ps1
├── uninstall.cmd                          ← 双击卸载
└── uninstall.ps1
```

安装流程：

1. 双击 `install.cmd`（UAC 提权）
2. 复制程序到 `%LOCALAPPDATA%\Programs\ProxyPilot`
3. 调用 `ProxyPilot.exe -install`：
   - 把内置 `wintun.dll` 释放到安装目录
   - 创建（或复用）名为 **ProxyPilot VPN** 的虚拟网卡 —— 首次会安装 Wintun 驱动
   - 在 `HKLM\...\Uninstall\ProxyPilot` 写入卸载信息
4. 脚本用 `Get-NetAdapter` 校验网卡已存在
5. 创建开始菜单与桌面快捷方式

安装完成后，「网络连接」里就能看到 **ProxyPilot VPN** 适配器（未连接时处于断开状态）。

### 后台运行与系统托盘

安装时会询问「是否随 Windows 启动并在后台（托盘）运行？」（也可用 `-Background` / `-NoBackground` 指定）。

- 开机自启写入 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，命令为 `"...\ProxyPilot.exe" -background`
- `-background`：启动后**不弹窗口**，直接常驻托盘
- **关闭窗口不退出**：窗口只是隐藏，隧道与托盘继续工作
- 托盘菜单：显示窗口 / 隐藏到托盘 / 连接 / 断开 / 打开设置 / 退出
- 托盘图标左键单击：切换窗口显示与隐藏
- 只有托盘菜单里的「退出」才会真正结束进程

对应的前端/程序参数：

| 参数 | 作用 |
|---|---|
| `-background` / `-tray` | 启动即隐藏，仅托盘运行 |
| （无参） | 正常打开窗口，关闭时缩到托盘 |

卸载可在「设置 → 应用 → ProxyPilot VPN」，或双击 `uninstall.cmd`。

### 手动执行安装动作

```powershell
# 管理员 PowerShell
.\ProxyPilot-V35.2-windows-amd64.exe -install -install-dir "$env:LOCALAPPDATA\Programs\ProxyPilot"
.\ProxyPilot-V35.2-windows-amd64.exe -adapter-status   # 校验网卡是否注册
.\ProxyPilot-V35.2-windows-amd64.exe -uninstall        # 移除网卡与卸载信息
```

`-install` 会输出 JSON（同时写入 `%TEMP%\proxypilot-install.json`），便于脚本解析：

```json
{ "ok": true, "install_dir": "...", "exe_path": "...", "adapter": "ProxyPilot VPN",
  "driver_path": "...\\wintun.dll", "version": "V35.2" }
```

## 接管模式

| 模式 | 生成的路由 | 适用 |
|---|---|---|
| `mesh`（默认） | 组网网段（默认 `10.99.0.0/16`）→ 网卡 | 只想访问组网内其它设备，其余流量走本地网络 |
| `full` | `0.0.0.0/1` + `128.0.0.0/1` → 网卡（覆盖默认路由），并给 VPN 服务器加 `/32` 主机路由防回环 | 全局代理：所有 IPv4 流量进隧道 |

> `full` 模式用两条 `/1` 覆盖默认路由，**不改动原有 `0.0.0.0/0`**，断开时只删自己加的两条，天然可回滚。

## 快速自检

不需要真实 VPN 会话，验证「驱动 + 权限 + 路由下发 + 清理」是否就绪：

```powershell
# Windows（管理员 PowerShell）
.\ProxyPilot.exe -tun-selftest -tun-selftest-mode mesh -tun-selftest-keep 5
```

```bash
# Linux / macOS
sudo ./proxypilot-vpn -tun-selftest -tun-selftest-mode full -tun-selftest-keep 5
```

预期输出：

```
自检开始：模式=mesh
网卡已创建：ppvpn0（10.99.0.250/16 · mesh · MTU 1420 · 路由 1 条）
  路由：10.99.0.0/16
5 秒后自动清理…
自检完成：网卡与路由已清理
```

失败时的典型提示：

| 提示 | 原因 |
|---|---|
| `Access is denied` | 未以管理员/root 运行 |
| `加载 wintun.dll 失败` | 安装包缺少内置驱动（重新构建即可，驱动已 `go:embed`） |
| `创建 TUN 失败：operation not permitted` | Linux 未给 `CAP_NET_ADMIN`，或容器没映射 `/dev/net/tun` |

## 命令行用法

```bash
# 组网（仅网段）
sudo ./proxypilot-vpn -mesh-token <JWT> -mesh-ip 10.99.0.11 \
  -mesh-cidr 10.99.0.0/16 -mesh-endpoint vpncenter.example.com:443 \
  -mesh-mode mesh

# 组网（全局接管）
sudo ./proxypilot-vpn -mesh-token <JWT> -mesh-ip 10.99.0.11 -mesh-mode full

# 本地控制台（127.0.0.1:8899）
./proxypilot-vpn -ui
```

## 桌面界面

连接后主界面新增两行实时信息：

```
虚拟网卡   ppvpn0 / ProxyPilot VPN
接管路由   2 条：10.99.0.0/16、0.0.0.0/1 …
```

「首选项」里可设置：

- 网络接管范围（仅组网网段 / 接管全部流量）
- 组网网段
- 虚拟网卡名（留空用默认）
- 是否把系统 DNS 指向隧道

## 实现要点

- `netdev.go`：`PacketDevice` 抽象 + `TunSpec`/`TunResult`
- `tun_windows.go` / `tun_unix.go`：平台网卡创建（Wintun / TUN）
- `route_linux.go` / `route_windows.go` / `route_darwin.go`：地址、MTU、路由下发与清理
- `tunnel.go`：TUN ↔ QUIC DATAGRAM 双向泵 + 收发统计 + 中断上报
- `wintun_windows.go`：内置驱动释放与预加载

> 路由清理是幂等的：断线、切换节点、退出程序都会调用 `cleanupTun`。