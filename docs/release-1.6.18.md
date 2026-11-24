# ProxyPilot 1.6.18 发布说明（错误统计、筛选、告警邮件与恢复修复）

日期：2026-11-24
版本：面板 V1.6.18 / Agent 1.4.2 / VPN 客户端 1.4.1

## 新增

### 1. 前端错误统计与趋势

- `GET /api/client-errors` 支持：
  - `q`：message / page / url 搜索；
  - `from` / `to`：毫秒时间范围；
  - `mode` 过滤；
  - 分页与 total 不变。
- 新增 `GET /api/client-errors/stats?range=24h|7d|30d`：
  - total；
  - 按 mode 计数；
  - 按小时/天趋势；
  - Top 10 message；
  - Top page；
  - 最近错误时间。
- 日志页「前端错误」Tab：
  - 近 24h / 7d / 30d 查询量；
  - 活跃模式数、最近时间；
  - 趋势图；
  - 搜索、时间范围、mode 过滤与清空筛选；
  - 保留删除、导出 CSV、清理、堆栈查看。

### 2. 错误告警邮件

- 设置项：
  - `client_error_alert_enabled`；
  - `client_error_alert_emails`（留空回退监控通知邮箱）；
  - `client_error_alert_threshold` 默认 20；
  - `client_error_alert_cooldown_min` 默认 30。
- `POST /api/client-errors` 落库后异步检查：
  - 5 分钟错误数达到阈值；
  - 超过冷却期；
  - 使用现有邮件服务发送告警；
  - 邮件未配置/无收件人时静默跳过；
  - 不阻塞请求。
- 系统设置「邮件 / 通知」增加「客户端错误告警」配置区。

### 3. 懒加载 chunk 旧版本恢复

- 面板滚动部署后旧 chunk 404 时：
  - `lazyWithRetry` 首次自动刷新一次；
  - ErrorBoundary 二次失败显示「页面资源已更新，请重新加载」；
  - 不再直接显示英文 `Failed to fetch dynamically imported module`；
  - 10 秒内不重复刷新，避免死循环。

## 修复

- 修复客户端错误统计接口 SQL 中 `COALESCE` 引号缺失导致 500；
- 修复趋势按小时分组计算错误，改为 `FLOOR(created_at / bucket) * bucket`；
- 修复旧 chunk 404 页面白屏/英文异常。

## 验证

- server 构建、测试全绿；
- web-antd 构建成功；
- render-check 38/0；
- 线上统计接口：
  - by_mode、trend、top_messages、top_pages 正常；
  - 趋势按小时聚合正确；
- 设置页保存错误告警四项参数正常；
- 旧 chunk 模拟 404 自动恢复 24/24 通过；
- `/me` 返回 200；
- 面板 healthz V1.6.18，15/15 节点在线；
- 全量发布 dist-release 更新 V1.6.18。

## 说明

- vendor chunk 拆分评估结论：esbuild 无法 manualChunks；多入口/external 方案会增加体积或破坏 tree-shaking，暂不实施；后续若需分组缓存可评估 Vite/Rollup 迁移。
- 客户端错误告警默认关闭，需在系统设置启用并配置收件人；
- 告警仅在邮件服务可用时发送，发送失败不会更新冷却时间。