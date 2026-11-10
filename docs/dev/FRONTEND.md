# 前端开发详解

> React 18 + Ant Design 6，esbuild 构建，产物内嵌进面板二进制。

## 1. 构建链路

```
web-antd/src/**           源码
   ↓ node scripts/build.mjs（esbuild）
web-antd/dist/{app.js,app.css}
   ↓ 自动复制 + 读 server/cmd/proxypilot/main.go 的 version 写 meta.json
server/internal/api/assets_web/
   ↓ go:embed
面板二进制（部署只需一个文件）
```

调试期可 `PP_WEB_DIST=/abs/path/web-antd/dist` 让后端直读磁盘，改前端不必重编译。

## 2. 目录与职责

| 路径 | 职责 |
|---|---|
| `src/App.jsx` | 路由 key → 页面组件映射、权限拦截、主题注入 |
| `src/nav.jsx` | `PAGE_META`（标题/图标/分组/权限）、`NAV_GROUPS`、`NAV_ORDER` |
| `src/api.js` | `api()` / `apiRaw()` 封装：自动带 token、401 跳登录、统一错误 |
| `src/hooks.js` | `useAsync` / `usePolling` / `useHashRoute` / `readForm` |
| `src/components.jsx` | `PageCard` / `StatCard` / `Toolbar` / `ErrorBlock` / `EmptyBlock` / `CopyText` / `JsonView` |
| `src/theme.js` | 色彩 token、`chartPalette`、`LOGO_URL` |
| `src/shell/AppLayout.jsx` | 侧边栏、头部、任务抽屉、主题切换 |
| `src/pages/*.jsx` | 29 个页面 |
| `src/portal/*` | 用户中心（PC 单页 + 移动端） |
| `src/styles.css` | 全局样式（约 3000 行，含主题变量） |

## 3. 页面清单

| 路由 key | 页面 | 说明 |
|---|---|---|
| `dash` | 系统概览 | 访问趋势、Top 榜 |
| `uuids` | 用户管理 | UUID 列表 + 详情抽屉（概览/代理配置/代理授权/VPN 授权/外部订阅源） |
| `userst` | 用户流量 | 上行下行、活跃判定、G6 流向图 |
| `tpls` | 订阅模板 | Clash/Mihomo 模板 |
| `tplhub` | 协议模板 | sing-box/MASQUE/MTProto 模板集中管理 |
| `servers` | 服务器 | 总览、Agent 接入、SSH 防护、实时终端 |
| `bindings` | 节点绑定 | 一台服务器绑定多协议；底部 VPN 节点绑定面板 |
| `cdnpref` | CDN 优选 | 优选地址与下发 |
| `masque` | WARP | Cloudflare WARP 注册与配置 |
| `vpn` | 组网 | 成员、RTT、心跳 |
| `normal` | sing-box 节点 | 节点列表与配置下发 |
| `mihomo` | mihomo | 模板/节点/流量 |
| `mtproto` | MTProto | 客户端与模板 |
| `logs` | 日志 | 访问日志、清理 |
| `attachments` | 附件 | 上传/预览/清理 |
| `backup` | 备份 | 导出导入 |
| `certs` | 证书管理 | 本站 CA + 功能证书签发/下发/下载 |
| `sec` | 安全中心 | 改密、MFA、邮箱 |
| `local` | 本机服务器 | 隧道、端口、进程、文件、终端 |
| `sysusers` | 用户管理（管理员） | 仅超管 |
| `syssettings` | 系统设置 | 站点、登录方式、SMTP、OIDC、Agent 分发 |

## 4. 新增页面五步法

