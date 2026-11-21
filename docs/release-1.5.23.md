# ProxyPilot 1.5.23 发布说明（移除旧 CDN 名称 CF）

日期：2026-11-21
版本：面板 V1.5.23 / Agent 1.4.2 / VPN 客户端 1.4.1

## 变更

- CDN 名称彻底移除旧名称 `CF`：
  - 前端下拉不再提供 `CF（旧名称）`；
  - 旧数据中的 `CF` 自动归一化为 `Cloudflare`；
  - 后端保存/读取 CDN 名称时同样归一化，避免旧名称再次出现在订阅 tag 和后台；
  - 已检查线上节点 CDN 数据，无残留 `CF` 名称。
- 保留 Cloudflare/EO/ESA/CloudFront/Fastly 预设。
- 不影响现有 Cloudflare 优选组、`节点-Cloudflare` / `节点-Cloudflare-1…N` 命名。

## 验证

- 构建产物中已无 `CF（旧名称）` 文案；
- 旧 `CF` 条目经前端/后端归一化后显示为 Cloudflare；
- 线上节点 CDN 数据无 CF 残留；
- 面板 healthz V1.5.23，15/15 节点在线；
- server 测试全绿，web-antd 构建成功；
- 全量发布 dist-release 更新 V1.5.23。

## 注意

- 订阅客户端需刷新一次以获取最新名称；
- 旧 CF 数据在下次保存时会写为 Cloudflare。