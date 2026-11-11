# 参考 MASQUE 重做 DATAGRAM 通道 —— 方案

> 目标：把现在「裸发 DATAGRAM、没人管」的 UDP 通道，改造成
> RFC 9484(CONNECT-IP) / RFC 9297(HTTP Datagrams) 那一套：
> **Datagram 必须绑定到具体请求（Context ID），必须协商，必须有降级路径**。
>
> 本文只谈设计与取舍，不含实现。

---

## 1. 先说结论：我们现在缺的不是"开开关"，是三个协议要素

对照 MASQUE 的四步，逐项打分：

| MASQUE 要素 | 标准做法 | 我们现在 | 后果 |
|---|---|---|---|
| **控制通道** | `CONNECT` + `:protocol=connect-udp/connect-ip` + `capsule-protocol: ?1` | 普通 `POST /tunnel`，没有 `:protocol`，没有 `capsule-protocol` | 对端无法声明"我支持 Datagram 封装" |
| **通道绑定** | DATAGRAM 载荷前置 **Context ID**（= 所属 Stream 的 quarter stream ID） | 载荷就是裸 IP 报文，**没有任何 ID** | 连接上有多条流/多个上下文时无法归属；连接级而非请求级 |
| **数据报封装** | `[Context ID varint][原始 UDP/IP 报文]` | `[原始 IP 报文]` | 节点只能靠连接对象猜属于谁 |
| **能力协商/降级** | 协商失败 → 改用 **DATAGRAM Capsule** 走可靠流 | 无协商；发送失败只在客户端回退，**节点侧不回退** | 一侧不支持就静默丢包（我们踩过三次） |

**这就是为什么我们的 DATAGRAM「通了但时好时坏」**：
它只是一条挂在连接上的、没有归属信息的不可靠通道，
既不知道属于哪个请求，也没有"不可用就改走流"的兜底。

---

## 2. 目标架构（RFC 9484 风格，保留自研鉴权）

我们代理的是 **IP 包（L3）**，所以对标的不是 RFC 9298(CONNECT-UDP)，
而是 **RFC 9484(CONNECT-IP)**：一条请求 = 一个 IP 隧道上下文。

```
一条 HTTP/3 连接
 └─ 一条双向流：POST /tunnel（控制流，等价于 CONNECT-IP）
      · 请求头：Authorization: Bearer <JWT>
                Capsule-Protocol: ?1          ← 新增：声明支持 capsule/datagram 封装
                Connect-Protocol: connect-ip  ← 新增：语义声明（可选，便于识别）
      · 响应头：Capsule-Protocol: ?1
                H3-Datagram: ?1               ← 新增：确认对端支持 HTTP Datagram
      · 这条流承载：
          ① 可靠的 IP 报文帧（现有 [2B len][IP] 格式，保留）
          ② Capsule 帧（降级时承载 IP 报文）
      · Context ID = 这条流的 quarter stream ID（RFC 9297 规定）

同一连接的 DATAGRAM 帧
      · 载荷 = [Context ID varint][IP 报文]
      · 节点按 Context ID 找到对应会话 → 写 TUN
```

### 为什么用 quarter stream ID 而不是自定义编号

RFC 9297 规定 HTTP Datagram 的 Context ID 就是所属请求流的
`stream_id / 4`（client-initiated bidirectional 流的低位是 0b00，
所以除以 4 得到连续的小整数）。好处：

- 不需要额外的 ID 分配与回收逻辑
- 一条连接上可以并行多条隧道请求（未来多流分片时天然支持）
- 与标准实现语义一致，将来接真 MASQUE 不用重写

---

## 3. 帧格式

### 3.1 可靠流上的帧（保持兼容）

```
[2B 大端长度][IP 报文]
```

现有格式不动，旧客户端/旧节点继续可用。

### 3.2 可靠流上的 Capsule 帧（新增，降级用）

按 RFC 9297 的 Capsule 结构：

```
Capsule = [varint 长度][varint 类型][载荷]
类型 0x00 = DATAGRAM Capsule，载荷 = [varint Context ID][IP 报文]
```

