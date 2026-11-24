# ProxyPilot 1.6.8 发布说明（/me 入口修复 + DoH 统计清理）

日期：2026-11-24
版本：面板 V1.6.8 / Agent 1.4.2 / VPN 客户端 1.4.1

## 修复

### 1. `/me` 用户中心入口恢复

- 后端 `pageIndex` 已支持 `/me`，但路由只注册了 `/` 与 `/admin`，导致 `/me` 返回 404；
- 新增路由：
  - `GET /me`
  - `GET /me/`
- `/me` 现在返回 portal shell，与 `/` 相同；
- 实测：
  - `https://proxypilot.yangzifun.org/me` → 200，`text/html`；
  - `/admin` → 302 Cloudflare Access 登录（预期）。

### 2. DoH 小时统计自动清理

- `cronCleanup` 增加 `doh_usage_hourly` 清理：
  - 保留最近 180 天；
  - 可通过设置 `doh_retention_days` 调整（7–3650 天）。
- 只清理聚合统计，不涉及 DNS 查询内容；
- 防止统计表长期无限增长。

## 测试

- 新增 `/me` portal shell 断言；
- 更新旧“已删除路由”测试，移除 `/me`；
- `go test ./...` 全绿；
- web-antd 构建成功；
- 面板 healthz V1.6.8，15/15 节点在线；
- 全量发布 dist-release 更新 V1.6.8。

## 说明

- `/me` 与 `/` 均为用户中心入口，后台入口仍为 `/admin`；
- DoH 统计保留天数未暴露到设置 UI，如需可在后续配置页增加；
- 统计清理在每日 cron 清理窗口执行。