# DATAGRAM 通道彻底修复方案：单连接复用

> 目标：修掉「UDP 走 DATAGRAM 后上行丢 62%」的根因，同时保留双通道带来的
> 「UDP 不被 TCP 大流量队头阻塞」收益。
>
> 本文只谈设计与取舍，不含实现。

---

## 1. 问题的完整证据链

### 1.1 现象

| 观测 | 数值 |
|---|---|
| 客户端 `path_udp_sent` | **343** |
| 节点 `DATAGRAM 流量 tx` | **129** |
| 节点 `dropped` | **0** |
| 客户端 `quic_lost` | **0** |
| 质量采样成功率 | 34 / 73（47%） |

**客户端成功发出 343 个 DATAGRAM，节点只收到 129 个，节点侧没有任何失败计数。**

同一时刻可靠流：`frames=90316 rx=128766 tx=90316 bad_pkt=0`，一个包都没丢。

### 1.2 根因

客户端同时维持**两条独立的 QUIC 连接**：

```
连接 A（GET / 鉴权）        → 承载 DATAGRAM
连接 B（POST /tunnel）      → 承载 TCP/ICMP 可靠流
```

连接 A 在 `GET /` 返回后就没有业务流量了。它的问题在于：

1. **空闲连接拥塞窗口极小**：quic-go 的 cwnd 在没有流量时回落到初始值附近，
   DATAGRAM 虽然不重传，但仍然受拥塞控制约束，超出窗口的部分只能排队或丢
2. **连接可能已经死了但客户端不知道**：UDP 没有 FIN，`SendDatagram` 在本地
   排队即返回成功；对端连接已被回收时，报文就凭空消失
3. 节点侧 `readSession` 随连接退出后，DATAGRAM 进来也没有人读

所以「发送成功」和「对方收到」之间没有任何保障 —— 这正是 343 vs 129 的解释。

### 1.3 顺带排除的

- 不是 CPU/GC：`inner_bad=0`、`uplink_dropped=0`
- 不是 DNS 转发：节点本地 10 次查询 19~61ms 全部返回
- 不是链路整体质量：同一路径上可靠流 0 丢包
- 不是带宽不足：实测瞬时 >100 Mbps，20 秒内从 4.7 爬到 11.4 Mbps 且仍在上升
  （远端 TCP 慢启动，不是隧道限速）

---

## 2. 方案对比

| 方案 | 做法 | 结论 |
|---|---|---|
| **A. 单连接复用** | GET / 与 POST /tunnel 走同一条 QUIC 连接，DATAGRAM 也用这条 | ✅ 采用 |
| B. 保温连接 A | 定期发心跳/空 DATAGRAM 维持 cwnd | ⚠️ 治标；连接死亡仍无感知 |
| C. 关掉 DATAGRAM | 全部走可靠流 | ✅ 保留为面板开关/回退手段 |
| D. 改用标准 CONNECT-IP | RFC 9484 | ❌ 本期不做，工作量远超收益 |

选 A 的理由：DATAGRAM 与流共享同一条连接的**拥塞窗口**与**存活状态**，
这两个正是当前失效的根因；而且线缆协议不变（仍是从前那两个端点），
新旧版本可互通。

---

## 3. 方案 A 设计

### 3.1 目标形态

```
一条 QUIC 连接（一个 5 元组、一个 cwnd、一个存活状态）
  ├─ HTTP/3 GET  /        鉴权，返回分配地址后请求结束（连接不受影响）
  ├─ HTTP/3 POST /tunnel  双向流，承载 TCP/ICMP
  └─ QUIC DATAGRAM 帧      承载 UDP
```

### 3.2 客户端改造

新增 `vpnclient/tunnel_session.go`，把「建连 + 鉴权 + 开流」收进一个对象：

```go
type tunnelSession struct {
    udpConn *net.UDPConn      // 4MB 缓冲
    qtr     *quic.Transport
    conn    *quic.Conn        // 唯一连接
    h3      *http3.Transport  // GET / 与 POST /tunnel 共用
}

func newTunnelSession(ctx, endpoint, sni string, insecure bool) (*tunnelSession, error)
func (t *tunnelSession) auth(ctx context.Context, token string) (assignedIP string, err error)
func (t *tunnelSession) openStream(ctx context.Context, token string) (*streamPacketConn, error)
func (t *tunnelSession) Close() error
```

关键实现点：

