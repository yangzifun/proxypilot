# ProxyPilot 1.5.17 发布说明（CDN 版本命名与订阅刷新）

日期：2026-11-21
版本：面板 V1.5.17 / Agent 1.4.2 / VPN 客户端 1.4.1

## 变更

1. CDN 版本节点名称规则统一为 `节点-CDN名称-1`：
   - 单 CDN 单地址：`彰化-Cloudflare-1`；
   - 多 CDN：`节点-Cloudflare-1`、`节点-ESA-1`；
   - 多优选地址：`-1`、`-2`；
   - 同名多条 CDN 先按条目序号区分，再追加目标序号；
   - 旧 legacy CDN 标签 `-cdn` / `-cdn-1` 保持兼容。
2. 保存 CDN 后自动刷新订阅配置：
   - 节点绑定保存 CDN 变更后，自动调用单节点 reconcile（force）；
   - 不再需要手动“下发”才能看到对应 CDN 版本；
   - 失败时保留节点数据并提示手动重试。
3. 修复 reconcile 配置同步：
   - 旧逻辑用“用户名+链接”与“UUID+链接”比较，导致强制 reconcile 可能误删配置；
   - 统一改为 `syncSingboxConfigsForUsers`，使用 UUID 映射，只增删真正变化的链接；
   - 已执行一次全量 reconcile，恢复并刷新全部订阅配置。
4. 模板版本下拉仅显示存储桶版本（1.14.4 / 1.15.0-alpha.13），默认仍 1.14.4。

## 验证

- raw 订阅中已启用 CDN 的节点均出现 `节点-Cloudflare-1`：
  - 彰化、德国、东京2、洛杉矶1、台北、曼谷1；
- 无 CDN 节点不生成 CDN 变体；
- `configs` 总数 641、11 个 UUID，订阅行数正常；
- 面板 healthz V1.5.17，15/15 节点在线；
- server 测试全绿，web-antd 构建成功；
- 全量发布 dist-release 更新 V1.5.17。

## 注意

- CDN 链接内容未变，仅 tag 命名与订阅刷新逻辑调整；
- 单节点 reconcile 失败会提示，不影响节点自身配置；
- 旧的 `-cdn` 标签仍兼容，客户端可按新规则识别 CDN 版本。