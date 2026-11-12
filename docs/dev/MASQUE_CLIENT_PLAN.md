# WARP MASQUE 客户端可行性方案

> 结论先说：**可以**。VPN 客户端现有的 `quic-go v0.55.0` 已经具备 HTTP/3 扩展 CONNECT 的客户端能力，
> 缺的只是 MASQUE（RFC 9484 connect-ip）的载荷层与地址协商。工作量集中在新增一个包，不需要重写隧道。

- 基线：`48f81ab7`（V1.1）
- 涉及代码：`vpnclient/`（客户端）、`server/internal/api`（面板下发）、`tunnel/`（仅供参考，不复用）
- 状态：方案（未实施）

---

## 1. 现状核查（都有代码依据）

### 1.1 面板侧「WARP」目前只是控制面注册

`server/internal/warp/warp.go`

```
1   // Package warp 负责在控制面注册/删除 Cloudflare WARP(MASQUE) 设备。
31  type Profile struct {
32      DeviceID      string
33      Token         string
35      PrivateKey    string // 客户端 X25519 私钥（base64）
37      PeerPublicKey string // WARP 服务端公钥（base64）
40      Endpoint      string // 注册返回的 WireGuard endpoint，仅参考
```

即：面板只做「注册设备 → 拿 WireGuard 凭据」，数据面仍是 WireGuard 形态，
`masqueWarpDefaultSNI = "zt-masque.cloudflareclient.com"` 只是注册时的 SNI 参数。
**面板目前没有实现 MASQUE 数据面。**

### 1.2 节点侧可以当 MASQUE 服务端，但要求 sing-box ≥ 1.15.0

`server/internal/api/singbox.go:352-375`

```go
if tpl.EnableMasque != 0 {
    endpoints = append(endpoints, map[string]any{
        "type": "masque-server", "tag": "masque-in", "listen": "::", "listen_port": ports["masque"],
        "tls": map[string]any{"enabled": true, "alpn": []string{"h2", "h3"}, ...},
        "users":   u,                        // username=UUID, password=ss_password/UUID（HTTP Basic）
        "path":    "/.well-known/masque/ip/{target}/{ipproto}/",
        "version": []any{1, 2, 3},           // connect-ip draft 版本
    })
}
```

`server/internal/api/singbox_bundle.go:118` 明确拒绝低版本：

```
MASQUE 需要 sing-box >= 1.15.0（masque-server endpoint），当前节点是 <ver>
```

**实测阻断点**：组件分发基址里当前发布的是 **1.14.4**，其 `protocol/` 下没有 `masque` 目录
（已核对 `sing-box@v1.14.4` 源码）。所以「节点侧 MASQUE 服务端」这条路要先补 1.15+ 的二进制。

### 1.3 客户端已有实现 MASQUE 所需的全部底层能力

`vpnclient/go.mod`

```
github.com/quic-go/quic-go v0.55.0
golang.zx2c4.com/wintun / github.com/songgao/water   （TUN 设备）
```

`quic-go@v0.55.0/http3/client.go`

```
if isExtendedConnectRequest(req) {
    if !c.conn.Settings().EnableExtendedConnect {
```

即 HTTP/3 扩展 CONNECT（`SETTINGS_ENABLE_CONNECT_PROTOCOL` + `:protocol`）**客户端已支持**，
这正是 MASQUE connect-ip 的握手基础。

客户端还已具备：TUN 生命周期（wintun/water）、路由下发与还原（`route_*.go`）、
DNS 守卫（`dns_guard_*.go`）、断线清理（`cleanup_*.go`）、绕过路由（`bypass_route_*.go`）。
这些都是 MASQUE 客户端同样需要的，可直接复用。

### 1.4 结论

| 能力 | 现状 | 结论 |
|---|---|---|
| HTTP/3 + 扩展 CONNECT（客户端） | quic-go v0.55.0 已支持 | ✅ 可用 |
| TUN / 路由 / DNS 守卫 | 已有 | ✅ 复用 |
| MASQUE 载荷（capsule + IP 包） | 无 | ❌ 需新增 |
| 地址/路由协商（ADDRESS_ASSIGN 等） | 无 | ❌ 需新增 |
| 节点侧 MASQUE 服务端 | sing-box 1.15+ 才支持，当前桶里是 1.14.4 | ⚠️ 需补二进制 |
| Cloudflare WARP 边缘的 MASQUE 鉴权 | 未验证（面板只注册 WireGuard 设备） | ⚠️ 需实测 |

---

## 2. 两种落地目标

### 目标 A：客户端连「自己的节点」（推荐先做）

