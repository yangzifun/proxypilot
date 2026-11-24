# ProxyPilot 1.6.2 发布说明（Ant Design 6.7.2 升级与主题控件增强）

日期：2026-11-24
版本：面板 V1.6.2 / Agent 1.4.2 / VPN 客户端 1.4.1

## 依赖升级

- `antd`：6.6.8 → 6.7.2
- `package-lock.json` 已更新
- `@ant-design/icons` 6.3.4 兼容确认：
  - peer 要求 React >= 16
  - 当前 React 18.3.1
  - `npm ls` 无依赖冲突
- `npm audit --omit=dev`：0 个漏洞

## 主题层增强

- ThemeProvider 显式设置 ConfigProvider 全局 `variant="outlined"`，保持现有视觉同时统一控件变体；
- 全局 Input / Select 启用 `allowClear`，输入框与下拉框清除按钮行为一致；
- 继续使用 6.7.0 的 ConfigProvider 全局配置能力，减少页面级样式差异。

## 体验增强

- 订阅用户、订阅模板分页支持 `info.recommendPage`：
  - 切换每页条数时尽量停留在当前数据位置，不再直接跳回第一页；
- 邮件 DNS 向导表格启用 `expandable.showExpandAll`：
  - 可一键展开/收起全部记录详情。

## 升级后回归

- `node scripts/build.mjs` 成功；
- `node scripts/render-check.mjs` 执行完成，重点页面无运行时错误；
- server 构建、测试全绿；
- 145 处 Table、79 处 Modal、232 处 Form、48 处 Select、408 处 Button 均正常渲染；
- 5 套主题截图验收：
  - 默认、翠绿、极简、扁平、毛玻璃；
  - 订阅模板表：表头、行、分页、按钮、Tag、搜索框正常；
  - 系统设置表单：Input、Switch、Tabs、Alert、保存按钮正常；
  - 管理账号改名 Modal：遮罩、圆角、输入框、按钮跟随主题；
- 面板 healthz V1.6.2，15/15 节点在线；
- 全量发布 dist-release 更新 V1.6.2。

## 注意

- AntD 6.7 将 Alert 容器圆角 token 从 `borderRadius` 调整为 `containerBorderRadius`；当前主题未自定义 Alert 圆角，无影响；
- `suffixIcon` 等 API 进入渐进废弃流程，后续如使用应迁移到 `suffix`；
- 浏览器需强制刷新以加载新 `admin-app.js`。