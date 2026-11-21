# ProxyPilot 1.5.1 发布说明（多 CDN 名称保存 + 主备/启用）

日期：2026-11-21
版本：面板 V1.5.1 / Agent 1.4.1 / VPN 客户端 1.4.1

## 修复

1. **CDN 名称无法保存**
   - 根因：`/api/nodes/bindings` 只返回旧 `cdn_domain`，没有返回 `cdn_entries`；NodeBindings 编辑回填时名称丢失。
   - 修复：bindings 接口新增 `sb_cdn_entries`（含 name/enabled）与 `sb_cdn_domains`；前端优先读取 `sb_cdn_entries` 回填。
   - 保存后再次打开节点，名称可正常保留。

2. **主 CDN / 备用 CDN**
   - 每条 CDN 明确显示“主/备”；
   - 主 CDN 只能有一个，且必须启用；
   - 关闭当前主 CDN 时自动回退到第一个启用条目。

3. **每个 CDN 增加启用开关**
   - 新增 `enabled` 字段，缺省 true，旧数据/旧请求自动视为启用；
   - 停用的 CDN 不生成订阅链接；
   - 全部停用时提示“所有 CDN 已停用，不会生成 CDN 链接”；
   - 节点列表摘要按启用数量显示，tooltip 标注主/备、启用/停用。

## 兼容性

- 无需新数据库迁移；`enabled` 存在 `cdn_entries` JSON 内；
- 旧 JSON 无 enabled 读取默认 true；
- 保留 `cdn_domain` 兼容列；
- 旧单域名链接行为不变。

## 验证

- 面板 healthz V1.5.1；
- `/api/nodes/bindings` 已返回 `sb_cdn_entries`，现有节点名称可见；
- `/api/singbox/nodes` 返回 `cdn_entries`（含 enabled）；
- server `go build/vet/test` 全绿；
- web-antd 构建成功；
- 全量发布 dist-release 更新 V1.5.1。

## 注意

- 停用 CDN 只是不生成订阅链接，不影响已下发节点配置；
- 每个 CDN 域名仍需在对应 CDN/Tunnel 侧配置回源；
- 主 CDN 建议保持启用，避免订阅主链接缺失。