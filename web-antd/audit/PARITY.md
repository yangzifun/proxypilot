# 新旧后台对照报告 · Ant Design 版

> 对照基准：`workers/worker.js` 第 6942–12233 行（老后台 `adminHtml`）
> 新实现：`web-antd/src`（Ant Design 5 + React 18）
> 更新：2026-10-28 · **第六轮：节点管理去掉「下发模板」页签（仅保留 协议模板 / 节点绑定）**

## 0. 测试结果

| 套件 | 覆盖 | 结果 |
|---|---|---|
| `scripts/smoke.mjs` | Worker 路由 + `/assets/world.svg` + 产物 + ETag + 回退 + 透传 + `scheduled` | **21 / 21** |
| `scripts/render-check.mjs` | 9 页 + 登录页真实渲染 | **37 / 37** |
| `scripts/e2e.mjs` | 内存后端 + jsdom 真点真填，**34 个场景** | **70 / 70** |

E2E 覆盖：登录（密码 / TOTP 两步 / **备份码** / 校验拦截）、概览、分组增改删查、搜索、模板新建/设默认、接入服务器、看密钥、**检测全部**、编辑服务器、建 API 密钥、**编辑密钥权限**、日志清理、MFA 全流程、监控地图与详情、**服务重启**、**通知设置**、**邮箱绑定**、一键下发、单节点下发、**批量推送**、模板创建/导入、绑定节点、授权节点、外部订阅源、**下发模板**、非超管 403。

### 测试过程中发现并修复的真实缺陷

| # | 缺陷 | 修复 |
|---|---|---|
| 1 | API 密钥默认权限在 Modal 打开前设置，表单未挂载导致丢失 | `<Form initialValues>` + `preserve={false}` |
| 2 | 7 处 `validateFields()` 未捕获，校验失败抛未捕获拒绝 | 统一 `readForm()` |
| 3 | 「从配置导入」页签 Form 实例未绑定，请求体全空 | 改受控 state + 显式校验 |
| 4 | 服务器页引用了未声明的 `busy` 状态 → **整个页面渲染崩溃** | 补 `useState('')` |
| 5 | 首轮：JWT 解析依赖 `TextDecoder`、Turnstile 在插槽挂载前渲染 | `decodeURIComponent`；回调 ref |

## 1. 接口覆盖：37 / 37 ✅

老后台调用的全部接口族均已在新前端落地，包含最后一轮补齐的：

- `node-templates`（下发模板 CRUD，含占位符提示）
- `servers/:id/test`（单台检测 / 检测全部）
- `agent/tasks/:id` DELETE（取消任务）
- `apikeys/:id` PUT（权限编辑）+ 前端生成的调用示例
- `bucket/file` PUT（新建文件）
- `mfa/login-with-backup`（备份码登录）

## 2. 页面状态

| 页面 | 状态 | 说明 |
|---|---|---|
| 系统概览 | ✅ | 指标卡 / 趋势面积图 / 构成环形图 / Top10 / 最新订阅 |
| 订阅分组 | ✅ | 列表（排序·搜索·状态筛选）+ 增改删 + 详情四页签（节点配置写操作、授权节点、外部订阅源）+ 批量推送 |
| 订阅模板 | ✅ | CRUD / 设为默认 / 对象存储浏览·编辑·**新建文件** |
| 节点管理 | ✅ | 协议模板（**JSON 编辑器：引擎开关 + sb.json/xray.json/masque.json + 实时解析结果 + 生成当前配置/填入示例**，保存走 `templates/from-config`）+ 节点绑定 CRUD + 单节点下发 + 一键下发 + 一致性巡检 |
| 服务器管理 | ✅ | 列表/接入/编辑/删除/密钥/轮换/任务（可取消）/检测（单台·全部）+ 详情抽屉（资源折线、服务状态、下发配置、原始指标、Agent 升级） |
| 服务器监控 | ✅ | KPI / 在线率 / 资源仪表盘 / 告警 / **世界地图** / 资源柱状图 / 明细表 / 服务矩阵 / 详情抽屉 / 服务重启 / 通知设置 / 关注服务过滤 |
| 系统管理 | ✅ | 账号 CRUD / 重置密码 / **邮箱绑定验证** / MFA 重置；API 密钥 CRUD / **权限编辑** / **调用示例** / 轮换 / 启停 |
| 安全中心 | ✅ | 改密 / MFA 全流程 / **我的邮箱绑定验证解绑** |
| 登录页 | ✅ | 账号密码 + TOTP / **备份码** / 邮箱验证码 + 动态码·备份码 / OIDC / Turnstile |
| 用户端 `/`、`/manage` | ⏸ | 未重做，仍由 `worker.js` 原样提供 |

## 3. 剩余可选项（都非阻塞）

1. 用户端 `/` 与配置页 `/manage` 的 Ant Design 重做（本次只做后台）
2. 订阅模板列表排序（接口支持 `sort`，UI 未接；订阅分组已接）
3. 全局任务队列视图（当前是单机任务 + 取消）
4. Bucket 文件删除按钮（`DELETE /api/bucket/file` 已有，UI 未加）

> 协议模板编辑器已按老后台改为 JSON 驱动：端口基线、WARP 基线 / 开关 / 凭据、证书路径、REALITY 参数、SS 密钥等 20+ 个参数字段全部从界面移除，
> 统一由后端 `from-config` 从 JSON 反推；「生成当前配置 / 填入示例」由后端渲染，保证密钥真实随机、格式合法。
>
> **节点管理已去掉「下发模板」（`node-templates`）页签** —— 该功能界面不再提供，接口仍在（如需可挪到「服务器管理」下再开）。
>
> **老版后台页面已下线**：`worker-antd.js` 不再有 `/admin-classic` 路由，`ADMIN_UI_CLASSIC` 回滚开关也已移除，
> `/admin` 始终是 Ant Design 版。`worker.js` 里的 `adminHtml` 代码**原样保留**（未删一行），只是没有路由指向它；
> 需要临时找回老页面时，在 wrapper 里加回一段 pathname 改写即可。

## 4. 复现

```bash
cd web-antd
npm run check     # build + smoke + render + e2e 全跑
npm run preview   # 生成本地预览页
npm run audit     # 重新生成老后台原始清单
```

构建产物：`workers/worker-antd.bundle.js`（**661 KB gzip**，占 Workers 免费额度 64.5%）。
`workers/worker.js` 全程零改动。

原始证据：`audit/old-admin-inventory.md`、`audit/old-ui-actions.md`。