1. `h3.Dial` 回调**返回已建立的 `t.conn`**，让两次请求落在同一条连接上
2. 鉴权成功后 `resp.Body` 必须读干并关闭，否则连接被这条流占住
3. `DATAGRAM` 直接 `t.conn.SendDatagram` —— 和流共用 cwnd
4. 关闭顺序：先关 `stream` → 再关 `h3` → 再关 `qtr` → 最后 `udpConn`
   （`http3.Transport.Close()` 会连带关闭底层连接）

替换现有两处调用：

- `ui.go`：`authHTTP3(...)` + `openTunnelStream(...)` → `newTunnelSession` + `auth` + `openStream`
- `main.go`（CLI 路径）同样替换

### 3.3 节点改造

节点侧改动很小，因为 M2 已经让 `sessionSet` 支持双会话并存：

1. `GET /` 保持现状（注册 datagram 会话 + `go readSession`），
   **但不再把 `closeFn` 绑到「请求结束」上** —— 只在连接真正关闭时清理。
   现在 `readSession` 阻塞在 `ReceiveDatagram(conn.Context())`，已经满足。
2. `register` 的 key 仍是 `ip`，槽位按 kind 区分，无需改动。
3. 新增一条诊断日志：当同一 `ip` 的 datagram 与 stream 会话来自**同一个
   remote 地址**时，打 `单连接双通道 ip=... remote=...`，便于确认新形态生效。

### 3.4 兼容性

| 组合 | 行为 |
|---|---|
| 新客户端 + 新节点 | 单连接双通道（目标形态） |
| 新客户端 + 旧节点 | 仍可用：旧节点同样接受同一连接上的两个请求 |
| 旧客户端 + 新节点 | 仍可用：旧客户端是两条连接，`sessionSet` 按 kind 分别注册 |

**线缆协议零变更**，不需要版本协商。

### 3.5 验收标准

| 指标 | 通过条件 |
|---|---|
| 客户端 `path_udp_sent` vs 节点 `DATAGRAM tx` | 差距 ≤ 1% |
| 质量采样成功率 | ≥ 95%（基线 47%） |
| 节点 `dropped` | 保持 0 |
| `quic_lost` | 不高于当前 0.46% |
| DNS 解析在大流量下的 P95 | 明显低于纯可靠流模式 |
| 连接建立 | 一次成功（3.3 已达成，不得回退） |

### 3.6 风险与回退

| 风险 | 应对 |
|---|---|
| `http3.Transport` 拥有连接，误关会连带断隧道 | 明确关闭顺序，单测覆盖 |
| `GET /` 响应体没读干导致流泄漏 | `auth` 里 `io.Copy(io.Discard, resp.Body)` 后关闭 |
| 单连接后 DATAGRAM 与流抢 cwnd，大流量时 UDP 被压 | 这是**预期**行为（比静默丢弃好）；如需隔离再议多连接 |
| 改坏导致连不上 | 面板 `vpn_datagram_enabled` 一键回退到纯可靠流；客户端与节点均可独立回退 |

---

## 4. 实施步骤

| 步骤 | 内容 | 验证 |
|---|---|---|
| 1 | 新增 `tunnel_session.go`，实现单连接 + 复用鉴权 | 单元测试：两次请求命中同一 conn |
| 2 | `ui.go` / `main.go` 切换到新路径 | 能连上，日志出现「单连接双通道」 |
| 3 | 节点加诊断日志 | 节点日志确认同 remote |
| 4 | 采样改用 `dualConn`，跑 2 分钟 | `path_udp_sent` ≈ 节点 `tx` |
| 5 | 大流量下跑 DNS 压测 | P95 明显下降 |
| 6 | 发版 3.4 / Agent 3.3 | 全量验收 |

工作量：**0.5~1 天**（含测试）。

---

## 5. 与测速波动的关系（不在本方案范围）

本次采样已证明：**隧道零丢包**（`quic_lost=0`），吞吐 20 秒内从 4.76 单调
上升到 11.36 Mbps 且仍在上升。这是远端服务器 TCP 慢启动，叠加测速时间过短。

- 不是隧道限速：瞬时出现过 >100 Mbps
- 正确测法：单连接连续 60 秒，或 4 条并发对比

结论：**测速波动不需要改代码**，先把 DATAGRAM 修好，再用 60 秒稳态读数评估
是否需要引入 BBR / 多流分片。

---

## 6. 明确不做

| 项 | 原因 |
|---|---|
| 用保活包维持 cwnd | 治标，且掩盖了「连接已死无感知」这个真问题 |
| 为 DATAGRAM 单独调大初始窗口 | quic-go 未开放该参数 |
| 本期改用 RFC 9484 CONNECT-IP | 需要重写数据面与鉴权，收益与风险不匹配 |