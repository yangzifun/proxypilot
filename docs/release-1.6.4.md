# ProxyPilot 1.6.4 发布说明（侧边栏主题化 + 测试修复）

日期：2026-11-24
版本：面板 V1.6.4 / Agent 1.4.2 / VPN 客户端 1.4.1

## 侧边栏主题化

- 新增 7 个侧边栏语义变量：
  - `sider-bg`
  - `sider-bg-hover`
  - `sider-text`
  - `sider-text-active`
  - `sider-border`
  - `sider-brand`
  - `sider-brand-soft`
- 5 套站点主题分别配置侧边栏：
  - 默认：保持深色侧边栏；
  - 翠绿：浅绿侧边栏 + 深绿文字；
  - 极简：浅灰侧边栏；
  - 扁平：品牌深蓝侧边栏；
  - 毛玻璃：半透明白 + backdrop-filter。
- AntD Layout/Menu 的 siderBg、triggerBg、darkItemColor、darkItemSelectedBg 等改为主题变量；
- `.pp-sider`、品牌区、导航选中项、子菜单、侧边栏底栏全部变量化；
- 默认主题视觉基本保持不变。

## 测试修复

- `render-check.mjs` 从 **30 通过 / 8 失败** 修复为 **38 通过 / 0 失败**；
- 更新内容：
  - 侧边菜单分组/页面命名；
  - 监控页新标题；
  - 概览环形图/趋势图选择器；
  - 系统管理路由 `#sysusers`；
  - `/api/auth/me` 打桩与超管字段；
  - 订阅用户、节点服务新文案；
- 保留全部运行时错误检查和业务内容检查。

## 验证

- `node scripts/build.mjs` 成功；
- `node scripts/render-check.mjs`：38/38 通过，退出码 0；
- server 构建、测试全绿；
- 5 套主题侧边栏截图验收：
  - 默认深色；
  - 翠绿浅绿；
  - 极简浅灰；
  - 扁平深蓝；
  - 毛玻璃半透明；
- 面板 healthz V1.6.4，15/15 节点在线；
- 全量发布 dist-release 更新 V1.6.4。

## 截图

- `myworkspace/reports/theme-screenshots/v164-sider-contact-sheet.png`
- `myworkspace/reports/theme-screenshots/v164-dash-{light,emerald,pulse,horizon,glass}.png`

## 后续可继续优化

- 前端路由级代码拆分（当前 app.js 约 2.3 MB）；
- 侧边栏展开/收起状态持久化；
- 大页面按需加载。