# ProxyPilot 1.5.2 发布说明（CDN 取消主备，仅保留启用）

日期：2026-11-21
版本：面板 V1.5.2 / Agent 1.4.1 / VPN 客户端 1.4.1

## 变更

1. 多 CDN 不再区分主/备：
   - 删除后台“主/备”单选与展示；
   - `primary` 仅保留旧 JSON 兼容，后端完全忽略，不再参与排序、唯一性校验或 fallback。
2. 只按“是否启用”控制：
   - 每条 CDN 保留「启用」开关，可同时启用多个；
   - 所有启用的 CDN 按条目顺序生成订阅链接；
   - 停用的 CDN 不生成链接；
   - 全部停用则不生成 CDN 链接，并提示“所有 CDN 已停用”。
3. 兼容字段：
   - `cdn_domain` 取第一个启用域名；
   - `cdn_domains` 只包含启用域名；
   - `cdn_entries` 保留全部条目（含 enabled），旧 `primary` 字段可解析但不使用。
4. 列表摘要按启用数量显示，例如 2 个启用、1 个停用显示 `CDN CF +1`。

## 验证

- 面板 healthz V1.5.2，15/15 节点在线；
- `/api/nodes/bindings` 仍返回 `sb_cdn_entries`（名称/enabled）；
- `/api/singbox/nodes` 返回 `cdn_entries`；
- server `go build/vet/test` 全绿；
- web-antd 构建成功；
- 全量发布 dist-release 更新 V1.5.2。

## 注意

- 旧数据中的 `primary=true` 不会被删除，但不再影响链接生成；
- 停用仅影响订阅链接，不影响已下发节点配置；
- 每个 CDN 域名仍需在对应 CDN/Tunnel 侧配置回源。