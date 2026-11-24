# ProxyPilot 1.6.12 发布说明（客户端错误管理完善）

日期：2026-11-24
版本：面板 V1.6.12 / Agent 1.4.2 / VPN 客户端 1.4.1

## 新增

### 1. 客户端错误管理接口

- `DELETE /api/client-errors/{id}`：删除单条；
- `DELETE /api/client-errors?mode=&before=`：按条件批量清理；
  - 至少提供一个条件，避免误删全部；
- `GET /api/client-errors/export`：CSV 导出；
  - 表头：`id,created_at,mode,page,url,message,ua`；
  - 最多 10000 条；
  - 支持 mode / before 过滤；
  - 仅超级管理员可调用。

### 2. 日志页「前端错误」管理操作

- 行内「删除」：superOnly，二次确认；
- 工具栏「导出 CSV」：superOnly，带 Authorization 下载；
- 「清理 30 天前」：superOnly，二次确认；
- 普通管理员不显示管理操作；
- 保留分页、mode 过滤、堆栈查看。

## 修复

- 修复 CSV 导出 SQL 中 `COALESCE` 字符串默认值缺失引号导致 500「导出失败」的问题；
- 修复后实测：
  - 导出返回 200；
  - `Content-Type: text/csv`；
  - `Content-Disposition: attachment; filename="client-errors.csv"`；
  - CSV 表头及记录正确。

## 验证

- server 构建、测试全绿；
- web-antd 构建成功；
- render-check 38/0；
- 线上验证：
  - 创建测试错误；
  - 导出 CSV 200 且内容正确；
  - 删除单条成功；
  - 测试数据已清理；
- 面板 healthz V1.6.12，15/15 节点在线；
- 全量发布 dist-release 更新 V1.6.12。

## 说明

- 删除/导出/批量清理仅超级管理员可用；
- 批量清理必须提供 mode 或 before 条件；
- 导出最多 10000 条，避免大表一次性加载。