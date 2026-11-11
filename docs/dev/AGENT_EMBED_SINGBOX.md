# 把 sing-box 合并进 Agent —— 可行性核查与方案

> 结论：**可以做，而且推荐做**。但「合并」有两种做法，代价差一个数量级：
> 推荐 **二进制内嵌（go:embed）**，不推荐 **源码库内嵌（libbox）**。
>
> 另外要先纠正一个预期：**今天升级 sing-box 本来就必须发一次 Agent 版本**
> （版本号硬编码在 Agent 里），所以合并不会让更新变慢 —— 它会让更新变得
> **可复现、不依赖外网**。

---

## 1. 现状核查（基于代码，非推测）

### 1.1 sing-box 由 Agent 以「独立二进制 + systemd 服务」方式运行

```
agent/internal/runner/component.go:23   singboxVersion = "1.14.4"
agent/internal/runner/component.go:57   r.ensureUnit("sing-box",
                                          "/usr/local/bin/sing-box run -c /etc/sing-box/sb.json")
```

即：Agent 只负责**安装 + 起 systemd 单元**，sing-box 本身是另一个进程。

### 1.2 版本是硬编码常量

```go
// component.go:23
singboxVersion = "1.14.4"
```

`installSingbox()` → `installSingboxVersion(ctx, singboxVersion)`。
**要升级 sing-box，必须改这行、重新编译 Agent、重新发布** —— 这一点很关键，
说明「合并」并不新增「必须发版」这个约束，它本来就存在。

### 1.3 安装依赖外网

```go
// component.go:225 singboxDownloadURLs
base + "/sing-box-<ver>-linux-<arch>.tar.gz"          // 自建分发基址（R2）
base + "/sing-box/sing-box-<ver>-linux-<arch>.tar.gz" // 兼容旧布局
"github.com/SagerNet/sing-box/releases/download/vX/…"  // 回落 GitHub
```

- 主路径：R2（`s3.proxypilot.yangzifun.org/agent`）
- 兜底：GitHub release

问题：目标机上如果拉不到 R2/GitHub，就装不上服务。属于**运行时依赖网络**。

### 1.4 体量数据（实测）

| 项 | 大小 |
|---|---|
| Agent 当前产物 | **9.7 MB** |
| sing-box 1.14.4 linux-amd64 tar.gz | **30.2 MB**（解压后单文件约 45 MB） |
| sing-box 最新版 | v1.14.4（2026-10-24 发布） |
| Agent 构建 | `CGO_ENABLED=0`，纯静态 |

sing-box 是纯 Go 项目，`github.com/sagernet/sing-box` 在 proxy.golang.org 有完整
版本列表（v1.5.0-rc.4 ~ v1.14.4），**既能当库引，也能当二进制嵌**。

### 1.5 统计采集走本地 HTTP API

```
agent/internal/stats/clash.go     GET http://<addr>/connections
agent/internal/stats/v2ray.go     (v2ray stats API)
```

这一点决定了：**无论 sing-box 以哪种方式合并，只要它还监听本地 API 端口，
统计代码一行都不用改。**

---

## 2. 两种合并方式对比

### 方案 A：源码库内嵌（import `sagernet/sing-box`）

把 sing-box 当 Go 包编译进 Agent，进程内直接跑。

| 维度 | 评估 |
|---|---|
| 产物大小 | Agent 从 9.7 MB → **约 55~70 MB** |
| 进程模型 | 单进程（Agent 与 sing-box 同生共死） |
| 崩溃隔离 | **无** —— sing-box panic 会带走 Agent，而 Agent 是 SSH/exec 通道，等于失去远程管理 |
| 配置 | 需从「写 sb.json + 起进程」改为「构造 option 结构体 + box.New/Start」，**改造量大** |
| 统计 | 仍需起 clash/v2ray API（或在进程内直接读），可复用 |
| 升级 sing-box | 改 go.mod 版本 → 重新编译（与今天等价） |
| 实现风险 | 高：sing-box 的协议注册依赖大量 `include` 包，且 libbox 面向移动端，桌面/服务器端用法非主流路径 |

### 方案 B：二进制内嵌（`go:embed`）——**推荐**

