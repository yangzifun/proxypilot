# 协议实现详解

> 面向开发者的字节级说明。产品视角的章节见 [readme](../readme.md#vpn-与组网协议栈)。
> 本文所有长度、字段偏移均与当前实现（面板 V35.3 / Agent 2.1.1 / 客户端 2.7）一致。

## 1. 三个网络平面的区分

| 平面 | 用途 | 协议 | 端口 |
|---|---|---|---|
| 控制面 | 客户端 ↔ 面板（换票、节点列表） | HTTPS（HTTP/1.1、HTTP/2） | 443 |
| 数据面（VPN） | 客户端 ↔ 节点（用户上网流量） | HTTP/3 over QUIC | **UDP 443** |
| 管理面（组网） | 服务器 ↔ 中心（运维内网） | 同数据面 | UDP 443 |

**注意**：面板的 443 是 TCP（给浏览器与客户端换票），节点的 443 是 UDP（QUIC）。
两者可以同域名不同记录，但客户端连接节点的地址通常是独立子域（如 `hk2-node.yzfn.top`）。

## 2. 连接建立时序

```
客户端                          面板                        节点
  │                              │                           │
  │ ① POST /api/portal/vpn/connect│                           │
  │   {device_id, node_id}       │                           │
  │ ────────────────────────────▶│ 校验授权/配额/并发         │
  │                              │ 分配 IP、签发 JWT          │
  │ ◀── token + ip4 + mtu + dns ─│                           │
  │                              │                           │
  │ ② QUIC 握手（TLS 1.3，ALPN=h3，SNI=节点域名）              │
  │ ═══════════════════════════════════════════════════════▶ │
  │ ◀═══════════════════════════════════════════════════════ │
  │                              │                           │
  │ ③ GET /  Authorization: Bearer <JWT>                     │
  │ ═══════════════════════════════════════════════════════▶ │ 验签
  │ ◀──── 200  x-vpn-auth: Success                          │ 注册会话
  │                              │                           │
  │ ④ POST /tunnel  Authorization: Bearer <JWT>              │
  │ ══ 请求体：[2B len][IP]… ══════════════════════════════▶ │ 写 TUN → NAT
  │ ◀═ 响应体：[2B len][IP]… ═══════════════════════════════ │ 回程
  │                              │                           │
```

耗时参考（香港节点，RTT 119ms）：

| 步骤 | 1-RTT 首连 | 会话复用 |
|---|---|---|
| ① 换票 | 2 RTT（TCP+TLS+HTTP） | 2 RTT |
| ② QUIC 握手 | 1 RTT | 0-RTT 可选 |
| ③ 鉴权 | 1 RTT | 1 RTT |
| ④ 建流 | 1 RTT | 1 RTT |

## 3. 隧道帧格式

### 3.1 帧结构

```
偏移  长度  内容
0     2     Length（大端 uint16）：后续 IP 报文的字节数
2     N     IP 报文（IPv4，N = Length）
```

- 单帧上限 **4096**（`maxFrame`），超限直接断流
- 帧与帧之间无分隔符，严格按 Length 切分
- 无校验和：依赖 QUIC AEAD + IP/TCP 自带校验

### 3.2 实际抓到的十六进制（ping 1.1.1.1）

```
0000  00 3C                                            Length = 60
0002  45 00 00 3C                                      IPv4：v4/IHL5, TOS0, 总长60
0006  1A 2B 00 00                                      ID=0x1A2B, Flags=0, Frag=0
000a  40 01 F2 9E                                      TTL=64, Proto=1(ICMP), Checksum
000e  0A 58 01 0C                                      Src = 10.88.1.12
0012  01 01 01 01                                      Dst = 1.1.1.1
0016  08 00 4D 5A                                      ICMP Type8/Code0/Checksum
001a  00 01 00 01                                      Identifier=1, Sequence=1
001e  ... 32 字节数据 ...                              Echo 负载
```

### 3.3 封装开销

| 层 | 增加字节 | 说明 |
|---|---|---|
| 隧道帧头 | +2 | Length |
| HTTP/3 DATA 帧 | +3 | 帧类型 + varint 长度 |
| QUIC STREAM 帧 | +6 | 类型 + 流 ID + 偏移 + 长度 |
| QUIC 包 | +27 | 短包头 1 + DCID 8 + PN 2 + AEAD 16 |
| UDP | +8 | 端口/长度/校验 |
| IP | +20 | IPv4 头 |

60 字节 ICMP → 上线 **126 字节**，开销 110%；
1280 字节满包 → 1346 字节，开销约 5%。**这是小包场景吞吐差的根本原因。**

## 4. HTTP/3 报文

### 4.1 鉴权请求

```
:method: GET
:scheme: https
:authority: hk2-node.yzfn.top:443
:path: /
authorization: Bearer eyJhbGciOiJFZERTQSIsInR5cCI6IkpXVCJ9...
user-agent: ProxyPilot/2.7
```

响应：

```
:status: 200
x-vpn-auth: Success
x-vpn-assigned-ipv4: 10.88.2.10
cache-control: no-store
```

### 4.2 数据面请求

```
:method: POST
:scheme: https
:authority: hk2-node.yzfn.top:443
:path: /tunnel
authorization: Bearer <同一 JWT>
content-type: application/octet-stream
```

响应头先返回（随后开始双向流）：

```
:status: 200
x-vpn-auth: Success
x-vpn-assigned-ipv4: 10.88.2.10
cache-control: no-store
content-type: application/octet-stream
```

无 `content-length`，靠流 FIN 结束。

### 4.3 错误码

| 场景 | 状态码 | 响应体 |
|---|---|---|
| 缺 Bearer | 401 | `unauthorized` |
| JWT 签名/过期 | 401 | `unauthorized` |
| JWT 的 node 不匹配 | 403 | `wrong node` |
| ip4 不在客户端子网 | 403 | `ip not in client cidr` |
| 同一 IP 已有会话 | 409 | `ip already in use` |
| 非 GET/POST | 405 | `method not allowed` |

## 5. QUIC 参数

| 参数 | 客户端 | 节点 | 备注 |
|---|---|---|---|
| ALPN | `h3` | `h3` | 必须一致 |
| KeepAlivePeriod | 10s | 15s | 防 NAT 超时 |
| MaxIdleTimeout | 120s | 120s | 双方取小 |
| HandshakeIdleTimeout | 默认 | 10s | |
| EnableDatagrams | — | true | 历史字段，数据面不用 |
| 流控窗口 | 默认 | 默认 | 见下 |

**吞吐上限公式**

```
单流吞吐 ≈ 接收窗口 / RTT
连接吞吐 ≈ 连接窗口 / RTT
```

实测：

| 节点 | RTT | 512KB 窗口下 | 换成 4MB 窗口后（理论） |
|---|---|---|---|
| 台北 | 376ms | 10.9 Mbps | 87 Mbps |
| 香港 | 119ms | 34.4 Mbps | 270 Mbps |

## 6. 会话与隔离

节点侧会话表：

```go
sessions map[string]*session   // key = 客户端 IPv4
type session struct {
    jti, ip, device, grantID
    send    func([]byte) error   // 注入的发送函数（流或 DATAGRAM）
    closeFn func()
    rx, tx  uint64
}
```

**隔离规则**（`tunLoop`）：

```go
// 仅当源与目的都是本节点客户端，且都不是网关时才丢弃
if srcClient && dstClient && !touchesGateway && !AllowClientToClient {
    drop
}
// 公网回包、网关回包一律放行
```

**源地址校验**：`src.Equal(sess.ip)`，不符计入 `bad_src` 并丢弃（防伪造）。

## 7. JWT

### 7.1 头部

```json
{ "alg": "EdDSA", "typ": "JWT" }
```

### 7.2 载荷

| 字段 | 类型 | 说明 |
|---|---|---|
| `iss` | string | 固定 `proxypilot` |
| `aud` | string | 固定 `masque-node` |
| `sub` | string | `device:<id>` |
| `uuid` | string | 用户 UUID |
| `node` | int | 节点 ID，必须与目标节点一致 |
| `grant` | int | 授权记录 ID |
| `ip4` | string | 分配地址 |
| `net` | string | 客户端子网 |
| `iat` / `exp` | int | 秒级时间戳，默认 TTL 1200s |
| `jti` | string | 会话唯一 ID |

### 7.3 校验顺序

```
解析 → 验签（节点公钥） → 检查 exp → 检查 iss/aud → 检查 node → 检查 ip4 属于 client_cidr
```

## 8. 组网（Mesh）

组网复用同一套帧与 HTTP/3 语义，差别在节点配置：

| 配置 | VPN | 组网 |
|---|---|---|
| `disable_nat` | false | **true** |
| `allow_client_to_client` | false | **true** |
| `client_cidr4` | 每节点独立 `10.88.x.0/24` | 统一 `10.99.0.0/16` |
| `gateway_ip4` | `10.88.x.1` | `10.99.0.1` |
| TUN | `ppvpn0` | `ppmesh0` |

心跳：

```
POST /api/portal/vpn/mesh/heartbeat
{ "rtt_ms":157, "transport":"masque", "state":"up",
  "endpoint":"vpncenter.yzfn.top:443", "last_error":"" }
```

## 9. 抓包速查

```bash
# 只看隧道（QUIC 加密，仅能看包长/时序）
tcpdump -ni eth0 'udp port 443' -c 100 -vv

# 看隧道内明文（节点侧）
tcpdump -ni ppvpn0 -c 50 -X

# Wireshark
udp.port == 443 && quic
http3.headers
tls.handshake.extensions_alpn
```

客户端日志关键行：

```
数据面：从网卡读到首个报文（N 字节）
数据面：首个报文已写入隧道流（N 字节）
隧道流量：网卡→隧道 N 包，隧道→网卡 M 包
隧道连通性：正常（N ms）
```

## 10. 已知坑（改协议前必读）

| 坑 | 表现 | 正确做法 |
|---|---|---|
| 用 QUIC DATAGRAM 承载 | 偶发能通，持续流量全丢 | 用可靠流（当前实现） |
| 手工拼 IP 头做探测 | 探测永远失败 | 只发 DNS 消息，让内核补头 |
| 写 TUN 失败就 return | 会话静默死亡 | 记日志后 continue |
| 隔离规则看 src 就丢 | 公网回包被丢 | 只在 C2C 场景丢 |
| 路由用网关做下一跳 | ARP 无法解析 | 用 on-link（0.0.0.0 下一跳） |
| 只给 TUN 配 DNS | Windows 多网卡并行查询导致泄露 | NRPT + 物理网卡 53 封禁 |
