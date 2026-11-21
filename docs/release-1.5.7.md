# ProxyPilot 1.5.7 发布说明（CDN 名称改为下拉选择）

日期：2026-11-21
版本：面板 V1.5.7 / Agent 1.4.1 / VPN 客户端 1.4.1

## 变更

- CDN「名称」由可自由输入的 AutoComplete 改为 **严格下拉选择**：
  - 预设选项：`Cloudflare`、`ESA`、`EO`（EdgeOne）、`CloudFront`、`Fastly`；
  - 不再允许手填新名称；
  - 旧数据中的 `CF` 等自定义名称会以「CF（旧名称）」临时选项保留，避免编辑时丢失；
  - 用户可把旧名称改选为 Cloudflare/ESA/EO，保存后 CDN 列显示新名称。
- CDN「域名」仍为 AutoComplete：可从已配置域名中选择，也可手填新域名。
- CDN 列显示已配置名称（如 CDN Cloudflare）；旧 CF 原样显示。
- 启用开关、删除、最多 8 条、保存后刷新逻辑不变。

## 验证

- 真实浏览器：编辑彰化节点，名称字段为下拉控件，显示「CF（旧名称）」，底部提示「名称从下拉选择（Cloudflare/ESA/EO，不可手填）」；
- jsdom：名称 Select 预设包含 Cloudflare/ESA/EO，选择后 payload name 正确；旧 CF 回填不丢失；自定义名称不能输入；域名仍可手填；
- web-antd 构建成功；
- server 测试全绿；
- 面板 healthz V1.5.7，15/15 节点在线；
- 全量发布 dist-release 更新 V1.5.7。

## 注意

- 旧名称不会自动迁移；需要显示 Cloudflare 的旧节点请编辑并改选 Cloudflare；
- CDN 名称仅用于后台识别/节点命名，不影响连接；
- CDN 域名仍需对应 CDN/Tunnel 侧配置回源。