把 sing-box 二进制作为资源嵌进 Agent，运行时释放到磁盘并按原方式以子进程启动。

| 维度 | 评估 |
|---|---|
| 产物大小 | Agent 从 9.7 MB → **约 18~25 MB**（tar.gz 压缩后 30 MB，进二进制前用 gzip 压一次约 12~15 MB） |
| 进程模型 | 保持双进程（systemd 单元不变） |
| 崩溃隔离 | **保持** —— sing-box 挂了 Agent 还活着，能自动重启并上报 |
| 配置 | **零改动** —— 仍是 `sing-box run -c /etc/sing-box/sb.json` |
| 统计 | **零改动** —— 仍是本地 clash/v2ray HTTP API |
| 升级 sing-box | 换内嵌文件 + 改版本常量 → 重编（与今天等价），但**不再需要目标机能连外网** |
| 实现风险 | 低：只替换「下载」为「写文件」，安装逻辑其余部分不动 |

**结论：选 B。** 它把所有收益（离线可用、版本确定、单产物分发）都拿到了，
却没有引入「单进程崩溃」这个最危险的代价。

---

## 3. 方案 B 的实现设计

### 3.1 目录与构建

```
agent/
  assets/
    singbox/                       # 构建期生成，不提交
      sing-box-1.14.4-linux-amd64.gz
      sing-box-1.14.4-linux-arm64.gz
  internal/runner/
    embedded_singbox.go            # go:embed + 释放逻辑
    embedded_singbox_stub.go       # 无资产时的降级（保持现有下载路径）
```

`go:embed` 的通配必须能编译通过，所以资产缺失时用 build tag 隔离：

```go
//go:build singbox_embedded

package runner

import _ "embed"

//go:embed assets/singbox/sing-box-linux-amd64.gz
var singboxAMD64 []byte
```

构建脚本按架构选择 embed 哪一份（或两份都嵌，运行时按 `componentArch()` 选）。

### 3.2 安装流程改造

现有：

```
installSingbox → installSingboxVersion → singboxDownloadURLs → fetchSingboxTarball
```

改造后：

```
installSingbox
  ├─ 1. 若内嵌资产存在 → 解压写到 componentBinDir/sing-box，chmod 0755，校验版本
  ├─ 2. 内嵌资产缺失 → 走原下载路径（保底，向后兼容）
  └─ 3. ensureUnit("sing-box", "/usr/local/bin/sing-box run -c /etc/sing-box/sb.json")
```

**关键：保留下载路径作为兜底**，这样即使某次构建忘了嵌资产，服务也不会装不上。

### 3.3 版本自检

```go
// 释放后执行一次 `sing-box version`，比对内嵌版本
// 不一致则记 ERROR 并强制重写文件
```

防止「磁盘上是旧版、二进制里是新版」这种静默错配。

### 3.4 产物体积控制

| 做法 | Agent 体积 |
|---|---|
| 内嵌未压缩 | +45 MB（不可接受） |
| 内嵌 gzip | +12~15 MB（可接受，约 22 MB） |
| 运行时 gzip 解压 | 一次性，约 1 秒 |

只嵌当前目标架构（按目标平台构建时决定），**不把 amd64+arm64 都塞进去**。

---

## 4. sing-box 发新版后的发布流程

目标是**从 sing-box 发布到我们发版 ≤ 30 分钟，且全程可回滚**。

### 4.1 自动化脚本 `scripts/sync-singbox.ps1`

```
1. 查询 GitHub API：latest release tag（或用参数指定版本）
2. 下载 sing-box-<ver>-linux-{amd64,arm64}.tar.gz
3. 校验官方 SHA256SUMS
4. 解压出单二进制 → gzip → 写入 agent/assets/singbox/
5. 更新 agent/internal/runner/component.go 的 singboxVersion
6. 更新 agent 版本号（minor +1）
7. go test ./internal/runner/...  （验证 embed + 释放逻辑）
8. 构建 → 发布 R2 → 推送三台节点
9. 节点上执行 `sing-box version` 回读校验
```

### 4.2 耗时拆解

