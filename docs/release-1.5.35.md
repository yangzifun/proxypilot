# ProxyPilot 1.5.35 发布说明（新建域名列表 + 修复改名崩溃）

日期：2026-11-22
版本：面板 V1.5.35 / Agent 1.4.2 / VPN 客户端 1.4.1

## 一、域名列表支持新建

- 模板列表页下方「域名列表（rule/*.list）」卡片新增「新建列表」按钮；
- 弹窗输入文件名，自动补 `.list`，校验：
  - 1–64 位 `[A-Za-z0-9._-]`；
  - 不能与现有列表重名；
- 可填写初始内容，默认 `# <名称> 域名列表`；
- 保存时直接写入 `rule/<name>.list`（PUT `/api/attachments/content`，`text/plain`）；
- 成功后刷新列表并自动打开 List 编辑器选中新文件，便于继续添加域名；
- 不提供删除，避免误删。

## 二、修复修改管理员用户名时 React #300 崩溃

根因：

- `web-antd/src/App.jsx` 中主题 `useState/useEffect` 写在 `if (!token) return <Login />` 条件返回之后；
- 修改当前登录账号或根超管用户名后触发退出登录，App 提前返回登录页，导致同一组件 hooks 数量变化，触发 `Rendered more hooks than during the previous render`（minified React error #300）。

修复：

- 将主题 hooks 移到条件 return 之前，保证登录/未登录渲染的 hooks 顺序一致；
- 未改后端 API、改名参数、权限与 Modal 行为；
- 覆盖验证：
  - 当前/根超管改名成功 → 重新登录，无 #300；
  - 普通管理员改名成功 → 不退出登录，无 #300；
  - 改名失败 → 停留页面并提示，无 #300。

## 验证

- 面板 healthz V1.5.35，15/15 节点在线；
- 新建列表 API 实测：
  - 创建 `rule/tmp-verify.list` 成功；
  - 列表可读取，内容为 text/plain；
  - 测试后已删除；
- 管理员改名 React #300 在 jsdom 中先复现后修复，15/15 通过；
- 当前根超管用户名为 `yang`，`/api/auth/me` 返回 `is_root=true/is_super=true`；
- DoH 外部请求仍正常；
- server 测试全绿，web-antd 构建成功；
- 全量发布 dist-release 更新 V1.5.35。

## 注意

- 新建列表需要 R2 配置和写权限；
- 新建后列表立即生效，引用它的模板无需重新保存；
- 根超管改名后需用新用户名重新登录，本次实际用户名为 `yang`；
- 旧管理员改名辅助脚本已改为从数据库读取 root 用户名。