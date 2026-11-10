# ProxyPilot 36.0 / Agent 3.0 / VPN 3.0 方案

> 三条主线：数据面双通道（DATAGRAM 分路 + FEC）、命名修正（为真实 MASQUE 预留）、
> BBR 拥塞控制。外加一项零风险的 socket 缓冲优化。
>
> 本文只谈设计与取舍，不含实现。

---

## 0. 目标与验收

| 目标 | 对应问题 | 验收口径 |
|---|---|---|
| 大流量下小包不被拖累 | DNS 解析慢、视频通话卡、游戏抖动 | UDP 单向时延 P95、DNS 解析耗时 |
| UDP 丢包可容忍但不失控 | 裸 UDP 应用无重传 | 开 FEC 后 UDP 流丢包率 |
| 跨境带宽上来 | 实测 Mbps 偏低 | 同节点同链路 speedtest 中位数 |
| 命名不再混淆 | `masque/` 名不副实，挡住真实 MASQUE | 代码/面板/文档无歧义 |
| 可回退 | 任何一项出问题能关掉 | 面板开关 + 旧版本兼容 |

---

## 1. 数据面双通道（DATAGRAM 分路）

### 1.1 为什么要分路

现在所有 IP 包挤在一条可靠流里：

```
一个 UDP 小包（DNS/QUIC/游戏）必须排在前面所有重传之后才交付
→ 大流量时小包被队头阻塞
```

DATAGRAM（RFC 9221）不重传、不排序，丢了只影响它自己。
把 UDP 流量迁到 DATAGRAM，小包就不再被 TCP 大包拖累。

### 1.2 分流规则

按内层 IP 头的协议号分流（`pkt[9]`）：

| 协议号 | 类型 | 承载 | 理由 |
|---|---|---|---|
| 6 | TCP | **可靠流** | 需要可靠 + 有序，DATAGRAM 反而更慢（要等上层 RTO） |
| 17 | UDP | **DATAGRAM** | 本来不要求有序，丢了让上层自己重试 |
| 1 | ICMP | **可靠流** | ping 是诊断手段，丢包必须真实反映链路，不能被 DATAGRAM 的丢弃污染 |
| 其他 | — | 可靠流 | 保守处理 |

判断代码位置：客户端 `vpnclient/tunnel.go` 的 `pumpUp`，
节点 `masque/node/tunLoop` 的下发处。

### 1.3 可靠性补强：FEC（可选）

DATAGRAM 丢了就没了。对「裸 UDP 应用」这是可接受的（他们会自己重试），
但有些场景（游戏、实时音视频）希望少丢一点。

**方案：XOR 奇偶校验分组 FEC，只作用于 DATAGRAM 通道。**

```
每 K 个 DATAGRAM 额外发 1 个校验包（K 可配，默认 8）
组内丢任意 1 个 → 用校验包还原，无需重传
开销 = 1/K（默认 12.5%）
```

帧格式（DATAGRAM 载荷）：

```
+--------+--------+--------+--------+--------+
| type   | group  | index  | seq    | payload|
| 1B     | 2B     | 1B     | 4B     | ...    |
+--------+--------+--------+--------+--------+
type: 0=数据 1=校验
```

要点：

- **注意**：这里的 FEC 与「可靠流不能做 FEC」的结论**不冲突**。
  流由 QUIC 负责重传，应用层 FEC 救不了拥塞窗口；
  但 DATAGRAM 根本不重传，应用层 FEC 是**唯一**能补它的手段。
- 只在 DATAGRAM 通道启用，配置关闭时零开销。
- 组大小 K 可配：K=4 抗 25% 丢包，K=8 抗 12.5%；K 越小开销越大。
- 连续性差的场景（大量零星小包）分组会凑不满，超时（如 20ms）即发送。

### 1.4 节点会话模型改造（关键阻塞项）

现在 `register()` 是「同 IP 接管」：

