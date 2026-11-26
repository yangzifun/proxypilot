# ProxyPilot 1.6.26 发布说明（通知风暴修复与通知/上下线记录）

日期：2026-11-26
版本：面板 V1.6.26 / Agent 1.4.2 / VPN 客户端 1.4.1

## 问题

2026-11-26 04:20 左右，面板在约 11 秒内连续发出上百封「服务器离线 / 恢复」邮件；多台设备实际在线但频繁收到上下线通知。

根因：

- `notifyStatus` 先检查冷却、再写 `monitor_notify_last_at`，两步非原子；
- 多个 Agent 恢复 goroutine 同时通过冷却检查；
- 每个 goroutine 都各自发送一封邮件，形成邮件风暴；
- 恢复通知与离线通知没有按设备记录/去重。

## 修复

### 1. 通知冷却原子化

- 新增进程级 `notifyStatusMu`；
- 冷却检查、预占 `monitor_notify_last_at`、发信在同一临界区完成；
- 先预占再发信，发送失败记录 failed，不会被并发请求重复发送；
- 同一批多节点仍只发一封；
- 收件邮箱去重。

### 2. 通知记录

新增迁移 `0065_notification_logs`：

- kind（offline / recovery / traffic / certificate / status）
- title
- server_ids
- recipients
- status（sent / failed / skipped）
- error
- created_at

`notifyStatus` 每次实际发送批次写入一条，不按收件人拆条；
冷却中、未启用、未配置邮箱也会记录 skipped 原因。

### 3. 设备/服务器上下线记录

新增迁移 `0066_server_status_events`：

- server_id
- event（online / offline）
- reason
- occurred_at
- duration_sec
- created_at

记录规则：

- cron 判定离线时，仅在真正从非 offline → offline 时写一条；
- Agent 恢复时，用条件更新抢占 offline → online，仅成功者写 online 事件；
- online 事件带离线时长；
- 并发恢复不会重复写事件。

### 4. 查询 API 与日志页

新增 authed 分页接口：

- `GET /api/notification-logs?page=&size=&kind=&status=`
- `GET /api/server-status-events?page=&size=&server_id=&event=&from=&to=`

日志管理新增两个 Tab：

- 「通知记录」：时间、类型、标题、涉及设备、收件人、状态、错误；
- 「设备上下线」：时间、设备、事件、原因、离线时长；
- 支持分页、刷新、过滤、空态/错误态。

## 验证

- 8 并发 notifyStatus 只发送 1 封、只写 1 条 sent 日志；
- 发送失败记录 failed，冷却期内不重发；
- 离线事件仅写一次；
- 并发 online 只成功一次；
- 分页/过滤/迁移测试通过；
- server 构建、测试全绿；
- web-antd 构建成功，render-check 38/0；
- 线上 API 测试记录写入/查询/清理正常；
- 部署后 10 分钟内邮件直投日志为 0；
- 面板 healthz V1.6.26，15/16 节点在线；
- 全量发布 dist-release 更新 V1.6.26。

## 说明

- 历史邮件风暴无法追溯，修复从 V1.6.26 起生效；
- 通知记录/上下线记录默认保留，后续可增加按保留期清理；
- 现有 `monitor_notify_cooldown_min` 仍为 10 分钟；如仍觉频繁，可提高到 30–60 分钟；
- 本次未改变邮件发信配置与收件人。