# ProxyPilot 1.6.9 发布说明（客户端错误上报）

日期：2026-11-24
版本：面板 V1.6.9 / Agent 1.4.2 / VPN 客户端 1.4.1

## 新增

### 1. 客户端错误上报接口

新增公开接口：

```text
POST /api/client-errors
```

接收字段：

- `message`
- `stack`
- `url`
- `page`
- `mode`（admin / portal）
- `ua`
- `at`

限制：

- body 最大 16KB；
- message 500 字符、stack 4000、url/page 500、ua 300；
- 每 IP 30 次/分钟；
- URL 日志前移除 query/fragment，避免泄露 token；
- 只写服务端 slog Warn，不写数据库；
- 非 POST 405，超大 413，非法 JSON 400。

### 2. 前端自动上报

新增 `clientError.js`：

- `reportClientError()`：静默上报，`keepalive:true`；
- 同页面同错误 30 秒去重；
- 每页面生命周期最多 20 条；
- 失败静默，不影响页面。

接入点：

- `PageErrorBoundary` 捕获的错误；
- `window.error`；
- `unhandledrejection`。

后台/用户中心自动判断 `mode`。

## 验证

- server 构建、测试全绿；
- web-antd 构建成功；
- render-check 38/0；
- 接口实测：
  - 合法上报返回 200 `{"success":true}`；
  - 非法 JSON/超大/限速有对应错误；
- jsdom 验证：
  - window.error / unhandledrejection 均上报；
  - 30 秒去重、20 条上限生效；
  - PageErrorBoundary 上报 source=boundary；
  - 失败静默；
- 面板 healthz V1.6.9，15/15 节点在线；
- 全量发布 dist-release 更新 V1.6.9。

## 说明

- 客户端错误只进入服务端日志，不提供后台查询页面；
- 如后续需要历史错误列表，可增加错误聚合表和清理策略；
- 公开接口有限速，避免被用于日志放大器。