```go
// masque/node/server.go:244
if cur, ok := s.sessions[sess.ip.String()]; ok {
    cur.conn.CloseWithError(0, "session replaced")   // 同 IP 直接踢掉
}
```

双通道后同一设备会**同时**有流会话与 DATAGRAM 会话，这个策略会把对方踢掉。

改造：

```go
type sessionSet struct {
    stream    *session   // POST /tunnel
    datagram  *session   // GET /
}
sessions map[string]*sessionSet   // ip -> 两路会话
```

连带要改：

| 位置 | 改动 |
|---|---|
| `server.go:244 register` | 按 `kind` 写入对应槽位，不再互相踢 |
| `server.go:260 unregister` | 只清自己那个槽位 |
| `server.go:268 lookup` | 返回 `*sessionSet` |
| `tunLoop` 下发 | 按 `pkt[9]` 选 `stream`/`datagram`；某路不可用时**回退到另一路** |
| `readSession` | 保留，作为 DATAGRAM 上行入口 |

**回退策略很重要**：某一路未建立（例如客户端没开 DATAGRAM）时，
节点必须能把包回退到可用通道，否则会静默丢包。

### 1.5 客户端侧改动

```
pumpUp：
  读 TUN → 按协议号分流
     ├ UDP → SendDatagram（含 FEC 编码）
     └ 其他 → SendPacket（可靠流，现有逻辑）

pumpDown：
  两条下行源并发写 TUN
     ├ RecvPacket()        （流）
     └ ReceiveDatagram()   （DATAGRAM，含 FEC 解码）
```

两块写 TUN 的协程要共用一个写锁，避免交叉写坏包。

### 1.6 配置项（面板「协议管理 → VPN」新增）

| 键 | 取值 | 默认 | 说明 |
|---|---|---|---|
| `vpn_datagram_enabled` | bool | true | 关闭后全部走可靠流（等同 2.38 行为） |
| `vpn_fec_mode` | off / xor | off | DATAGRAM 通道 FEC |
| `vpn_fec_group` | 4 / 8 / 16 | 8 | 每组包数，越小越抗丢包、开销越大 |

下发路径：`vpn_settings` → `vpn.json` → 节点；同时 `/api/portal/vpn/connect`
响应里带上，客户端不用另外请求。

### 1.7 预期与风险

| 项 | 预期 |
|---|---|
| DNS 解析耗时 | 大流量下 P95 明显下降 |
| 视频通话/游戏抖动 | 明显改善 |
| 测速 Mbps | **基本不变**（TCP 仍走流） |
| FEC 带宽开销 | 1/K（默认 12.5%，仅 UDP） |

风险：

- DATAGRAM 受 `max_datagram_frame_size` 限制，内层包不能超过它 → 需要按 MTU 校验
- 部分中间设备对 QUIC DATAGRAM 不友好（极少数），表现为 UDP 流量「全丢」→ 必须能一键切回全流模式
- FEC 分组在低流量下会引入最多 20ms 延迟 → 组内未满时超时即发

---

## 2. 命名修正（为真实 MASQUE 预留）

### 2.1 现状：名不副实的清单

| 位置 | 现状 | 问题 |
|---|---|---|
| 目录/模块 | `masque/`、`github.com/yzfn/proxypilot/masque` | 我们不是 MASQUE |
| 二进制 | `pp-masque-node` | 同上 |
| JWT 声明 | `aud = masque-node` | 同上 |
| 面板文案 | 「VPN（MASQUE / HTTP3）」「组网」 | 和 sing-box 的真实 MASQUE 混淆 |
| 包注释 | `masque/node/config.go` 已写明「非 RFC 9484」 | 说明里承认了，但名字没改 |

同时仓库里**确实有**真实的 MASQUE：sing-box 的 `masque-server`（WARP 用，端口 13707）。
两套东西共用一个词，以后接真实 MASQUE 必然出乱子。

### 2.2 目标命名

