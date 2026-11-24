# ProxyPilot 1.6.1 发布说明（修复 FlowGraph 主题变量未定义）

日期：2026-11-24
版本：面板 V1.6.1 / Agent 1.4.2 / VPN 客户端 1.4.1

## 修复

- 修复用户流量页打开 FlowGraph 时 `ReferenceError: themeVars is not defined` 导致页面崩溃的问题。
- 根因：
  - `FlowGraph.jsx` 的模块级 `rackIcon()` 函数直接引用了组件内的 `themeVars`；
  - 该变量只在 React 组件作用域内定义，模块级函数执行时不可见。
- 修复：
  - `rackIcon(w, h, themeVars = {})` 增加参数；
  - 组件渲染时显式传入 `themeVars`；
  - 保留主题变量 fallback，主题切换行为不变。

## 验证

- `node scripts/build.mjs` 成功；
- server 构建与测试全绿；
- 面板 healthz V1.6.1；
- 相关渲染检查无运行时错误；
- 全量发布 dist-release 更新 V1.6.1。

## 说明

- 浏览器控制台中的 CSP inline script 报错来自页面外部脚本/扩展，与本次主题代码无关；
- 若浏览器缓存旧 `admin-app.js`，请强制刷新（Ctrl/Cmd+Shift+R）。