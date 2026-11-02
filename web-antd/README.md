# ProxyPilot · Ant Design 控制台（web-antd）

一套基于 **React 18 + Ant Design 5** 的后台控制台，**整体打包进同一个 Cloudflare Worker**，
不依赖任何外部 CDN，也不需要单独的静态站点托管。

- 后端逻辑仍然全部由 `workers/worker.js` 提供（本方案**不改动、不删除**它）
- 新 UI 挂载在 `/admin`；老版后台页面已下线（`worker.js` 里的代码保持原样，只是不再有路由指过去）
- 图表为自绘 SVG（面积图 / 柱状图 / 环形图 / 仪表盘 / 迷你走势），配色取自 antd 主题 token
- 图标全部来自 `@ant-design/icons`

```
web-antd/
├── src/
│   ├── main.jsx            入口：ConfigProvider + AuthProvider
│   ├── App.jsx             哈希路由 + 页面装配
│   ├── api.js              /api 客户端（JWT 复用老后台的 localStorage: token）
│   ├── auth.jsx            登录态：账号密码 / 邮箱验证码 / OIDC
│   ├── hooks.js            useAsync / usePolling / useHashRoute / useTurnstile
│   ├── theme.js            antd 主题 token + 图表配色
│   ├── charts.jsx          自绘 SVG 图表
│   ├── components.jsx      卡片 / 指标 / 状态 / 复制 / JSON 预览
│   ├── nav.jsx             菜单与页面元信息
│   ├── shell/AppLayout.jsx 侧边栏 + 顶栏外壳
│   └── pages/              9 个页面 + 登录页
└── scripts/build.mjs       构建：esbuild → 内联进 Worker
```

## 构建

```bash
cd web-antd
npm install
npm run build
```

构建会做三件事：

1. `esbuild` 把 `src/main.jsx` 打成单个 `dist/app.js` + `dist/app.css`
2. 把两个产物以字符串常量写入 `workers/worker-antd-assets.js`
3. 用 esbuild 把 `workers/worker-antd.js`（含 `worker.js` 全部后端）合并成单文件
   `workers/worker-antd.bundle.js`

## 部署（Cloudflare Workers）

两种方式任选其一：

**A. wrangler（推荐，多文件）**

```toml
# wrangler.toml
name = "proxypilot"
main = "workers/worker-antd.js"
compatibility_date = "2024-11-01"

[[d1_databases]]
binding = "DB"
database_name = "proxypilot"
database_id = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"

[[r2_buckets]]
binding = "bucket"
bucket_name = "proxypilot"
```

`wrangler` 会自动打包 `worker.js` / `worker-antd-assets.js` 这些 import。

**B. 控制台粘贴（单文件）**

把构建产物 `workers/worker-antd.bundle.js` 整份贴进 Cloudflare 控制台的 Worker 编辑器即可
（已把后端与前端合成一个模块）。

## 路由

| 路径 | 说明 |
| --- | --- |
| `/admin`、`/ui` | **新版 Ant Design 控制台**（SPA 外壳，客户端哈希路由 `#dash` / `#uuids` …） |
| `/admin-app.js`、`/admin-app.css` | 前端产物，内容哈希 ETag + 一年强缓存 |
| `/`、`/me` | **用户中心**（新版 Ant Design）：邮箱验证码登录 + 我的订阅 + 单个 UUID 管理（配置 / 订阅 / 统计 / 订阅源 / 换邮箱），PC 与移动端自适应 |
| `/manage`、`/manage/` | 已下线（302 回 `/`）；`/manage/configs`、`/manage/stats`、`/manage/uuid-info` 等**接口**不受影响，仍交给 `worker.js` |
| `/admin-classic` | 已下线（返回 404）；老版后台代码仍在 `worker.js` 里，但没有路由暴露 |
| 其它全部 | 原样交给 `worker.js`（`/login`、`/sub/*`、`/clash-config`、`/api/*` …） |

## 环境变量

| 变量 | 作用 |
| --- | --- |
| `SUPER_ADMIN_USERNAME` | 超级管理员账号名（默认 `admin`），决定「系统管理」菜单是否可见 |
| `TURNSTILE_SITE_KEY` | 登录页人机验证的站点密钥；设置后自动加载 Turnstile |
| `OIDC_ISSUER` / `OIDC_CLIENT_ID` | 配置后登录页出现「单点登录」按钮 |
| `OIDC_PROVIDER_NAME` / `OIDC_BUTTON_TEXT` / `OIDC_DISABLE_PASSWORD` | 单点登录文案与「禁用密码登录」 |
| `EMAIL` / `SENDER_EMAIL` | 配置后登录页出现「邮箱验证码」页签 |
| `ADMIN_UI_CLASSIC` | ~~设为 `1` 时回退老后台~~ —— 老版页面已下线，此开关已移除 |

## 页面

| 哈希 | 页面 | 主要内容 |
| --- | --- | --- |
| `#dash` | 系统概览 | 指标卡、访问趋势面积图、访问构成环形图、热门订阅 Top10、最新订阅时间线 |
| `#uuids` | 订阅分组 | 服务端分页表格、搜索/状态筛选、新建/编辑、删除（级联清理）、详情抽屉（含节点配置） |
| `#tpls` | 订阅模板 | 模板 CRUD、设为默认、对象存储文件浏览与在线编辑 |
| `#servers` | 服务器管理 | 在线/离线/未知统计、接入命令与密钥查看、密钥轮换、任务队列弹窗 |
| `#singbox` | 节点管理 | 协议模板列表（协议标签、端口基线、用户数）、节点绑定、一键下发 / 全量部署 |
| `#status` | 服务器监控 | 在线率环形图、CPU/内存仪表盘、告警列表、各节点资源柱状图、服务状态矩阵（30s 自动刷新） |
| `#logs` | 日志管理 | 日志构成、按保留期可删行数柱状图、需要密码确认的清理、下发记录表 |
| `#sys` | 系统管理 | 管理员账号 CRUD / 重置密码 / 邮箱绑定 / MFA 重置；API 密钥 CRUD、轮换、启停 |
| `#sec` | 安全中心 | 修改密码、TOTP 绑定（二维码 + 备份码）、重新生成备份码、关闭 MFA |

## 说明

- 会话与老后台共用同一枚 JWT（`localStorage.token`），两边登录状态互通。
- 401 会自动清会话并回到登录页。
- 图表没有引入 `@ant-design/plots` / ECharts：Worker 脚本体积有上限，
  自绘 SVG 只占几 KB，且能直接吃 antd 主题色。