| 步骤 | 预计 |
|---|---|
| 下载 + 校验（30 MB × 2） | 1~2 分钟 |
| 改版本 + 编译（两条架构） | 2~3 分钟 |
| 发布 R2 + 推送 3 台节点 | 1~2 分钟（现有 `/root/push_33.sh`） |
| **合计** | **约 5~8 分钟**（人只按一次） |

### 4.3 兼容性门禁

sing-box 大版本（1.14 → 1.15）可能改配置字段。发布前必须：

```
1. 用新版跑一次 `sing-box check -c /etc/sing-box/sb.json`
2. check 失败 → 中止发布，保留旧版
3. 通过 → 才进发布流程
```

这条门禁必须内置在脚本里，不能靠人工记得。

### 4.4 回滚

```
Agent 侧保留上一版二进制：
  /usr/local/bin/sing-box.previous
  /usr/local/bin/sing-box         （当前）
```

新版本启动后 10 秒内 `systemctl is-active sing-box` 不为 active →
自动回滚到 `.previous` 并上报 ERROR。

---

## 5. 实施阶段

| 阶段 | 内容 | 产出 | 验收 |
|---|---|---|---|
| **1** | embed 骨架 + 释放逻辑 + 版本自检 | Agent 能离线装出 sing-box | 断网机器上 `proxypilot-agent` 可拉起 sing-box |
| **2** | 保留下载兜底 + 回滚标记 | 双路径可用 | 删除 embed 资产后仍能联网安装 |
| **3** | `sync-singbox.ps1` 自动化 | 一键同步新版 | 跑一次脚本，三台节点 `sing-box version` 变成新版 |
| **4** | `sing-box check` 门禁 | 配置不兼容时中止 | 故意给错配置，脚本退出非 0 |
| **5**（可选） | 面板展示「组件版本」 | 面板可见 Agent/sing-box 版本对 | 面板与 `systemctl` 一致 |

阶段 1~2 是核心（约 1 天）；3~4 是省时间的关键（约半天）；5 是可选。

---

## 6. 风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| Agent 体积从 9.7 → ~22 MB | 分发流量增加 | 可接受；R2 出流量成本极低 |
| embed 资产忘更新 | 装出旧版 | §3.3 版本自检 + 发布脚本强制比对 |
| sing-box 配置跨版本不兼容 | 服务起不来 | §4.3 `sing-box check` 门禁 + §4.4 自动回滚 |
| 构建时间变长 | CI 变慢 | 资产只在版本变化时重新生成，可缓存 |
| 磁盘空间 | 多占 ~45 MB | 释放前删除旧文件，保留 `.previous` 一份 |

---

## 7. 明确不做

| 项 | 原因 |
|---|---|
| 把 sing-box 编译成库跑在 Agent 进程内 | panic 会带走 SSH/exec 通道，等于失去远程管理能力 |
| 把 amd64 + arm64 同时嵌入 | 体积翻倍，而单次分发只需一个架构 |
| 放弃 systemd 单元、改由 Agent 直接 fork | 会丢掉 systemd 的自动重启与日志集成 |

---

## 8. 现状对比：合并前后

| 项 | 现在 | 合并后 |
|---|---|---|
| 分发产物 | Agent 9.7 MB + sing-box 30 MB（分开） | Agent 约 22 MB（一个） |
| 目标机需要外网 | **是**（下载 sing-box） | **否** |
| 版本一致性 | 靠 URL 与常量对齐 | 编译期确定 |
| 升级 sing-box | 改常量 + 发版 | 改常量 + 发版（脚本自动化） |
| 升级耗时 | 手工 | 5~8 分钟一键 |
| 崩溃隔离 | 有 | **保持** |
| 统计/配置代码改动 | — | **零** |

---

## 9. 一句话结论

**可以合并，建议用二进制内嵌而不是源码内嵌。**

- 收益：离线可部署、版本确定、单产物分发，且今天「升级要发版」这一点不会变差
- 代价：Agent 体积 +12~15 MB，配置与统计代码零改动
- 关键保障：保留下载兜底、`sing-box check` 门禁、失败自动回滚 `.previous`

按阶段 1~4 实施约 1.5 天，之后每次跟进 sing-box 新版约 5~8 分钟自动化完成。