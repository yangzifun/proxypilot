# ProxyPilot 1.5.25 发布说明（面板内置 DoH 服务）

日期：2026-11-22
版本：面板 V1.5.25 / Agent 1.4.2 / VPN 客户端 1.4.1

## 新增

- 面板服务器内置 DoH（DNS over HTTPS）服务：
  - 每位用户使用自己的 UUID 作为访问凭证；
  - 地址：`https://proxypilot.yangzifun.org/dns-query/<uuid>`；
  - 支持 RFC 8484 GET（`?dns=base64url`）与 POST（`application/dns-message`）；
  - 仅面板服务器提供，节点不安装 DoH；
  - UUID 状态为 inactive/suspended 时拒绝；
  - 每 UUID+IP 限速，默认 20 QPS，超限 429；
  - 请求体默认限制 4 KB，上游固定 DoH 列表，5 秒超时并 fallback；
  - 默认不记录 DNS 查询内容，仅按小时聚合查询数与字节数。
- 管理后台「系统设置 → DoH 服务」：
  - 启用/关闭；
  - DoH 域名；
  - 上游 DoH 列表；
  - 每用户 QPS；
  - 查询日志开关（默认关闭）；
  - 示例 DoH URL。
- 用户中心新增 DoH 卡片：
  - 展示个人 DoH URL、复制按钮；
  - 启用状态、近 30 天查询量、最近使用；
  - Chrome/Android/AdGuard 使用说明。
- 新增 `doh_usage_hourly` 统计表（迁移 0060_doh_usage）。
- 面板 nginx 已增加 `/dns-query/` 独立反代，关闭该路径 access log。

## 验证

- 线上 POST/GET DoH 查询 `example.com A` 成功：
  - `200`，`Content-Type: application/dns-message`，61 字节响应；
- 无效 UUID：400；
- `doh_usage_hourly` 已记录查询数与上下行字节；
- 5 KB POST 在 nginx 层返回 413；
- 外部 `https://proxypilot.yangzifun.org/dns-query/<uuid>` 可直达面板，无 Access 登录跳转；
- 面板 healthz V1.5.25，15/15 节点在线；
- server 测试全绿，web-antd 构建成功；
- 全量发布 dist-release 更新 V1.5.25。

## 注意

- DoH 与面板共用域名，Cloudflare 侧不要对 `/dns-query/*` 启用人机验证/Challenge；当前 Access 应用为 0，若后续启用 Access，必须为该路径配置 Bypass；
- UUID 等同访问凭证，请勿公开分享；
- 默认不记录查询内容，开启 `doh_log_queries` 后才记录 QNAME；
- 当前为单面板节点 DoH，无节点级容灾；
- 客户端使用完整 DoH URL（含路径），Android Private DNS 若不支持路径需使用支持 DoH URL 的客户端。