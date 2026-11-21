# ProxyPilot 1.5.4 发布说明（vless-ws-cdn 支持公网监听）

日期：2026-11-21
版本：面板 V1.5.4 / Agent 1.4.1 / VPN 客户端 1.4.1

## 变更

- `vless-ws-cdn` 入站不再固定 `listen: 127.0.0.1`：
  - 模板 sb.json 导入时记录 CDN 入站的 listen 到 `protocol_options.vlws.cdn_listen`；
  - 生成配置时读取该值，允许 `127.0.0.1`、`0.0.0.0`、`::`（`[::]` 归一化为 `::`），非法值回退 `127.0.0.1`；
  - 旧模板没有该字段时保持 `127.0.0.1`，Cloudflare Tunnel 行为不变；
  - `cdn_port` 逻辑不变，默认仍为 `port_base+7`（13707）。
- 模板编辑器对 tag 含 `-cdn` 的入站增加提示：
  - `127.0.0.1` 仅 Tunnel；
  - `0.0.0.0`/`::` 允许 ESA/EO 等 CDN 公网回源；
  - 需在节点防火墙/云安全组放行 CDN 回源端口；
  - 公网回源为明文 HTTP+WS；如 CDN 强制 HTTPS 源站，需额外 TLS 反向代理。

## ESA/EO 使用方式

1. 模板编辑器把 `vless-ws-cdn` 的监听改为 `0.0.0.0`（IPv4）或 `::`（双栈），保存模板；
2. 重新下发/应用目标节点；
3. 节点防火墙/云安全组放行 TCP 对应端口（默认 13707）；
4. ESA/EO 源站配置：协议 HTTP、源站为节点公网 IP、端口 13707、路径 `/vlws`（与模板一致），开启 WebSocket；
5. 建议源站只允许 CDN 官方回源网段，避免暴露为任意公网入口。

## 注意

- `vless-ws-cdn` 是明文 WS，CDN 边缘终止 TLS，但 CDN 到源站这段为明文；若 CDN 要求 HTTPS 源站，需要另加 TLS 反向代理。
- 模板 1 被 15 个节点共用，修改其 `cdn_listen=0.0.0.0` 会影响所有使用该模板且生成 CDN 入站的节点；建议先复制模板或只对目标节点使用的模板修改。
- 仅 Tunnel 回源的节点保持 `127.0.0.1` 即可。

## 验证

- server `go build/vet/test` 全绿；
- web-antd 构建成功；
- 面板 healthz V1.5.4，15/15 节点在线；
- 全量发布 dist-release 更新 V1.5.4。