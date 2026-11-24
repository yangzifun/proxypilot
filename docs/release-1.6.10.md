# ProxyPilot 1.6.10 发布说明（客户端错误持久化与查询）

日期：2026-11-24
版本：面板 V1.6.10 / Agent 1.4.2 / VPN 客户端 1.4.1

## 新增

### 1. 客户端错误持久化

- 迁移 `0063_client_errors` 新增 `client_errors` 表：
  - message；
  - stack；
  - url；
  - page；
  - mode；
  - ua；
  - created_at；
- 索引：
  - `created_at`；
  - `mode + created_at`。
- `POST /api/client-errors`：
  - 保留原有 16KB/字段截断/IP 限速/日志；
  - best-effort 写入 `client_errors`；
  - DB 写入失败不影响上报成功，只记录服务端 Warn；
  - URL 不保存 query/fragment，避免敏感参数。

### 2. 管理后台查询

- 新增 `GET /api/client-errors`（管理员可读）：
  - 分页参数 `page`、`size`；
  - 可选 `mode` 过滤；
  - 按时间倒序返回。
- 日志管理新增「前端错误」Tab：
  - 时间、模式、页面、错误信息、URL、UA；
  - 分页、刷新、mode 过滤；
  - 查看堆栈弹窗；
  - 加载/错误/空态；
  - 代码块使用主题变量。
- `cronCleanup` 每日清理：
  - `client_error_retention_days` 默认 30 天；
  - 范围 7–365 天。

## 验证

- server 构建、测试全绿；
- web-antd 构建成功；
- render-check 38/0；
- 线上验证：
  - 迁移 0063 已应用；
  - 测试上报成功写入 `client_errors`；
  - `GET /api/client-errors?mode=admin` 返回记录；
  - URL 中 query 参数已被移除；
  - 测试记录已清理；
- 面板 healthz V1.6.10，15/15 节点在线；
- 全量发布 dist-release 更新 V1.6.10。

## 说明

- 客户端错误只保存截断后的文本，不上传请求头/Authorization/Cookie；
- 公开上报接口有限速，避免日志/数据库放大；
- 默认保留 30 天，可在设置中写入 `client_error_retention_days` 调整；
- 当前错误列表不提供删除/导出，后续可扩展。