```
客户端 --HTTP/3 connect-ip--> 节点 sing-box masque-server --direct--> 目标
```

优点：服务端完全可控、鉴权用现成的 UUID/密码、可单机灰度、不依赖 Cloudflare。
缺点：要求节点 sing-box ≥ 1.15.0，且要发布到组件分发基址。

### 目标 B：客户端直连 Cloudflare WARP 边缘（真正的「WARP MASQUE」）

```
客户端 --HTTP/3 connect-ip--> zt-masque.cloudflareclient.com:443 --CF 网络--> 目标
```

优点：出口是 Cloudflare，抗封锁更强，不需要自建节点。
缺点：鉴权与地址分配流程依赖未公开接口（面板的 `warp` 包只覆盖注册部分），要先抓包验证；
另外 Cloudflare 对 MASQUE 的客户端身份校验方式可能随版本变化。

**建议：先做 A（可控、可验证），把同一套客户端代码在 B 上复用，只换连接参数与鉴权。**

---

## 3. 技术方案

### 3.1 新增包 `vpnclient/internal/masque`

| 文件 | 职责 |
|---|---|
| `client.go` | 建 HTTP/3 连接、发扩展 CONNECT（`:protocol=connect-ip`）、拿到双向流 |
| `capsule.go` | RFC 9297 capsule 编解码：DATAGRAM(0x00)、ADDRESS_ASSIGN(0x01)、ADDRESS_REQUEST(0x02)、ROUTE_ADVERTISEMENT(0x03) |
| `connectip.go` | connect-ip 载荷：4 字节上下文 ID + IP 包（IPv4/IPv6 双栈） |
| `assign.go` | 解析 ADDRESS_ASSIGN / ROUTE_ADVERTISEMENT，产出「本端地址 + 路由表」 |
| `conn.go` | 实现 `net.PacketConn` 形态，供上层 TUN 读写循环复用现有代码 |

关键接口（与现有 TUN 循环对接）：

```go
type Session interface {
    // LocalAddr / Routes 来自 ADDRESS_ASSIGN 与 ROUTE_ADVERTISEMENT
    LocalAddrs() []*net.IPNet
    Routes() []*net.IPNet
    // ReadPacket/WritePacket 与现有 tunnel 的包接口一致，直接喂给 TUN
    ReadPacket(p []byte) (int, error)
    WritePacket(p []byte) error
    Close() error
}
```

### 3.2 握手流程（connect-ip）

```
1. QUIC 建连（ALPN h3），SNI = 节点域名（目标 A）或 zt-masque.cloudflareclient.com（目标 B）
2. 发 CONNECT 请求：
     :method = CONNECT
     :protocol = connect-ip
     :scheme = https
     :authority = <host>
     :path = /.well-known/masque/ip/*/*/        （sing-box 默认 path 模板）
     capsule-protocol: ?1                        （RFC 9297）
     authorization: Basic base64(user:pass)      （目标 A 用授权 UUID/密码）
3. 服务端 2xx → 该请求流即 IP 载荷通道
4. 客户端发 ADDRESS_REQUEST（想要的地址，可为空表示「请分配」）
5. 服务端回 ADDRESS_ASSIGN（本端地址/前缀）+ ROUTE_ADVERTISEMENT（可用路由）
6. 之后：DATAGRAM capsule 承载 IP 包（4 字节 context id + IP 包）
```

版本协商：sing-box 的 `version: [1,2,3]` 指 connect-ip 草案版本，
客户端需在握手时按服务端选择回落（草案 1/2/3 的 capsule 类型号与地址格式有差异，
实现时以 `draft-ietf-masque-connect-ip-03` 为主，其余按需兼容）。

### 3.3 与现有客户端的集成点

| 现有位置 | 改动 |
|---|---|
| `vpnclient/ui.go` 的连接/断开序列 | 增加 `Mode = "masque"` 分支：建 MASQUE 会话替代现有 QUIC 隧道 |
| `vpnclient/tunnel.go` 的 TUN 读写循环 | 抽象出 `packetSource` 接口，MASQUE 与现有隧道都实现它 |
| `vpnclient/route_*.go` | 路由表来源从「节点分配的段」改为 `Session.Routes()` |
| `vpnclient/dns_guard_*.go` | 不变（仍然把 DNS 指向节点下发的解析器） |
| `vpnclient/main.go:208 authHTTP3` | 抽取「HTTP/3 建连」公共部分，供 MASQUE 复用 |
| P0 修复后的 `cleanupTun` | MASQUE 模式断开也必须走同一条清理路径（这次审计发现的自断黑洞问题，新路径要一并覆盖） |

