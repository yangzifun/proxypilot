# ProxyPilot 1.5.22 发布说明（Cloudflare CDN 优选与命名）

日期：2026-11-21
版本：面板 V1.5.22 / Agent 1.4.2 / VPN 客户端 1.4.1

## 变更

1. Cloudflare CDN 自动使用 cloudflare 优选组：
   - 节点 CDN 名称选择 `Cloudflare`（含旧 `CF` 数据）时，自动读取「域名优先」中 `cloudflare` 分组的启用地址；
   - 不再需要手动填写 prefer_group；
   - 其它 CDN 名称（ESA/EO 等）不自动套用 Cloudflare 优选地址。

2. CDN 版本命名：
   - 默认节点：`节点-Cloudflare`（使用配置的 CDN 域名直连）；
   - 优选版本：`节点-Cloudflare-1`、`节点-Cloudflare-2`……按优选地址顺序；
   - 仍保持 SNI/Host 为节点 CDN 域名；
   - 旧 legacy 标签 `-cdn` / `-cdn-1` 保持兼容。

3. 删除旧名称 `CF`：
   - CDN 名称下拉移除 `CF（旧名称）`；
   - 新保存统一使用 Cloudflare/EO/ESA/CloudFront/Fastly。

4. 已全量刷新订阅配置：
   - 彰化、德国、东京2、洛杉矶1、台北、曼谷1 均生成默认 + 14 个 Cloudflare 优选版本；
   - 其它 CDN 或无优选地址的节点保持默认域名链接。

## 验证

- raw 订阅中 Cloudflare 节点出现 `节点-Cloudflare` 与 `节点-Cloudflare-1…14`；
- 德国/彰化等节点链接 SNI/Host 保持各自 CDN 域名；
- 无 Cloudflare 优选地址时不生成多余链接；
- 面板 healthz V1.5.22，15/15 节点在线；
- server 测试全绿，web-antd 构建成功；
- 全量发布 dist-release 更新 V1.5.22。

## 注意

- 优选地址来自「域名优先 → cloudflare 分组」，只使用启用的地址；
- CDN 名称仅 Cloudflare 自动使用 cloudflare 优选组，其它类型需自定义分组时保留原有 prefer_group 能力；
- 订阅客户端需刷新一次以获取新增优选节点。