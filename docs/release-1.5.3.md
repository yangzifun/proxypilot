# ProxyPilot 1.5.3 发布说明（旧版接口清理）

日期：2026-11-21
版本：面板 V1.5.3 / Agent 1.4.1 / VPN 客户端 1.4.1

## 清理内容

1. **旧 Worker 兜底/501 机制全部移除**
   - 删除 `PP_LEGACY_BASE`、`legacyPath`、`proxyAttempt`、`notMigrated`、`Config.LegacyBase`、旧 HTTP 转发 client；
   - 未注册路径统一 JSON 404，不再转发旧 Worker、不再返回 501；
   - `/api/mail/settings`、`/api/xray/status` 等旧入口现为 404。

2. **删除 23 条旧路由/页面别名**
   - `/api/node-templates` 5 条；
   - `/api/node-configs/preview`、`/api/node-configs/push`；
   - `/api/stats/uuid-details`、`/api/uuid-configs/stats`；
   - `/api/servers/services`、`/api/mihomo/stats`；
   - `/me`、`/ui`、`/manage`、`/manage/all-uuid-info|entry|generate-uuid|uuid-check`、`/admin/certs`、`/admin/settings`。
   - 对会被宽路由捕获的已删路径增加显式 404。

3. **保留在用接口**
   - `/`、`/admin`、`/login` SPA；
   - `/manage/uuid-info`、`/manage/configs`、`/manage/stats`、`/manage/uuid-sub-sources`；
   - `/api/node-configs`、`/{id}`、`/{id}/push`、`/logs`；
   - `/api/mihomo/bin/{arch}`、`/api/mihomo/report`；
   - `/api/agent/*`、`/api/client-auth/*`、`/api/vpn/device/*`、订阅输出、Agent WebSSH。

4. 删除仅被旧路由引用的死代码文件/函数；更新测试与文档。

## 验证

- 面板 healthz V1.5.3，15/15 节点在线；
- 已删除接口/页面返回 404；
- 保留接口正常工作（`/api/mihomo/bin/amd64` 200、`/api/mihomo/report` 400 参数校验、`/api/node-configs/40/push` 401 未授权、`/api/node-configs` 401 未授权）；
- server `go build/vet/test` 全绿；
- 部署 smoke check 中 `/api/mail/settings` 已从 501 变为 404，属于预期。

## 注意

- 旧书签/旧脚本调用已删除路径会得到 404；
- 旧 Worker 兜底已不可恢复；如需旧功能需重新实现；
- 本轮未删除 DB 表/列、未停止服务；DB 死表/旧列、台北 mihomo 等留待后续单独清理。