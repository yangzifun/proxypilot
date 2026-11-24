# ProxyPilot 1.6.13 发布说明（旧 chunk 404 自动恢复）

日期：2026-11-24
版本：面板 V1.6.13 / Agent 1.4.2 / VPN 客户端 1.4.1

## 问题

滚动部署后面板 chunk 文件名 hash 变化，旧标签页仍加载旧 `admin-app.js`，点击懒加载页面时请求已删除的旧 chunk：

```text
Failed to fetch dynamically imported module:
https://proxypilot.yangzifun.org/chunks/NodeBindings-MEL6BNIW.js
```

ErrorBoundary 直接显示英文异常，用户无法继续使用。

## 修复

### 1. 新增 lazyRoute 工具

- `isChunkLoadError()` 识别：
  - `Failed to fetch dynamically imported module`
  - `Importing a module script failed`
  - `ChunkLoadError`
  - `Loading chunk ... failed`
  - `error loading dynamically imported module`
- `lazyWithRetry()`：
  - 首次 chunk 加载失败时写入 sessionStorage `pp.chunk.reload.at` 并自动 `location.reload()`；
  - 10 秒内二次失败不再自动刷新，抛给 ErrorBoundary，避免死循环；
  - 成功加载后清除标记。
- 所有 React.lazy 页面统一使用 lazyWithRetry。

### 2. ErrorBoundary 兜底

- 识别 chunk 加载错误；
- 未自动刷新过 → 自动刷新一次；
- 已刷新过 → 显示「页面资源已更新，请重新加载」+ 重新加载按钮；
- 不再把原始英文异常/堆栈作为主错误展示；
- 普通运行时错误逻辑保持不变。

## 验证

- web-antd 构建成功；
- render-check：38/38 通过；
- 模拟旧 chunk 404：
  - 首次触发自动 reload 一次；
  - 10 秒内第二次失败显示友好中文提示；
  - 正常页面加载不触发 reload；
  - 24/24 测试通过；
- 旧 chunk 线上确认返回 404；
- 面板 healthz V1.6.13，15/15 节点在线；
- 全量发布 dist-release 更新 V1.6.13。

## 说明

- 自动刷新只尝试一次，不会死循环；
- 若 Cloudflare/浏览器强缓存旧 `admin-app.js`，刷新后仍可能拿到旧引用，此时第二次进入会显示“页面资源已更新，请重新加载”；
- 建议后续对 `admin-app.js`/HTML 使用 no-cache 或更短缓存策略。