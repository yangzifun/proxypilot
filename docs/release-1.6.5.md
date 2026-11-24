# ProxyPilot 1.6.5 发布说明（路由拆包 + 侧边栏折叠持久化）

日期：2026-11-24
版本：面板 V1.6.5 / Agent 1.4.2 / VPN 客户端 1.4.1

## 一、前端路由拆包

- 构建从单文件 IIFE 改为 ESM + esbuild splitting；
- `App.jsx` 中 19 个页面改为 `React.lazy` + `Suspense`：
  - Dashboard / Login / AppLayout 保持同步；
  - 其余页面进入按需 chunk；
  - 懒加载失败由 PageErrorBoundary 兜底。
- chunk 输出到 `assets_web/chunks/*-<hash>.js`；
- Go 端：
  - `//go:embed assets_web/chunks/*`；
  - 新增 `GET /chunks/*` 静态路由；
  - 白名单、目录穿越防护、br/gzip 优先、immutable 缓存；
  - 外壳改为 `<script type="module">`，并对首屏共享 chunk 添加 modulepreload。
- G6 / xterm 仍保持独立按需包。

体积实测：

- 入口 `app.js`：2,351,171 B → 402,017 B（-82.9%）
- 入口 gzip：750,030 B → 132,887 B（-82.3%）
- 首屏 JS（入口 + eager 共享 chunk）：2,351,171 B → 1,868,951 B（-20.5%）
- 24 个页面 chunk 按需加载；
- 浏览器实测：进入订阅模板页时按需加载 `Templates-*.js`，页面与表格正常。

## 二、侧边栏折叠持久化

- 新增 localStorage key：`pp.sider.collapsed`；
- Header 新增折叠/展开按钮，带 aria-label / aria-expanded / Tooltip；
- 刷新后自动恢复折叠状态；
- 折叠时品牌文字和版本文案隐藏，品牌 mark 居中；
- 菜单分组、权限、导航不变；
- 真实浏览器实测默认深色侧边栏背景 `rgb(15,23,42)` 正常。

## 三、测试

- `node scripts/build.mjs` 成功；
- `node scripts/render-check.mjs`：38/38 通过；
- server 构建、测试全绿；
- Go 测试覆盖 `/chunks/*` 200、目录穿越/非 js 404；
- 浏览器实测：
  - 登录态、主题、懒加载页面正常；
  - `/chunks/` 请求正常；
  - 左侧边栏主题与折叠状态正常；
- 面板 healthz V1.6.5，15/15 节点在线；
- 全量发布 dist-release 更新 V1.6.5。

## 注意

- `assets_web/chunks/` 为本次新增构建产物，需随版本提交；
- Go 构建前需至少执行一次 `node scripts/build.mjs`；
- 当前首屏仍包含约 1.45 MB 的共享 chunk（React/AntD 等），后续可继续拆分 vendor chunk；
- 页面首次进入会额外加载对应路由 chunk，属预期行为。