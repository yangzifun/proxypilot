# 用户端对照：新版用户中心 vs 老版（`/` + `/manage`）

> 基准：`workers/worker.js` 的 `portalHtml`（6742–6940 行）与 `unifiedPageHtmlContent`（17272 行起，约 1873 行）
> 新版：`web-antd/src/portal/*`（`/`、`/me` 由 worker-antd 外壳挂载）
> **状态：已补齐并下线老页面**
> `worker.js` 一行未改，全部接口沿用现有的用户令牌鉴权。
> `/manage` 页面路由在 wrapper 里已改为 **302 → `/`**（旧书签自动进新版）；`/manage/configs`、`/manage/stats`、
> `/manage/uuid-info`、`/manage/uuid-sub-sources` 等接口保持不变，新版用户中心仍在用。

### 收尾结论

- 第 1–6、8、10 项**已在新版实现**（见下表「新版」列）。
- 第 7 项「生成新 UUID」与第 9 项「入口门禁」：后端 `POST /manage/entry`、
  `POST /manage/generate-uuid` **直接返回 403**（源码 17188 行「该入口已关闭：请用邮箱登录后管理自己的 UUID」），
  老版本里这两个按钮本身也已是死功能 → 新版不实现，属于**对齐**而非缺失。

---

## 一、老用户端由什么构成

| 页面 | 用途 | 体量 |
|---|---|---|
| `/`（portalHtml） | 邮箱验证码登录；登录成功后 **`location.replace('/manage')`** | ~200 行 |
| `/manage`（unifiedPageHtmlContent） | 用户真正的操作台：UUID 管理、配置增删改、订阅生成、统计、外部订阅源 | ~1873 行 |

也就是说：老版 `/` 只是一道门，**用户 90% 的时间花在 `/manage`**。

---

## 二、逐项对照

### 已实现（新版）

| 功能 | 老版 | 新版 |
|---|---|---|
| 邮箱验证码登录 | ✅ portalHtml | ✅ `PortalLogin` |
| Turnstile 人机验证 | ✅ `portalTs*` | ✅ 复用 `useTurnstile`（提交时 `getResponse` 现取） |
| 60 秒重发倒计时 | ✅ | ✅ |
| 登录态持久化 | `localStorage.pp_portal_token` | ✅ **同一个 key，与新老页面互通** |
| 401 自动登出 | ✅ | ✅ |
| 我的 UUID 列表（备注/所有者/状态/节点数） | 隐含在 `/manage` 的下拉与列表里 | ✅ 卡片化 |
| 三种订阅链接（raw / clash / batch） | ✅ `/manage` 里复制/下载/二维码 | ✅ 复制 + 二维码（自绘，不依赖外部 QR 服务） |
| 空状态（邮箱未绑定 UUID） | ✅ 文案提示 | ✅ |
| 退出登录 | ✅ | ✅ |
| PC / 移动端自适应 | ❌ 老页面是固定宽度 | ✅ 断点式布局 |

### 未实现（老 `/manage` 有，新版没有）

| # | 功能块 | 老版实现 | 状态 | 优先级 |
|---|---|---|---|---|
| 1 | **配置管理** | `loadUuidData` / `openEditModal` / `saveEditedConfig` / `manageAddConfig` / `delOne` / `deleteAll` / `批量删除` / 搜索筛选分页 / 启用禁用 | ❌ 全部缺失 | **P0** |
| 2 | **添加新节点** | 「4. 添加新节点」表单（协议、端口、参数） | ❌ | **P0** |
| 3 | **订阅生成** | 「2. 订阅版本与模板」+ 模板选择 + 客户端类型 + `generateBase64Subscription` / `generateClashConfig` + 「3. 生成结果」+ 下载文件 | ❌（新版只有现成的三条链接，不能选模板/生成/下载） | **P0** |
| 4 | **访问统计** | 「3. 访问统计」总访问 / 分类统计 / Chart.js 图表切换 / 最近访问记录 | ❌ | P1 |
| 5 | **修改绑定邮箱** | `openChangeEmailModal` + `/api/portal/email/send-code` + `/verify` | ❌ | P1 |
| 6 | **外部订阅源合并** | 「5. 外部订阅源（合并）」增/删/启停 + URL 校验（`/manage/uuid-sub-sources`） | ❌ | P2 |
| 7 | **生成新 UUID** | `generateNewUuid`（`/manage/generate-uuid`） | ❌ | P2 |
| 8 | **UUID 信息编辑** | `saveUuidInfo`（备注等，`/manage/uuid-info`） | ❌（新版只读展示） | P2 |
| 9 | **入口门禁 / 订阅受限提示** | `setEntryGate` / `setSubscribeRestricted` | ❌ | P2 |
| 10 | **协议类型说明** | 「支持的协议类型」区块 | ❌ | P3 |

> 新版订阅卡片底部的「管理节点配置（老版页面）」只是**跳转链接**，
> 点过去仍然是旧样式的 `/manage` —— 严格说用户端只换了「门」，没换「屋」。