| 现在 | 改为 | 说明 |
|---|---|---|
| `masque/` 目录 | `tunnel/` | 自研 HTTP/3 隧道 |
| `pp-masque-node` | `pp-tunnel-node` | 旧名保留软链接一个版本 |
| `aud=masque-node` | `aud=pp-tunnel` | **过渡期双接受** |
| 面板「VPN（MASQUE / HTTP3）」 | 「VPN（自研隧道 · HTTP/3）」 | 明确不是标准 MASQUE |
| 面板「组网」 | 「自建组网」 | 已改过，保持一致 |

**真实的 MASQUE 单独占位**：面板里 WARP 那块继续叫 MASQUE，
并新增说明「由 sing-box masque-server 提供」，与自研隧道区分开。

### 2.3 兼容期（不能一刀切）

TLS/JWT 是硬约束，改名必须能平滑：

1. `vpn.json` 的 `audience` 保持 `masque-node`，**节点同时接受新旧两个值**
   （verifier 里用白名单，不是单值比较）
2. 客户端先发新 audience，节点接受；等所有节点升到 3.0 后再切换为只接受新值
3. 二进制改名后，`/opt/proxypilot/pp-masque-node` 保留为软链接，防止
   旧 systemd unit / 旧安装脚本失效
4. 面板文案与文档同步改，配置键名不变（避免迁移数据）

### 2.4 为真实 MASQUE 预留的接口

现在是「一条流 + 一个 DATAGRAM 会话」硬编码。建议抽一层：

```go
// tunnel/transport.go
type Transport interface {
    Name() string                                   // "pp-stream" / "massque-connectip"
    Open(ctx, endpoint, token) (DataPlane, error)   // 建立数据面
}

type DataPlane interface {
    SendIPTCP(pkt []byte) error
    SendIPUDP(pkt []byte) error
    RecvIP() ([]byte, error)
    Close() error
}
```

当前实现 `pp-stream`；未来接真实 MASQUE（RFC 9484 CONNECT-IP）时新增
`connectip` 实现，上层（TUN、会话、心跳）完全不用改。

这层抽象**本轮不做完整实现**，只把接口占位与现有代码对齐，
避免 3.0 之后再动一次数据面。

---

## 3. BBR 拥塞控制

### 3.1 为什么需要

CUBIC 是丢包驱动：一丢包就把窗口砍半，150ms RTT 下恢复极慢。
跨境链路常年 1~2% 丢包，CUBIC 实际只能跑到链路带宽的一小部分。

BBR 用「带宽 × RTT」建模，丢包不下调窗口，只按真实瓶颈发包。

### 3.2 选型

| 方案 | 优点 | 缺点 | 结论 |
|---|---|---|---|
| `github.com/apernet/quic-go` | Hysteria2 在用；`quic.Config` 暴露 `CongestionControl`；成熟验证 | 第三方分支，要跟上游 API | **采用** |
| 自己 fork quic-go 打补丁 | 完全可控 | 维护成本更高 | 不采用 |
| 上游 quic-go | 稳定 | **不暴露拥塞控制选择** | 现状，无法用 BBR |

### 3.3 两端改造点

| 位置 | 改动 |
|---|---|
| `vpnclient/go.mod`、`masque/go.mod` | 换成 apernet 分支（版本固定，写清楚理由） |
| 客户端 `stream_dial.go` / `main.go` | `quic.Config.CongestionControl = "bbr"` |
| 节点 `server.go:111` | 同上，从 `vpn.json` 读 |
| 面板 | 新增 `vpn_congestion_control`（cubic / bbr），默认 bbr |
| 下发 | `vpn_settings` → `vpn.json` → 节点；connect 响应带给客户端 |

### 3.4 风险与回退

- **API 漂移**：分支若落后上游，遇到 quic-go 安全更新要手动 forward
  → 记录在 `docs/dev/` 里，每次升级 quic-go 都要验证
