# ProxyPilot 1.5.21 发布说明（累计网络按月重置、重启不丢失）

日期：2026-11-21
版本：面板 V1.5.21 / Agent 1.4.2 / VPN 客户端 1.4.1

## 变更

1. 服务器「累计网络」改为 **本周期累计用量**：
   - 数据来自 `server_traffic_usage`，按周期持久化在数据库；
   - 不再使用 `/proc/net/dev` 开机计数，Agent/服务/服务器重启不重置；
   - 当月累计到下次重置日/时。
2. 默认每月 1 日 00:00 重置：
   - 没有设置流量限额的服务器同样按默认周期重置；
   - 如已配置重置日期/小时，则按配置时间重置。
3. 默认不设流量上限：
   - `server_traffic_limits.enabled=0`、`limit_bytes=0`；
   - 只做月度统计，不做限额/停服；
   - 超限提醒机制保持不变（启用限额时才生效）。
4. 监控页面与服务器列表的「累计网络」标签改为「本周期累计网络」，并显示重置日期/时间提示。

## 验证

- `/api/servers/monitor` 返回 `traffic` 周期字段（rx/tx/period_start/reset_day/reset_hour）；
- 东京2：本周期累计 rx 1.16 TB / tx 529 GB，周期起点 2026-11-01 00:00；
- `traffic-limit` API 返回 `enabled=0`、`limit_bytes=0`、`reset_day=1`、下次重置 2026-12-01 00:00；
- 用户流量 active 正常（当前 4 个活跃用户）；
- 面板 healthz V1.5.21，15/15 节点在线；
- 新增 `trafficPeriodStart`/`trafficNextReset` 单元测试，server 测试全绿；
- 全量发布 dist-release 更新 V1.5.21。

## 注意

- 累计网络现在是「本周期」口径，不再是服务器开机以来的累计；
- 月度重置不会影响已产生的月度统计历史；
- 如需设置特殊重置日/时间，在「流量限额 → 设置」里调整 reset_day/reset_hour，即使不启用限额也会按该时间重置。