为了不与现有 `[2B len]` 帧冲突，用一个**保留长度值**做区分：
长度字段为 `0xFFFF` 时，表示"后面跟一个 Capsule"。
（IPv4/IPv6 报文不可能达到 64KB，MTU 1380，天然安全。）

### 3.3 DATAGRAM 帧（新增 Context ID）

```
[varint Context ID][IP 报文]
```

Context ID 通常 1 字节（stream_id ≤ 252 时），开销可忽略。

---

## 4. 能力协商与降级

### 4.1 协商

| 环节 | 判断依据 |
|---|---|
| 客户端是否可发 DATAGRAM | 连接的 `ConnectionState().SupportsDatagrams` |
| 节点是否可收发 DATAGRAM | 响应头 `H3-Datagram: ?1` |
| 是否支持 Capsule | 双方都有 `Capsule-Protocol: ?1` |

三者齐全 → 走 DATAGRAM；
任一缺失 → 全部走可靠流（Capsule 或现有帧），**不丢包**。

### 4.2 三档降级

```
① DATAGRAM（首选，不可靠、低延迟）
      ↓ 对端不支持 / 发送连续失败
② DATAGRAM Capsule over 流（可靠，但有队头阻塞）
      ↓ 连 Capsule 都不支持（旧版本）
③ 现有 [2B len][IP] 帧（纯可靠流）
```

**关键点：降级必须在两端都成立。** 现在只有客户端做回退，
节点 `SendDatagram` 失败就直接丢弃 —— 这正是「下行归零」的成因。
新的设计里，节点侧同样维护"该会话当前可用通道"，
发送失败立刻改走 Capsule/流，并记录一次降级。

### 4.3 与 MASQUE 文档里 GOST 的 `enableDatagrams` 对应

我们两端都需要显式开启：

```
客户端：quic.Config.EnableDatagrams = true        （已在 3.7 补上）
        http3.Transport.EnableDatagrams = true    （已在 3.7 补上）
节点：  quic.Config.EnableDatagrams = true        （已有）
        http3.Server.EnableDatagrams = true       ← **缺失，必须补**
```

已核对 quic-go v0.55 源码（http3/server.go:137-139）：

```go
// EnableDatagrams enables support for HTTP/3 datagrams (RFC 9297).
// If set to true, QUICConfig.EnableDatagrams will be set.
EnableDatagrams bool
```

`http3.Server` 需要显式设置这个字段，否则 HTTP/3 层不会协商
`SETTINGS_H3_DATAGRAM`，客户端的 `ReceiveDatagram` 仍然不可用 ——
这与 GOST 配置里必须写 `enableDatagrams: true` 是同一件事。

这次事故的直接原因就是客户端隧道流那条连接漏了这两项，
`ReceiveDatagram` 立刻返回 `datagram support disabled`。

---

## 5. 分流策略（与 MASQUE 一致的分层）

MASQUE 的意义是「让 UDP 走 UDP」，但不是所有 UDP 都该走：

| 流量 | 通道 | 依据 |
|---|---|---|
| **DNS（UDP 53）** | **可靠流** | 最怕丢；2 秒超时对用户是灾难。实测走 DATAGRAM 时 nslookup 要重试 2~3 次 |
| 其他 UDP 小包（< 512B） | DATAGRAM + Capsule 降级 | 实时性优先，丢了上层能重试 |
| 大 UDP | 可靠流 | 吞吐优先，队头阻塞影响小 |
| TCP / ICMP | 可靠流 | 不变 |

这条规则把「MASQUE 的收益」和「DNS 的可靠性」解耦，
是本次修复里性价比最高的一步。

---

## 6. 代码落点