```jsx
// ① pages/MyPage.jsx
export default function MyPage() {
  const { data, loading, error, reload } = useAsync(() => api('xxx'), []);
  return (
    <PageCard title="标题" extra={<Button onClick={reload}>刷新</Button>}>
      {error ? <ErrorBlock error={error} onRetry={reload} /> : null}
      <Table rowKey="id" size="small" loading={loading} dataSource={data || []} columns={cols} />
    </PageCard>
  );
}

// ② nav.jsx
mypage: {
  title: '我的页面',
  subtitle: '一行说明',
  icon: <CloudServerOutlined />,   // ← 必须 import！
  group: '系统',
  accent: '#0ea5e9',
  superOnly: true,                  // 可选
},
// ③ NAV_ORDER 里加 'mypage'；NAV_GROUPS 对应分组 keys 里加
// ④ App.jsx 的 PAGES 加 mypage: MyPage
// ⑤ node scripts/build.mjs
```

> ⚠️ 图标只写 JSX 不 import → `Element type is invalid` → **整页白屏**，且控制台看不到明显报错。

## 5. 数据层用法

```js
// 列表（自动处理 total）
const { data, loading, error, reload } = useAsync(
  () => api('uuids', { query: { page, size, kw } }),
  [page, size, kw]
);

// 轮询
usePolling(() => reload(), 5000);

// 写操作
await api('admin/vpn/nodes', { method: 'POST', body: payload });

// 原生请求（需要自定义响应头时）
const res = await apiRaw('/manage/uuid-sub-sources', { query: { uuid } });

// 表单
const v = await readForm(form);   // 自动校验 + 返回 values
```

`api()` 行为：

- 自动加 `Authorization: Bearer <localStorage.token>`
- 401 → 清 token + 跳登录
- 非 2xx → 抛出 `Error(error)`

## 6. 组件约定

```jsx
<PageCard title="…" subtitle="…" extra={<Space>按钮</Space>}>
  …内容…
</PageCard>

<Row gutter={[16, 16]}>
  <Col xs={12} lg={6}>
    <StatCard icon={<ThunderboltOutlined />} label="CPU" value="16%" accent="#f59e0b" hint="8 核" />
  </Col>
</Row>
```

- 统计卡统一 4 列（`xs={12} lg={6}`），不要整行铺满
- 表格统一 `size="small"`，操作列固定宽度避免换行
- 空态用 `EmptyBlock`，错误用 `ErrorBlock`（带重试）
- 危险操作一律 `Popconfirm`

## 7. 主题

`theme.js` 提供 token 与调色板；`styles.css` 用 CSS 变量实现 5 套主题（默认/翠绿/极简/扁平/毛玻璃）。
顶部有缩放（100%→80%）与主题切换，均存 localStorage。

新增颜色请加到 `theme.js` 的 `brand` / `chartPalette`，不要硬编码十六进制。

## 8. 用户中心（portal）

- PC：`PortalHome.jsx`（单页，登录后展示订阅/用量/设备）
- 移动：`PortalMobileApp.jsx`（4 页签）
- 登录：邮箱验证码 + Turnstile 人机验证（`PortalLogin.jsx`）

数据接口统一 `/manage/*` 与 `/api/portal/*`，只允许操作自己绑定的 UUID。

## 9. 构建排错

| 现象 | 原因 |
|---|---|
| 页面白屏、控制台无报错 | 组件未 import（常见图标）/ 渲染抛错被 React 吞掉 |
| 改了代码页面没变 | 没重新 build，或浏览器缓存 → 强刷 |
| 加载动画版本号不对 | `meta.json` 落后 → 重新 build（会读 main.go 版本） |
| 样式错乱 | 用了内联大块样式覆盖主题变量 |

排查技巧：用 CDP 注入 `console.error` 钩子后再加载页面，能捕获 React 的渲染错误。

## 10. 代码规范

- 页面文件一个默认导出；内部子组件放同文件末尾
- 禁止 `any` 式全量 `useState`；按需拆
- 数字/时间格式化统一用 `format.js` 的 `fmtNum` / `fmtTime` / `fmtAgo`
- 不要引入新依赖（当前只有 antd / react / react-spring / g6）
- 提交前必须 `node scripts/build.mjs` 成功
