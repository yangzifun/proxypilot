# ProxyPilot 1.6.6 发布说明（Portal 首屏拆包）

日期：2026-11-24
版本：面板 V1.6.6 / Agent 1.4.2 / VPN 客户端 1.4.1

## 变更

- 后台首屏不再静态加载用户中心 PortalApp；
- `main.jsx` 改为：
  - `const PortalApp = React.lazy(() => import('./portal/PortalApp'))`；
  - 仅 `IS_PORTAL` 分支使用 `<Suspense>` 加载；
  - 后台分支保持 `AuthProvider + App` 不变。
- 新增按需 Portal chunk，仅访问用户中心 `/` 或 portal 模式时加载；
- 后台首屏不再携带 antd-mobile 与用户中心业务代码。

## 体积结果

| 产物 | 改前 | 改后 | 变化 |
|---|---:|---:|---:|
| 入口 app.js | 402,017 B | 33,973 B | -91.5% |
| 入口 gzip | 132,887 B | 11,767 B | -91.1% |
| 首屏 JS（入口+eager） | 1,868,951 B | 1,504,535 B | -19.5% |
| 首屏 JS gzip | 611,160 B | 491,155 B | -19.6% |
| Portal chunk | 无 | 364,937 B | 仅 Portal 加载 |

## 验证

- `node scripts/build.mjs` 成功；
- `node scripts/render-check.mjs`：38/38 通过；
- server 构建、测试全绿；
- 真实浏览器实测：
  - `/admin`：只加载后台共享 chunk，无 `PortalApp-*.js`；
  - `/`：加载 `PortalApp-*.js`，用户中心登录页正常；
  - 无错误边界/白屏；
- 面板 healthz V1.6.6，15/15 节点在线；
- 全量发布 dist-release 更新 V1.6.6。

## 注意

- Portal chunk 为按需资源，首次访问用户中心会增加约 365 KB 下载；
- 后台首屏体积已明显下降，剩余主要是 React/AntD 共享 vendor chunk；
- 极端网络下 Portal 加载失败暂无专用错误边界，后续可补。