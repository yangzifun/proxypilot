# ProxyPilot 1.5.9 发布说明（CDN 名称/域名与品牌标签）

日期：2026-11-21
版本：面板 V1.5.9 / Agent 1.4.1 / VPN 客户端 1.4.1

## 变更

1. CDN 名称改为严格下拉选择：
   - 预设：`Cloudflare`、`EO`（EdgeOne）、`ESA`、`CloudFront`、`Fastly`；
   - 不可手填；旧值 `CF` 以「CF（旧名称）」临时选项保留，用户可改选 Cloudflare；
   - 名称仅用于后台识别/节点命名。
2. CDN 域名改为手动填写：
   - 不再下拉；只允许域名，不支持协议/路径/端口/IP；
   - 仍需在对应 CDN/Tunnel 侧配置回源。
3. CDN 列改为品牌标签：
   - 每个 CDN 显示一个标签，不再显示 `CDN` 前缀文本；
   - 品牌色参考各 CDN：
     - Cloudflare `#F38020`
     - ESA `#FF6A00`
     - EO `#0052D9`
     - CloudFront `#FF9900`
     - Fastly `#FF282D`
   - 停用 CDN 使用灰色标签并标注「（停用）」；
   - 旧 `CF` 显示为 Cloudflare 品牌标签，存储值不改。
4. 启用开关、最多 8 条、保存刷新、CDN 名称/域名保存逻辑不变。

## 验证

- 真实浏览器：节点绑定 CDN 列显示橙色 Cloudflare 标签，无「CDN」前缀；编辑弹窗名称为下拉、域名为普通输入；
- jsdom/构建：名称 Select 选项 Cloudflare/EO/ESA/CloudFront、旧 CF 保留；域名 Input 可手填；标签色值正确；
- web-antd 构建成功；
- server 测试全绿；
- 面板 healthz V1.5.9，15/15 节点在线；
- 全量发布 dist-release 更新 V1.5.9。

## 注意

- 旧名称不会自动迁移；需要显示 Cloudflare 的旧 CF 节点请编辑后改选 Cloudflare；
- 域名需手动输入，CDN 名称与域名都不再影响节点连接配置，仅影响订阅链接/后台展示；
- 修改 CDN 后仍需重新下发节点配置（如涉及 vless-ws-cdn 监听）。