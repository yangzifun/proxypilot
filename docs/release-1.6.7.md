# ProxyPilot 1.6.7 发布说明（Portal 错误边界与路由预取）

日期：2026-11-24
版本：面板 V1.6.7 / Agent 1.4.2 / VPN 客户端 1.4.1

## 变更

### 1. Portal 错误边界

- `PageErrorBoundary` 支持自定义：
  - `title`
  - `homeLabel`
  - `homeHash` / `homePath`
  - 默认后台文案保持不变。
- 用户中心 Portal 分支增加错误边界：
  - 标题「用户中心出现异常」；
  - 返回按钮「返回用户中心」→ `/`；
  - chunk 加载失败或渲染异常不再白屏。

### 2. 路由 chunk 空闲预取

- 后台登录后空闲预取常用页面：
  - 订阅用户；
  - 订阅模板；
  - 节点服务；
  - 用户流量；
  - 系统设置。
- 使用 `requestIdleCallback`（timeout 4s），降级 `setTimeout 2.5s`；
- 预取失败静默，不阻塞首屏；
- 页面 loader 复用，避免重复构建 import。

## 验证

- web-antd 构建成功；
- render-check：38/38 通过；
- server 构建、测试全绿；
- 浏览器实测：
  - `/admin` 登录后空闲预取 5 个常用页面 chunk，不加载 Portal chunk；
  - `/me` 正常加载 Portal；
  - Portal chunk 404 时显示「用户中心出现异常」错误页，不白屏；
- 面板 healthz V1.6.7，15/15 节点在线；
- 全量发布 dist-release 更新 V1.6.7。

## 风险/说明

- 预取仅在登录后空闲执行，网络空闲时才会进行；
- Portal 返回使用 `location.assign('/')`，受限环境自动回退 hash；
- 后台首屏与 Portal 仍各自按需加载，不改变现有路由与权限逻辑。