| 位置 | 改动 |
|---|---|
| `vpnclient/stream_dial.go` | 请求头加 `Capsule-Protocol: ?1`、`Connect-Protocol: connect-ip`；解析响应头 `H3-Datagram`；计算并保存 `contextID = streamID/4` |
| `vpnclient/dual_tunnel.go` | DATAGRAM 收发加/去 Context ID；连续失败 N 次降级到 Capsule |
| `vpnclient/stream_tunnel.go` | 增加 Capsule 帧编解码（`0xFFFF` 长度标记） |
| `tunnel/node/stream.go` | 读流时识别 Capsule 帧；响应头回 `Capsule-Protocol`/`H3-Datagram`；把 `contextID → session` 注册进会话表 |
| `tunnel/node/server.go` | `readSession` 收到 DATAGRAM 后按 Context ID 查会话（而不是"这条连接上的唯一会话"）；`tunLoop` 发送失败改走 Capsule |
| `tunnel/node/config.go` | 新增 `datagram` 开关的语义：关闭时不注册上下文，全部走流 |

---

## 7. 实施阶段

| 阶段 | 内容 | 验收 |
|---|---|---|
| 1 | 加 `Capsule-Protocol` / `H3-Datagram` 协商头 + Context ID 编解码 | 双向抓包可见 `[ctx][ip]`；节点能按 ctx 找到会话 |
| 2 | 节点侧降级：`SendDatagram` 失败改走 Capsule | 人为关闭对端 datagram，连接仍通 |
| 3 | 分流规则：DNS 强制可靠流 | 开启 DATAGRAM 后 `nslookup` 一次成功、无超时 |
| 4 | 面板开关与能力协商联动：节点不支持时拒绝开启并提示 | 面板显示"节点不支持 DATAGRAM" |
| 5（可选） | 多流：每类流量一条 Context | 与 HO 优化合并做 |

阶段 1~3 是让 DATAGRAM「能用」的最小集合；4 防止误开；5 是性能优化。

---

## 8. 验收标准

| 指标 | 通过条件 |
|---|---|
| `nslookup`（DATAGRAM 开） | 一次成功，无 `timeout was 2 seconds` |
| 客户端 `path_udp_sent` vs 节点 `DATAGRAM tx` | 差距 ≤ 1% |
| `path_udp_recv` | > 0 且与节点 `DATAGRAM rx` 同量级 |
| 对端不支持时 | 自动降级，流量不丢，日志有明确提示 |
| 节点发送失败时 | 改走 Capsule，下行不归零 |

---

## 9. 风险与回退

| 风险 | 对策 |
|---|---|
| Context ID 计算错误导致串流 | 用 quarter stream ID（规范定义），单测覆盖 stream_id=0/4/8 |
| Capsule 与现有帧冲突 | 用 `0xFFFF` 保留长度值区分，MTU 1380 不可能撞上 |
| 改数据面再次导致断网 | 每一步都可回退到「全走可靠流」；面板开关保留为总闸 |
| 节点/客户端版本不一致 | 协商头缺失即降级到现有 `[2B len]` 帧，新旧互通 |

---

## 10. 明确不做

| 项 | 原因 |
|---|---|
| 完整 RFC 9298 CONNECT-UDP（每条 UDP 流一个 tunnel） | 我们是 L3 隧道，一个上下文即可；逐流会增加数万倍请求 |
| 完整 RFC 9484 地址分配/路由通告（capsule 里的 ADDRESS_ASSIGN） | 地址仍由面板下发，无需在隧道内协商 |
| 与第三方 MASQUE 客户端互通 | 需要完整实现 CONNECT-IP 语义，本期不做；只借其封装模型 |

---

## 11. 与现有事故的对应关系

| 历史故障 | 本方案如何避免 |
|---|---|
| `datagram support disabled`（3.4~3.6 收不到 UDP） | §4.3 两端显式开启 + §4.1 协商确认 |
| FEC 开启后 UDP 全丢（节点不认 FEC 帧） | §3.2 用 Capsule 承载，格式由协商决定，不再"客户端单方面加头" |
| 关掉开关反而断网（节点仍发 DATAGRAM） | §4.3 面板开关两端同一个值；关闭时不注册上下文 |
| 下行归零（节点发送失败直接丢） | §4.2 节点侧同样降级 |
| DNS 超时 2~6 秒 | §5 DNS 强制可靠流 |