- **行为差异**：BBR 更「激进」，共享链路上可能挤压其他流量
  → 面板可切回 cubic，逐节点生效
- **回退**：配置项切 cubic 即恢复 2.38 行为，无需回滚代码

---

## 4. 顺带：socket 缓冲放大（零风险，建议同期）

```
BDP = 带宽 × RTT = 100 Mbps × 150 ms ≈ 1.9 MB
```

若 UDP socket 接收缓冲仍是默认 64KB，理论上限约 `64KB / 0.15s ≈ 3.4 Mbps`。

- 自建 `net.UDPConn` 并 `SetReadBuffer/SetWriteBuffer(4MB)`
- 用 `quic.Transport{Conn: conn}` 接管，客户端与节点都改

改动小、无协议变更，**建议与本期一起做**，因为它会直接影响 BBR 的效果上限
（BBR 估出来的带宽再高，缓冲不够也吃不下）。

---

## 5. 里程碑

| 阶段 | 内容 | 工作量 |
|---|---|---|
| M0 | socket 缓冲 + 对照测量（先拿到基线） | 0.5 天 |
| M1 | 命名修正（目录/二进制/audience 双接受/面板文案）+ Transport 接口占位 | 2~3 天 |
| M2 | 节点会话模型改造（`sessionSet`，双通道并存 + 回退） | 2 天 |
| M3 | 双通道数据面（客户端分流 + 两条下行）+ 面板开关 | 2~3 天 |
| M4 | FEC（XOR 分组） | 1~2 天 |
| M5 | 换 apernet/quic-go + BBR（两端） | 2~3 天 |
| M6 | 联调、压测、文档 | 1~2 天 |

合计约 **11~15 天**。

**顺序理由**：M0 先拿到基线，否则后面所有优化都无法判断是否有效；
M1 先改名，避免后面新代码再沿用旧名；M2 是 M3 的前置阻塞项。

---

## 6. 验收方法

每一项都要有可对比的数字，同一节点、同一时段、连续三次取中位数。

| 项 | 方法 |
|---|---|
| 带宽 | `speedtest` 或 `curl -o /dev/null` 拉大文件，记 Mbps |
| 延迟与丢包 | `ping 1.1.1.1 -n 100` 记 min/avg/丢包率 |
| 小包被拖累 | 大流量后台运行时 `nslookup` 计时；或 `ping -l 32` 抖动 |
| 隧道内部 | `/api/state` 的 `tunnel_quality`、`quic_lost`、`inner_bad` |
| 节点侧 | `流隧道流量` 与新增的 `DATAGRAM 流量`（frames/rx/tx/bad/dropped） |
| FEC 效果 | 开/关 FEC 各跑一次 UDP 打流，对比丢包率与带宽开销 |

---

## 7. 回退方案

| 出问题的项 | 回退动作 | 影响面 |
|---|---|---|
| DATAGRAM 分路 | 面板关 `vpn_datagram_enabled` → 全走流（等同 2.38） | 无需发版 |
| FEC | 面板切 `off` | 无需发版 |
| BBR | 面板切 `cubic` | 无需发版 |
| 命名改动 | 旧二进制软链接 + 旧 audience 双接受 | 无需回滚 |
| 整体回退 | 客户端与 Agent 回 2.38 / 2.2.4（tag 保留） | 需重新分发 |

**所有新功能都必须有面板开关，且开关不依赖客户端升级即可生效** ——
这是本期的硬要求。

---

## 8. 明确不做

| 项 | 原因 |
|---|---|
| 应用层 FEC 作用在**可靠流** | 流由 QUIC 重传，FEC 救不了拥塞窗口，纯浪费带宽 |
| Brutal 固定速率 | 需要精确带宽配置，配错会拥塞崩溃 |
| 多流分片（HIP） | 与本期 DATAGRAM 分路目标重叠，先做 DATAGRAM；若仍不够再议 |
| 真实 MASQUE 实现 | 本期只做命名与接口预留，不实现 RFC 9484 |