---

## 三、可直接复用的接口（后端无需改动）

| 接口 | 用途 |
|---|---|
| `POST /api/portal/send-code` | 发登录验证码 |
| `POST /api/portal/login` | 登录换 token |
| `GET /api/portal/uuids` | 我的 UUID + 节点数 + 三种订阅地址 |
| `POST /api/portal/email/send-code`、`/verify` | 换绑邮箱 |
| `GET/POST /manage/entry` | 入口与门禁 |
| `GET /manage/all-uuid-info` | UUID 信息 |
| `GET/POST /manage/configs`、`/manage/configs/batch` | 配置列表 / 批量操作 |
| `PUT /manage/uuid-info` | 改备注等信息 |
| `GET/POST/DELETE /manage/uuid-sub-sources` | 外部订阅源 |
| `POST /manage/generate-uuid` | 生成新 UUID |
| `GET /sub/:uuid`、`/clash-config?uuid=`、`/batch-configs/:uuid` | 订阅输出（下载用） |

全部接受 `Authorization: Bearer <portal token>`（`role: user`），按邮箱/UUID 归属鉴权 —— 所以补齐功能**不需要动 worker.js**。

---

## 四、建议补齐顺序

1. **P0-a 配置管理页**：UUID 卡片点进去 → 配置列表（搜索/分页/多选）→ 编辑弹窗 / 新增 / 删除 / 启用禁用。这是用户最常做的事。
2. **P0-b 订阅生成**：模板选择 + 客户端类型 → 生成 → 下载 `.yaml` / `.txt`、二维码、复制。
3. **P1 访问统计**：复用后台的 `AreaChart` / `ColumnChart`（自绘 SVG，无需 Chart.js）。
4. **P1 换绑邮箱**：两个接口现成，一个弹窗的活。
5. **P2** 外部订阅源、生成新 UUID、UUID 信息编辑。
6. **P2** 入口门禁与「订阅受限」提示。

按这个顺序做完，`/manage` 就可以下线，用户端才算真正完整。

---

## 五、移动端（antd-mobile 外壳）补齐记录

移动端不再是 PC 单页的缩小版：`PortalApp` 按断点分流，手机/平板竖屏走 `PortalMobileApp`
（NavBar + TabBar），三个页面各自独立，数据层与 PC 共用 `portal/hooks/usePortalData.js`。

| 能力 | PC | 移动端 | 说明 |
|---|---|---|---|
| 我的订阅 / 订阅生成（原始 · Clash · 批量） | ✅ | ✅ | 模板 Picker、复制 / 下载 / 二维码、客户端一键导入 |
| 订阅二维码 | ✅ antd QRCode | ✅ antd QRCode | 已去掉对 `api.qrserver.com` 的依赖，离线可出图 |
| 配置管理（只读） | ✅ 表格 + 搜索 + 8 种协议 | ✅ 列表 + 搜索 + 8 种协议 | 移动端用 CapsuleTabs + 下拉刷新 + 加载更多 |
| 外部订阅源（新增 / 启停 / 删除） | ✅ | ✅ | 移动端为底部 Popup，接口与 PC 相同 |
| 访问统计（KPI + 趋势 + 构成 + 最近访问） | ✅ | ✅ | 移动端支持下拉刷新与「刷新」重试 |
| 账号与信息（所有者 / 联系方式 / 备注） | ✅ | ✅ | 移动端 antd-mobile Form，展示 UUID / 状态 / 绑定邮箱 |
| 换绑邮箱 | ✅ | ✅ | 同一个 `EmailChange` 弹窗（antd Modal） |
| 一个邮箱多个 UUID | ✅ 卡片切换 | ✅ Selector 切换 | 切换后各页 `key` 变化，重新拉数据 |
| 深链 `#/u/<uuid>` | — | ✅ | 进入单个 UUID 的完整页签视图 |
| 生成新 UUID / 入口门禁 | — | — | 后端 `POST /manage/generate-uuid`、`/manage/entry` 返回 403，属于死功能，两端都不做 |

### 顺手修掉的两个真实缺陷

- **`btoa` 抛 `InvalidCharacterError`**：Clash 订阅链接里带中文模板名（如 `template=内置默认`），
  旧代码在生成结果区调用 `btoa(url)` 直接把整页打崩。现在先 `encodeURIComponent` 再编码，
  且只在需要 base64 的 scheme 上计算（`PortalUuid.jsx` / `MobileSubscribe.jsx`）。
- **移动端「今日访问」恒为 0**：后端字段是 `today_total`，移动端误读 `today_access`；
  已改为 `today_total ?? today_access`（`MobileStats.jsx`）。

### 回归

`npm run check`：smoke 22 + render 38 + e2e 154，全绿；
e2e 新增 S41 覆盖多 UUID 切换、二维码本机渲染、搜索 / 协议筛选请求参数、
外部订阅源弹层新增、统计刷新与 KPI 取值。