### 3.4 面板侧改动（目标 A 必需）

1. **发布 sing-box ≥ 1.15.0 到组件分发基址**：
   `singbox/sing-box-1.15.x-linux-{amd64,arm64}.tar.gz` + 更新 `singbox/manifest.json`
   （面板已按 manifest 里的版本来下发，见 V1.1）
2. **节点配置里带上 MASQUE 入站**：模板 `enable_masque=1` 后，`singbox.go:352` 会渲染
   `masque-in` endpoint（端口 = 端口表里的 `masque`，当前 base+8 = 13708）
3. **用户订阅里输出 MASQUE 连接参数**：host / port / path / username / password / ALPN，
   与现有 vless/hy2 链接并列（前端加一个「MASQUE」协议卡片即可）
4. **客户端配置结构**：`vpnclient` 的 profile 增加 `masque: {host, port, path, user, pass, sni, insecure}`

---

## 4. 实施计划（建议分四步，可独立验收）

| 阶段 | 内容 | 产出 | 预估 |
|---|---|---|---|
| P1 | 发布 sing-box 1.15+ 到组件基址；节点启用 masque-server 并用 `curl --http3` 完成一次 connect-ip 冒烟 | 节点能当 MASQUE 服务端 | 0.5 天 |
| P2 | 客户端新增 `internal/masque`（capsule + connect-ip + 地址协商），单测覆盖编解码与分配 | 库可用，带单测 | 2 天 |
| P3 | 接入客户端：`Mode=masque`、TUN 复用、路由/DNS 复用、断开清理（含自断路径） | 可连自家节点的 MASQUE 客户端 | 2 天 |
| P4 | 目标 B：验证 Cloudflare WARP 边缘的鉴权与地址分配（抓包 + 实测），必要时补设备 JWT 流程 | WARP MASQUE 直连 | 1~3 天（含不确定） |

### 验收标准

- P2：`go test ./internal/masque/...` 覆盖 capsule 往返、地址分配解析、双栈包
- P3：单节点 e2e —— 客户端连上后 `curl -4 ifconfig.me` 走对出口；断连后 `route print`（或 `ip route`）无残留；重复连接 20 次无句柄泄漏
- P4：确认 WARP 边缘是否接受面板注册的设备凭据；确认地址分配（100.96.x 之外的新网段）

---

## 5. 风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| 组件基址没有 sing-box 1.15+ | 目标 A 无法起服务端 | P1 先补二进制；本地模块缓存有 1.14.4 但无 masque，需拉 1.15 源码构建 |
| Go 模块代理不可达（本机实测 proxy.golang.org 超时） | 无法新引依赖 | 客户端方案不引入新依赖（quic-go 已在依赖里）；sing-box 构建走面板机或预先 vendor |
| connect-ip 草案版本差异（1/2/3） | 与不同 sing-box 版本不兼容 | 握手按服务端返回的版本回落；先只支持 03，其余报错提示 |
| Cloudflare WARP 鉴权未公开 | 目标 B 可能做不成 | 先抓 WARP 客户端流量确认 header；失败则目标 B 降级为「不支持」 |
| MASQUE 只有 UDP/IP 语义（connect-ip 是 IP 级） | 需要完整 IP 栈（不是 TCP 代理） | 客户端已有 TUN，天然满足；注意 MTU（capsule 开销，建议 1280） |
| 断开清理遗漏导致路由残留 | 整机断网（历史事故） | 复用修复后的 `cleanupTun`，并在 MASQUE 路径加同样的断线自愈 + 单测 |

---

## 6. 与现有方案的关系

- 现有 `tunnel/`（自研 HTTP/3 隧道 + DATAGRAM + FEC）继续作为主通道，MASQUE 是**第二条可选通道**；
- 两者在客户端共用 TUN / 路由 / DNS 守卫，只是「包从哪来」不同（见 3.3 的 `packetSource` 抽象）；
- MASQUE 的优势是：协议标准、无自研载荷、可直连 Cloudflare 出口；
  劣势是：需要 sing-box 1.15+ 或 CF 凭据、每次升级要跟草案版本。

---

## 7. 需要你确认的三件事

1. **先做目标 A（自建节点 MASQUE）还是直接冲目标 B（Cloudflare WARP）？** 我建议 A。
2. **是否允许把 sing-box 升到 1.15+ 并发布到组件基址？** 这会替换所有节点的 sing-box 版本，需要一次灰度。
3. **MASQUE 作为「可选第二条通道」还是「替代现有隧道」？** 我建议先并存，稳定后再决定。