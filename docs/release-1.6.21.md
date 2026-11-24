# ProxyPilot 1.6.21 发布说明（sing-box 通配证书改造）

日期：2026-11-24
版本：面板 V1.6.21 / Agent 1.4.2 / VPN 客户端 1.4.1

## 目标

将需要证书的 sing-box 入站从自签证书改为管理后台申请的 `*.yzfn.top` 通配证书，客户端不再使用 `insecure=1`。

## 已完成

### 1. 证书下发到 sing-box

- 证书同时写入：
  - `/etc/proxypilot/tunnel/tls/cert.pem` / `key.pem`
  - `/etc/sing-box/cert.crt` / `private.key`
- 原子写入，cert 0644 / key 0600；
- 写入后校验证书与私钥匹配；
- sing-box 服务 active 时重启，服务不存在时跳过；
- 新增 `GET /api/vpn/cert/nodes`，返回节点 TLS 域名、sing-box 服务状态与证书状态。

### 2. sing-box TLS 使用节点域名

- AnyTLS / Hysteria2 / TUIC v5 / VLESS-WS 直连 / MASQUE 入站增加：
  - `tls.server_name = 节点域名`（如 `hk1-node.yzfn.top`）；
- per-user 入站同步增加 `server_name`；
- 客户端链接：
  - AnyTLS / Hysteria2 / TUIC 移除 `insecure=1` / `allow_insecure=1`；
  - SNI 改为节点域名；
  - VLESS-WS 保持节点域名，无 insecure；
- 节点域名为空时不再生成假安全 TLS 链接。

### 3. 实际证书与节点验证

- 重新签发 `yzfn.top,*.yzfn.top`（Let's Encrypt，Cloudflare DNS-01）：
  - 新指纹 `34486A183FDF7144FAF0378A75D2694BAE814D5BE1EE63C4DAD23F9B3A307DA7`；
  - 有效期至 2027-02-22；
- 已验证 TLS 握手：
  - `hk1-node.yzfn.top:13703` → CN=yzfn.top，Verification OK；
  - `changhua-node.yzfn.top:13703` → OK；
  - `de1-node.yzfn.top:13703` → OK；
  - `lax1-node.yzfn.top:13703` → OK；
  - `hk2-node.yzfn.top:13703` → OK；
- 节点证书文件已确认：
  - cert 4800 字节；
  - key 227 字节；
  - `KEY_OK`、CN=yzfn.top；
- 订阅链接已改为节点域名 + 可信证书。

### 4. 管理 UI

- 证书管理页：
  - 明确通配目标 `yzfn.top,*.yzfn.top`；
  - 展示 sing-box 证书路径；
  - 新增「节点证书同步」表；
- 节点页：
  - 增加 TLS 域名说明（`*.yzfn.top` 一级子域）；
  - AnyTLS / HY2 / TUIC / VLESS-WS 增加「需可信证书」标识。

## 已知问题

1. 4 台节点证书任务仍 pending（节点离线/重启中），恢复后自动执行；
2. `lax1-node.yzfn.top` 被两个 sing-box 节点共用，需拆分域名（如 `lax2-node.yzfn.top`）；
3. 本机面板未运行 `proxypilot-agent`，证书下发结果中本机 VPN 一项显示失败，不影响面板与远端节点；
4. 一键下发批量任务当前只反映任务是否创建，不反映逐节点部署失败，后续需修正统计口径；
5. 旧自签 insecure 链接不再兼容，客户端需重新获取订阅。

## 验证

- server 测试全绿；
- web-antd 构建，render-check 38/0；
- 面板 healthz V1.6.21；
- TLS 握手与证书链验证通过；
- 11/15 节点证书已写入，剩余 4 台离线/排队；
- 全量发布 dist-release 更新 V1.6.21。

## 后续

- 补做证书自动续期（到期前 30 天）；
- 拆分重复节点域名并补 DNS；
- 修复一键下发逐节点状态；
- 增加节点证书状态回采（目前 UI 为 unknown）；
- 处理离线节点的证书重试。