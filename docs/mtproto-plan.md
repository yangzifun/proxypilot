# ProxyPilot 接入 MTProto（Telegram 专用）部署方案

> **实施状态**：已按本方案落地（V13.3，`workers/test_mtproto_api.mjs` 120 项通过）；V13.4 增补「模板 + 节点」两级：新增 `mtproto_templates`（监听端口 13709 收在模板里，不走基线+偏移），节点只存覆盖项，模板可编辑并一键下发
>
> 目标版本：V13.3 · 范围：节点管理新增 MTProto 部署、按用户授权、用户端二维码 / 邮件交付
> 结论先行：**配置与链接独立成表，绝不写入 `configs`**，因此 `/sub`、`/batch-configs`、`/clash-config` 三条订阅出口天然不含 Telegram 链接，无需改动订阅生成器。

---

## 0. TL;DR

| 项 | 决策 |
| --- | --- |
| 引擎 | **mtg-multi**（[9seconds/mtg](https://github.com/9seconds/mtg) 的多 secret 分支）：单进程、每用户独立 secret、`SIGHUP` 热加载、可选 per-user 统计 |
| 降级 | 同一二进制支持 `single` 模式（只有 `secret = "..."`，等价官方 mtg），配置项 `secret_mode` 切换 |
| 数据 | 新增 `mtproto_nodes`（部署）+ `mtproto_clients`（授权 / 凭据），**不碰 `configs` / `uuid_info` 结构** |
| 下发 | 复用 `node_configs` + `agent_tasks` 管道，产物名 `mtpx:<node>:mtg`，Agent 只需新增「装 mtg-multi」 |
| 授权 | 一个 UUID → 一个独立 secret；撤销 = 删条目 + reload，**只断该用户，其他人不掉线** |
| 用户端 | `tg://proxy?...` 链接：门户内 antd `QRCode` 本机渲染 + 一键发邮件（复用 `sendMail` / `emailLayout`） |
| 端口 | TCP，模板里显式配置（默认 `13709`），无需证书、无需域名（FakeTLS 的域名只写在 secret 里） |

---

## 1. 需求拆解与现状盘点

### 1.1 需求 → 能力映射

| 需求 | 现有可复用 | 缺口 |
| --- | --- | --- |
| 后台节点管理里部署 MTProto | `singbox_templates` / `singbox_nodes` 的两级结构、`node_configs` 版本化、Agent deploy | 无 MTProto 渲染器、无 mtg 安装能力 |
| 授权给用户 | `singbox_nodes.uuid_list` + `singboxUsersWithUuids()` 的「UUID → 凭据」思路 | MTProto 需要**每用户独立 secret**，不能共用 |
| 不通过订阅下发 | `/sub` 只读 `configs`（`handleRawSubscription` → `fetchConfigsByUuidFromDB`） | 只要不写 `configs` 即自动满足 |
| 配置单独存表 | — | 新增两张表 |
| 用户端二维码 | `PortalUuid.jsx` / `PortalHome.jsx` 已在用 `antd` 的 `QRCode`（`Security.jsx:342` 有静默区注意事项） | 新增数据源与卡片 |
| 发送到邮箱 | `sendMail()`（`worker.js:805`）、`emailLayout()`（`:818`）、监控告警的用法（`:9314`） | 新增邮件模板与限流 |

### 1.2 关键代码位置（改造锚点）

| 位置 | 作用 |
| --- | --- |
| `workers/worker.js:4099` | `/api/singbox/*` 路由挂载点，MTProto 路由挂在其后 |
| `workers/worker.js:8493` `handleSingboxApi()` | 模板 / 节点 / 预览 / 授权的参考实现 |
| `workers/worker.js:7657` `singboxRenderBundle()` | 「模板 + 节点 → 文件列表」的渲染范式 |
| `workers/worker.js:7749` `singboxApplyNode()` | 落 `node_configs`（内容变了才 `version+1`） |
| `workers/worker.js:7835` `singboxQueueDeployFiles()` | 排队 deploy 任务 |
| `workers/worker.js:10145` `agentBuildDeployPayload()` | 生成 Agent 载荷（path / content / sha256 / restart_cmd） |
| `agent/internal/runner/runner.go:141` `deploy()` | 先校验后替换、`check_cmd`、`restart_cmd` |
| `agent/internal/runner/component.go:49` `ensureComponent()` | **自动安装白名单**：现在只有 sing-box / xray |
| `agent/internal/metrics/metrics.go:90` `defaultServices()` | 服务监控名单：`sing-box / xray / cloudflared` |
| `workers/worker.js:6637` | 服务状态卡片白名单（与服务端二次过滤呼应） |
| `web-antd/src/pages/Nodes.jsx:360` | 节点管理的 `Tabs`，新增第三个页签 |
| `web-antd/src/portal/PortalUuid.jsx:335` | 用户中心的页签定义，新增「Telegram 代理」 |

---

## 2. 关键决策

### 2.1 引擎选型

MTProto 的凭据模型是「**secret 即身份**」：客户端首包用 secret 做 HMAC，服务端据此放行。因此「按用户授权」= 「每用户一个 secret」。

| 方案 | 多用户 | 热更新 | 代价 / 风险 | 结论 |
| --- | --- | --- | --- | --- |
| **mtg v2（官方）** | ❌ 单 secret | 只能重启 | 撤销只能整节点轮换，一个人泄露全员换链接 | 仅作降级模式 |
| **mtg-multi**（上游 README 对「需要多 secret」的人点名推荐的 fork） | ✅ `[secrets]` 命名表 | ✅ `SIGHUP` 只增删改 secret，未变更用户会话不断 | fork 维护性；需锁版本 + 自建镜像 | **采用** |
| mtprotoproxy（Python） | ✅ `USERS` 字典 | 需重启 | 引入 Python 运行时，破坏「单二进制 + systemd」模型 | 不采用 |
| 一用户一进程（官方 mtg） | ✅ | 重启单实例 | N 个用户 N 个进程 / N 个端口，内存与端口管理失控 | 不采用 |
| 自研 multi-secret 前端 | ✅ | — | 数据面自研，风险与工期都不划算 | 不采用 |

选 mtg-multi 的另外两个理由：
1. **配置格式与官方 mtg 完全兼容**（只多一个 `[secrets]` 段），降级只是渲染分支不同，二进制不变；
2. 自带 `/stats`（按用户连接数 / 流量 / 最后活跃），后续做「谁在用」有现成数据源。

> 参考：[mtg README（单 secret 的设计取舍）](https://github.com/9seconds/mtg)、[mtg-multi（多 secret / SIGHUP / stats）](https://github.com/dolonet/mtg-multi)、[mtg 配置样例](https://github.com/9seconds/mtg/blob/master/example.config.toml)。

### 2.2 授权粒度：一个 UUID = 一个 secret

- 授权 = 在 `mtproto_clients` 里为 `(node_id, uuid)` 建一行并生成 secret；
- 撤销 = 该行 `status='revoked'`、清空 secret，重渲染后 `SIGHUP`，mtg-multi 会**主动关闭该 secret 的活跃会话**；
- 轮换 = 换 secret 写回，效果同上，只影响本人；
- `[secrets]` 的 key 用 `c<client.id>`（例如 `c17`），**必须稳定**——改名等价于「删旧增新」，会误断会话。

### 2.3 与订阅体系的隔离（硬约束）

```
mtproto_nodes ──渲染──▶ node_configs(name=mtpx:xxx:mtg) ──Agent──▶ /etc/mtg/config.toml
mtproto_clients ─┘                                                  │
                                                                    └─ SIGHUP 生效

configs 表 ✗ 不写入        /sub ✗ 不读取        /clash-config ✗ 不读取
```

落实手段：
1. 代码上**不存在**任何 `INSERT INTO configs` 的 MTProto 分支；
2. `singboxSyncUuidConfigs()`（`:7977`）一行不改；
3. 新增回归测试：授权 3 个 UUID 后断言 `SELECT COUNT(*) FROM configs WHERE protocol='mtproto'` 与包含 `tg://` 的行数均为 0，且 `/sub/<uuid>` 响应里不含 `tg://`。

### 2.4 端口与网络

| 项 | 值 | 说明 |
| --- | --- | --- |
| 端口 | 模板里的 `port`（默认 `13709`） | 端口是模板的显式配置，不走「基线 + 偏移」；节点可覆盖 |
| 传输 | TCP | 无需 UDP、无需证书；防火墙只需放行 1 个 TCP 端口 |
| 伪装域名 | `node.domain`（如 `storage.googleapis.com`） | 只写进 secret，用于 FakeTLS 的 SNI 与 domain fronting；**不需要在本机签证书** |
| 统计端口 | `127.0.0.1:13719`（模板可改） | `api-bind-to` 只绑本机，不对公网暴露 |

> 注意：secret 内嵌域名。换域名不会让旧 secret 失效（旧 secret 仍带旧域名），但**新发的链接会用新域名**，页面需提示。

---

## 3. 数据模型

新增两张表，全部走 `ensureServerTables()` 的 `CREATE TABLE IF NOT EXISTS` 兜底 + `init_database.sql` 声明（与 `singbox_*` 同款做法）。

```sql
-- ========================================================
-- 15. MTProto（Telegram 专用；与订阅体系完全隔离）
-- ========================================================
CREATE TABLE IF NOT EXISTS mtproto_nodes (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id        INTEGER NOT NULL UNIQUE,      -- 一台服务器一个 MTProto 节点
    name             TEXT UNIQUE NOT NULL,
    domain           TEXT,                          -- 伪装域名（写进 secret 的 SNI）
    port             INTEGER DEFAULT 13709,
    api_port         INTEGER DEFAULT 13719,         -- mtg-multi /stats，仅绑 127.0.0.1
    secret_mode      TEXT DEFAULT 'multi',          -- multi（每用户）| single（整节点共享）
    engine           TEXT DEFAULT 'mtg-multi',      -- 预留：以后可换实现
    config_path      TEXT DEFAULT '/etc/mtg/config.toml',
    service_name     TEXT DEFAULT 'mtg',
    concurrency      INTEGER DEFAULT 8192,
    prefer_ip        TEXT DEFAULT 'prefer-ipv4',
    public_ipv4      TEXT,                          -- 留空则由 mtg 自动探测（探测可能被墙）
    blocklist        INTEGER DEFAULT 0,             -- 1 = 启用 firehol 黑名单（家宽/内网会误杀）
    throttle_max     INTEGER DEFAULT 0,             -- >0 时写入 [throttle]
    bootstrap_secret TEXT,                          -- clients 为空时的占位 secret，保证服务可启动
    enabled          INTEGER DEFAULT 1,
    last_apply_at    INTEGER DEFAULT 0,
    last_render_hash TEXT,
    remark           TEXT,
    created_at       INTEGER DEFAULT (CAST(STRFTIME('%s','now') AS INT)*1000),
    updated_at       INTEGER DEFAULT (CAST(STRFTIME('%s','now') AS INT)*1000)
);

CREATE TABLE IF NOT EXISTS mtproto_clients (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id          INTEGER NOT NULL,             -- mtproto_nodes.id
    uuid             TEXT,                          -- 授权对象（平台 UUID）；NULL = 未绑定
    email            TEXT,                          -- 冗余：授权时的邮箱，便于后台检索
    label            TEXT,                          -- 备注 / 昵称
    secret           TEXT,                          -- ee<32hex><domain hex>；撤销后清空
    status           TEXT DEFAULT 'active',         -- active | revoked
    delivered_at     INTEGER DEFAULT 0,             -- 首次交付（二维码被看 / 邮件发出）
    deliver_count    INTEGER DEFAULT 0,
    last_mail_at     INTEGER DEFAULT 0,             -- 门户发信限流
    mail_count_today INTEGER DEFAULT 0,
    mail_day         TEXT DEFAULT '',               -- 'YYYY-MM-DD'，跨天清零
    revoked_at       INTEGER DEFAULT 0,
    remark           TEXT,
    created_at       INTEGER DEFAULT (CAST(STRFTIME('%s','now') AS INT)*1000),
    updated_at       INTEGER DEFAULT (CAST(STRFTIME('%s','now') AS INT)*1000),
    UNIQUE(node_id, uuid)                          -- 同节点同 UUID 只有一条授权
);

CREATE INDEX IF NOT EXISTS idx_mtproto_clients_uuid ON mtproto_clients (uuid);
CREATE INDEX IF NOT EXISTS idx_mtproto_clients_node ON mtproto_clients (node_id, status);
```

设计说明：

- **为什么保留 `revoked` 行而不是删除**：审计（谁在什么时候被授权 / 撤销）+ `UNIQUE(node_id, uuid)` 天然防重复授权；重新授权 = 同一条记录 `status='active'` + 新 secret。
- **不建模板表**：MTProto 的部署参数只有「域名 / 端口 / 并发 / 黑名单」几项，两级模板在这个场景是纯负担。批量部署时从 `app_settings.mtproto_defaults`（JSON）取默认值即可。
- **不建投递日志表**：邮件 / 查看二维码都写 `node_push_logs`（`action='mtproto-mail'｜'mtproto-deliver'`），复用现有日志页与清理任务。
- secret 明文落库，与 `singbox_templates.ss_server_key` / `reality_private_key` 保持一致的口径；列表接口默认打码（`ee12****cdef`），`?reveal=1` 才明文并写日志。

---

## 4. 配置渲染

### 4.1 secret 规范

```
secret = "ee" + hex(16 字节随机) + hex(域名 ASCII)
例：ee367a189aee18fa31c190054efd4a8e + 73746f726167652e676f6f676c65617069732e636f6d
    └── 16 字节 ──┘                     └── storage.googleapis.com ──┘
```

- 只支持 FakeTLS 形态（`ee` 前缀），mtg v2 已禁用 legacy 明文模式；
- 域名统一转小写、去掉首尾空格与端口；非 ASCII 域名拒绝（IDN 请先转 punycode）；
- 生成函数 `mtprotoGenerateSecret(domain)` 放在 Worker，随机源用 `crypto.getRandomValues`（与 `singboxRandomBytes` 同源）；
- Base64 形态 Telegram 也接受，但统一用 hex，避免链接里出现 `+ / =` 的转义问题。

### 4.2 TOML 渲染（multi 模式）

```toml
# /etc/mtg/config.toml — 由 ProxyPilot 渲染，请勿手改
bind-to = "[::]:13709"
api-bind-to = "127.0.0.1:13719"
prefer-ip = "prefer-ipv4"
concurrency = 8192
public-ipv4 = "203.0.113.10"

[domain-fronting]
port = 443

[defense.blocklist]
enabled = false

[throttle]
max-connections = 5000

# ⚠️ [secrets] 必须是最后一个 section（TOML 语义：section 之后的键都归属该表）
[secrets]
c17 = "ee..."
c23 = "ee..."
```

渲染规则（**必须确定性**，否则 `node_configs.version` 会被无意义地 +1）：

1. 客户端按 `id ASC` 排序输出；
2. 布尔 / 数字统一格式化，不带尾随空格；
3. 空 `clients` 时输出 `c0 = "<bootstrap_secret>"`，保证 mtg 能启动；首个真实授权落地后自动移除；
4. 注释头里写 `# node: <name> · clients: <n>`，便于 `journalctl` / 人工排查时对号。

### 4.3 single 模式（降级）

```toml
secret = "ee..."          # 来自 mtproto_nodes.bootstrap_secret（整节点共享）
bind-to = "[::]:13709"
...
```

此模式下所有授权用户拿到的是**同一条链接**，「撤销某个用户」实际是「轮换整节点 secret」，UI 需明确提示。

### 4.4 与 `node_configs` 的对接

| 字段 | 值 |
| --- | --- |
| `name` | `mtpx:<node.name>:mtg` |
| `engine` | `mtproto`（新值；`buildNodeDeploy` 会回落到 `other`，无副作用） |
| `target_path` | `node.config_path` |
| `service_name` | `mtg` |
| `check_cmd` | `mtg-multi version`（见 §5：让 Agent 的「缺组件自动安装」被触发） |
| `restart_cmd` | `systemctl enable mtg >/dev/null 2>&1; systemctl reload mtg 2>/dev/null \|\| systemctl restart mtg` |

`restart_cmd` 的语义：优先 `reload`（SIGHUP，不断线），老 systemd 单元没有 `ExecReload` 时回落 `restart`（断线重连）。Agent 的 `deploy()` 只有在内容变化时才走到 restart，未变化直接短路返回。

---

## 5. Agent 侧改造

### 5.1 自动安装 mtg-multi

`component.go` 的白名单现在只有 `sing-box` / `xray`，新增：

```go
case "mtg-multi":
    if code, out := r.installMtg(ctx); code != 0 { return code, out }
    r.ensureUnit("mtg", "/usr/local/bin/mtg-multi run /etc/mtg/config.toml")
    return 0, "已自动安装 mtg-multi"
```

- 下载源：优先 `AGENT_BINARY_BASE_URL` 镜像（`https://s3.proxypilot.yangzifun.org/agent/mtg-multi/<ver>/...`），失败回落 GitHub Release；
- 校验：沿用 `sha256`（Release 自带的 `SHA256SUMS` 或清单里写死）；
- 版本用变量 `mtgVersion`，与 `singboxVersion` / `xrayVersion` 同风格，方便热修。

> **待验证**：mtg-multi Release 资产命名（预期 `mtg-multi_<ver>_linux_<arch>.tar.gz`，内含单二进制）。实现前先 `curl -s <release API>` 对齐，或直接自建一次构建上传到 S3，彻底摆脱上游命名变化。

### 5.2 systemd 单元

`ensureUnit()` 目前只写最小单元。为 `reload` 生效需要 `ExecReload`：

```ini
[Service]
ExecStart=/usr/local/bin/mtg-multi run /etc/mtg/config.toml
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=3
```

改造点：
1. `ensureUnit` 增加可选 `execReload` 参数；
2. 已存在的单元若缺 `ExecReload`，**幂等补写**（读文件、不含则插入、`daemon-reload`）；
3. 不做 `DynamicUser`（要与 `bind-to` 低端口 / 读 `/etc` 保持一致，直接 root，与现有 sing-box 单元口径一致）。

### 5.3 服务监控

- `agent/internal/metrics/metrics.go:90` `defaultServices()` → 追加 `"mtg"`；
- `workers/worker.js:6637` 白名单 → 追加 `"mtg"`；
- 老 Agent 不上报该键，服务端已做 `!== undefined` 过滤，不会出现空卡片；
- 需要发 **Agent 1.4.0**（`agent/dist/manifest.json` + 两个平台的二进制重发）。

### 5.4 为什么 `check_cmd` 用 `mtg-multi version`

`ensureComponent` 的触发条件是「`check_cmd` 的第一个词在 PATH 里找不到」。若 `check_cmd` 写成 `sh -c '...'`，`firstCommandWord()` 会跳过 shell 前缀直接返回空，自动安装就永远不会触发。用 `mtg-multi version` 同时满足「二进制存在性检查」与「触发安装」两件事；配置语法错误则由 `systemctl restart` 的非零退出码兜住（Agent 会把 `[check]/[restart]` 输出回传到任务日志）。

---

## 6. Worker 后端

### 6.1 路由

在 `worker.js:4099` 之后挂载，权限与 `/api/singbox/*` 同级（**后台管理员均可用，非超管亦可**，与现有节点管理一致）：

```js
if (path === '/api/mtproto' || path.indexOf('/api/mtproto/') === 0) {
    return await handleMtprotoApi(req, env, url, path, method, user);
}
```

Portal 侧在 `4029-4033` 的 `/api/portal/*` 区块追加两条（`role=user` 令牌只能走这里，见 `4066` 的拦截）。

### 6.2 管理接口

| 方法 | 路径 | 入参 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/mtproto/nodes` | — | 列表：节点 + 客户端数 + 服务状态（取 `servers.last_metrics.services.mtg`） |
| POST | `/api/mtproto/nodes` | `server_id,name,domain,port,secret_mode,...` | 建节点；`auto_deploy` 默认 true（渲染 + 排队 deploy） |
| GET | `/api/mtproto/nodes/:id` | — | 详情（含渲染预览） |
| PUT | `/api/mtproto/nodes/:id` | 同上 | 改参数；`auto_deploy` 显式 true 才立即下发 |
| DELETE | `/api/mtproto/nodes/:id` | — | 删节点：清 `node_configs`（`mtpx:<name>:%`）+ 级联删 clients（保留日志） |
| POST | `/api/mtproto/nodes/:id/preview` | — | 返回渲染后的 TOML + 端口 + 授权用户 + 示例链接（不含明文 secret，除非 `reveal`） |
| POST | `/api/mtproto/nodes/:id/apply` | `{push?:bool}` | 重渲染 → `node_configs` → 可选立即排队 |
| GET | `/api/mtproto/nodes/:id/clients` | `?reveal=1` | 授权列表（默认 secret 打码） |
| POST | `/api/mtproto/nodes/:id/clients` | `{uuid\|email, label, send_mail?}` | 授权：生成 secret → 重渲染 → 排队 → 可选发信 |
| POST | `/api/mtproto/clients/:id/rotate` | — | 轮换 secret（只断该用户） |
| POST | `/api/mtproto/clients/:id/revoke` | — | 撤销（`status=revoked`） |
| POST | `/api/mtproto/clients/:id/restore` | — | 重新授权（生成新 secret） |
| POST | `/api/mtproto/clients/:id/send-mail` | — | 管理员代发 |
| GET | `/api/mtproto/uuids` | — | 复用 `singbox/uuids` 的并集查询，供选择器用 |
| POST | `/api/mtproto/push` | `{server_ids, uuids}` | 批量授权（对齐 `singboxPushToServers`：无节点则按默认参数自动建） |
| POST | `/api/mtproto/reconcile` | — | 比对 `mtproto_clients` 与已渲染 `[secrets]`，可自动修复 |

统一响应沿用 `{success, data}` / `{error}`；日志用 `recordNodeLog(env, {serverId, action:'mtproto-*', ...})`。

### 6.3 核心函数（全部加入 `__test__` 导出，见 `worker.js:11399`）

```js
mtprotoGenerateSecret(domain)                 // ee + 16B hex + domain hex
mtprotoParseClients(rows)                     // 过滤 active / 排序 / 生成 [secrets] 键名
mtprotoRenderConfig(node, clients)            // → { content, ports, hash }
mtprotoClientLink(node, server, client)       // → { tg, tme, secret, host, port }
mtprotoApplyNode(env, node, server, opts)     // 渲染 + 写 node_configs（version 语义同 singboxApplyNode）
mtprotoQueueDeploy(env, server, cfgId, actor) // 复用 agentQueueTask
mtprotoAuthorize(env, node, {uuid,email,label})// 授权 + 重渲染 + 排队（+ 可选发信）
mtprotoRevoke(env, node, clientId)            // 撤销 + 重渲染 + 排队
mtprotoSendMail(env, node, client, opts)      // 门户 / 后台共用，含限流
```

### 6.4 链接生成

```js
const host = node.domain || server.last_ip || server.host;   // 连接目标
const qs = `server=${enc(host)}&port=${port}&secret=${enc(secret)}`;
tg  = `tg://proxy?${qs}`;
tme = `https://t.me/proxy?${qs}`;
```

- `server=` **必须是本机可达地址**（Agent 上报的公网 IP，其次 `servers.host`）。`node.domain` 是伪装站域名（解析到 Google 这类真实站点），写进 `server=` 会让客户端去连被伪装的网站——它只该出现在 secret 里；
- 链接里**不要**带 `#name` 之类装饰（Telegram 不识别，且会让复制变长）；
- 二维码内容就用 `tg://` 串（Telegram 客户端可直接识别），`t.me` 串用作文本备用。

### 6.5 与巡检 / 级联删除的衔接

| 场景 | 处理 |
| --- | --- |
| UUID 被删除 | 在 `worker.js:2924 / 3013 / 3277` 三处 `DELETE FROM configs WHERE uuid=?` 旁统一调用 `purgeMtprotoClients(env, uuid)`：将 clients 置 `revoked` → 重渲染受影响节点 → 排队。抽成公共函数，避免三处漂移 |
| 邮箱换绑 | `uuid_info.email` 变更后同步 `mtproto_clients.email`（仅展示用，鉴权仍以 `uuid_info` 为准） |
| 定时巡检 | 可选：把 MTProto 纳入现有 30 分钟巡检（`singboxReconcileUuidState` 的兄弟函数 `mtprotoReconcile`），比对 DB 与 `node_configs` 里的 `[secrets]` 键集合 |
| 节点删除 | 删 `node_configs` + 删 clients + 不动 `server` |
| 备份 | 无需特殊处理；secret 在 D1 内，随库备份 |

---

## 7. 用户端

### 7.1 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/portal/mtproto` | 当前登录邮箱名下的 MTProto 条目：`{id, node_name, host, port, tg, tme, secret_hint, created_at, delivered_at}` |
| POST | `/api/portal/mtproto/:id/deliver` | 记录「已查看二维码」（写 `delivered_at` / `deliver_count`） |
| POST | `/api/portal/mtproto/:id/send-mail` | 发送到**绑定邮箱**（不接受任意收件人），限流 |

归属校验（复用 `portalGateByIds` 的思路）：

```
token.email → uuid_info.uuid → mtproto_clients.uuid AND status='active'
```

任何一步不匹配返回 404（不泄露「存在但无权」）。`role=user` 令牌已经在 `worker.js:4066` 被限制只能访问 `/api/portal/*`。

### 7.2 展示（PC + 移动）

- **PC**：`PortalUuid.jsx` 新增页签「Telegram 代理」；`PortalHome.jsx` 的订阅卡下方加一条轻量入口（有 MTProto 时才渲染）。空态文案：「管理员尚未为你开通 Telegram 代理」。
- **移动**：`MobileSubscribe.jsx` 增加一张卡片，二维码按钮与现有订阅二维码同款交互（参考 `:237` 的 `<QRCode value size={220} type="svg" errorLevel="M" bordered={false} />`）。
- **二维码渲染必须在客户端**（antd `QRCode` 本地生成），不要走 `api.qrserver.com` 之类的第三方，避免 secret 外泄给第三方服务。
- 二维码四周留白 ≥ 4 个模块（`Security.jsx:342` 的踩坑注释），移动端 220px、PC 260px。
- 卡片上同时给「复制 `tg://`」「复制 `https://t.me/...`」「发送到邮箱」三个动作，以及一行安全提示：**链接即凭证，请勿转发；泄露请联系管理员重置**。

### 7.3 邮件

复用 `emailLayout()`，结构：

```
标题：你的 Telegram 代理已就绪
正文：节点 <name> · 服务器 <host> · 端口 <port>
      按钮/链接：tg://proxy?...（手机点开直接添加）
      备用链接：https://t.me/proxy?...
      两步使用说明：1) 手机点链接 → 2) Telegram 弹窗点「连接」
底部：链接即凭证，请勿转发；如非本人申请请忽略
```

- `plain text` 版本必须同样包含两条链接（部分客户端屏蔽 HTML）；
- **不建议**在邮件里塞二维码图片：邮箱客户端普遍拦截远程图片，内联 base64 会被 Gmail 剥离，附件又需要 `EMAIL.send` 的附件能力（待验证）。P2 可做「签名 URL 的二维码图片」：`GET /qr/:token.svg`，token = HMAC(clientId + exp)，服务端渲染 SVG，短有效期（10 分钟），不落日志。
- 限流：同一 client 60 秒 1 次、每天 5 次（`last_mail_at` / `mail_count_today` / `mail_day`），超限返回 429 + 剩余秒数。

### 7.4 为什么不做「订阅里的 Telegram 节点」

MTProto 不是 Clash / sing-box 的协议，任何订阅客户端都解析不了 `tg://`。把它塞进 `configs` 只会让 `/sub` 产出客户端无法识别的行，甚至导致客户端整份订阅解析失败。**独立表 + 独立入口是唯一正确的做法**，也正好满足「不要通过订阅获取」。

---

## 8. 管理端 UI

### 8.1 节点管理第三个页签

`Nodes.jsx` 的 `Tabs`（`:360`）新增：

```jsx
{ key: 'mtproto', label: `MTProto（${mtpRows.length}）`, children: <MtprotoPanel ... /> }
```

`MtprotoPanel`（新文件 `web-antd/src/pages/MtprotoPanel.jsx`，约 400 行）内容：

1. 三张 `StatCard`：节点数 / 已授权用户 / 运行中服务；
2. 节点表：名称 · 服务器 · 伪装域名 · 端口 · 模式（多用户/共享）· 授权数 · 服务状态 · 最近下发 · 操作（编辑 / 预览 / 下发 / 授权用户 / 删除）；
3. 「授权用户」抽屉：左列可授权 UUID（复用 `/api/mtproto/uuids`），右列已授权（UUID / 邮箱 / 标签 / 创建时间 / 交付状态 / 操作：复制链接 · 二维码 · 发邮件 · 轮换 · 撤销）；
4. 顶部批量动作：「一键下发全部」「批量授权」。

交互细节：
- secret 默认打码，点「显示」二次确认后带 `reveal=1` 请求，并提示「本次查看会记入日志」；
- 撤销用 `Popconfirm`，文案明确「仅该用户的链接立即失效，其他用户不受影响」；
- 新建节点的域名输入框给建议值（`storage.googleapis.com` / `www.microsoft.com`），并提示「域名要挑一个与服务器 IP 归属地看起来合理的站点」。

### 8.2 订阅分组详情（P2）

`Groups` / `GroupDetail` 增加「Telegram」小节，展示该 UUID 在各 MTProto 节点的授权状态，支持一键授权 / 撤销——直接复用 `handleUuidNodesRest`（`:9917`）的模式，新增 `/api/uuids/:uuid/mtproto` 的 GET/PUT。

### 8.3 日志

`Logs.jsx` 无需改动：`node_push_logs` 里 `action` 以 `mtproto-` 开头的新记录会自然出现。建议动作名：`mtproto-install` / `mtproto-apply` / `mtproto-authorize` / `mtproto-revoke` / `mtproto-mail` / `mtproto-deliver`。

---

## 9. 安全与合规

| 风险 | 处置 |
| --- | --- |
| secret = 凭证，等同于密码 | 列表打码；`reveal=1` 记日志；邮件只发绑定邮箱；门户不提供「查看他人」 |
| 邮件经过第三方（Cloudflare Email Routing / 收件方） | 文案提示；P2 可加「邮件只发一次，之后请到门户查看」 |
| 二维码被截图转发 | 无法防；靠「轮换」降低损失，UI 明示 |
| 门户接口越权 | 一律 `token.email → uuid_info.uuid → clients.uuid` 三段校验，失败返回 404 |
| 发信轰炸 | 60s / 5 次每日限流，按 client 维度 |
| 服务端渲染 XSS | 域名 / 标签全部走 `escapeHtmlText` 后再进邮件模板；TOML 渲染做引号转义（`"` `\` 与控制字符） |
| 日志泄露 | `node_push_logs` 只记「已发送」与节点名，不记 secret 明文 |

---

## 10. 实施拆分

### P0 · 数据层与渲染（1.5 人日）
- `workers/init_database.sql`：新增 §15 两张表（与 `singbox_*` 同风格注释）
- `workers/worker.js`：`MTPROTO_NODES_TABLE_DDL` / `MTPROTO_CLIENTS_TABLE_DDL` 常量；`ensureServerTables()`（`:565`）里建表；§6.3 的纯函数；`__test__`（`:11399`）导出
- `workers/test_mtproto_unit.mjs`：secret 形态、TOML 渲染确定性、`[secrets]` 位置、空 clients 回落 bootstrap、链接拼接

### P1 · 下发链路（1.5 人日）
- `agent/internal/runner/component.go`：`mtgVersion` + `installMtg()` + `ensureComponent` 分支 + `ensureUnit` 支持 `ExecReload`（含幂等补写）
- `agent/internal/metrics/metrics.go:90`：`mtg`
- `agent/dist/manifest.json` + 二进制重发（1.4.0）
- `workers/worker.js`：`handleMtprotoApi`（节点 CRUD / preview / apply / clients / rotate / revoke / push / reconcile）
- `workers/test_mtproto_api.mjs`：本地 D1 stub 跑全链路（参考 `test_singbox_api.mjs` 的桩）

### P2 · 管理端 UI（2 人日）
- `web-antd/src/pages/MtprotoPanel.jsx`（新）
- `web-antd/src/pages/Nodes.jsx`：第三个 Tab
- `npm run build` → 产物回写 `workers/worker.js`（`GENERATED ASSETS` 区间）

### P3 · 用户端与邮件（1.5 人日）
- `workers/worker.js`：`/api/portal/mtproto*` 两个 handler、`mtprotoMailHtml()`、限流
- `web-antd/src/portal/PortalUuid.jsx`（页签）、`PortalHome.jsx`（入口）、`MobileSubscribe.jsx`（卡片）
- `web-antd/src/portal/portalApi.js`：三个新方法

### P4 · 收尾（1 人日）
- `purgeMtprotoClients()` 接入 3 处 UUID 删除
- 巡检可选接入 + App 设置项
- readme：功能总览 / 协议与端口 / 数据表 / API 概览 / 更新日志 V13.3
- 全量回归：`node --check`、`npx tsc -p workers`、`check_pages.mjs`、`npm run check`、`go test ./...`

**合计约 7.5 人日**（不含真机 Telegram 联调）。

---

## 11. 测试计划

| 层 | 文件 | 断言要点 |
| --- | --- | --- |
| 单元 | `test_mtproto_unit.mjs` | secret 正则 `^ee[0-9a-f]{32}[0-9a-f]+$` 且域名 hex 段可还原；同一输入两次渲染字节一致；客户端乱序不改变输出；`[secrets]` 在文件末尾；空 clients 用 bootstrap；TOML 特殊字符转义 |
| API | `test_mtproto_api.mjs` | 建节点 → apply 后 `node_configs` 有 `mtpx:*:mtg`；授权 → version+1 且 `[secrets]` 多一行；撤销 → 该行消失、其他行不动；重复授权 409；不存在 UUID 404；`reveal=0` 时列表无明文 |
| 订阅隔离 | 同上 | 授权 3 个 UUID 后 `configs` 无 `tg://`；`/sub/<uuid>` 响应不含 `tg://`；`/clash-config` 不含 |
| 门户 | `test_portal.mjs` 追加 | 用户 A 取不到 B 的 client（404）；未登录 401；发信限流 429；`admin` 令牌访问 `/api/portal/*` 403 |
| Agent | `agent/internal/runner/component_test.go` | `ensureComponent("mtg-multi")` 走安装分支；单元文件含 `ExecReload`；重复调用幂等 |
| 前端 | `npm run check` | 新 Tab 渲染、抽屉打开、二维码 SVG 生成、移动端卡片不溢出 |
| 真机 | 手工 | Android / iOS Telegram 各扫一次码、点一次 `t.me` 链接；撤销后确认旧链接连不上、其他人仍可用 |

---

## 12. 风险与待验证项

| # | 项 | 影响 | 应对 |
| --- | --- | --- | --- |
| 1 | mtg-multi 为个人 fork | 长期维护风险 / Release 资产改名 | 锁版本 + 自建构建上传 S3；`engine` 字段预留切换位；最坏降级 single 模式（官方 mtg 二进制即可） |
| 2 | Release 资产命名未知 | 安装失败 | 实现前先查 Release API（见 §5.1），或直接自建 |
| 3 | `SIGHUP` reload 的原子性 | 半写文件被解析 → 用户被误踢 | Agent 本来就是「写临时文件 + rename」的原子替换，天然满足 |
| 4 | 老 systemd 单元无 `ExecReload` | reload 失败 → 回落 restart，全员断线几秒 | `ensureUnit` 幂等补写；`reload \|\| restart` 兜底 |
| 5 | 域名与 IP 归属不匹配 | 被主动探测识别为代理 | 新建节点时提示选「同 ASN / 同地区」的站点；文档给例子 |
| 6 | 默认黑名单误杀内网客户端 | 家宽 / 内网连不上 | `blocklist` 默认 0；开启时提示 `firehol_level1` 含 RFC1918 |
| 7 | 邮件含明文凭证 | 转发 / 存档泄露 | 文案提示 + 可轮换；P2 改「一次性链接指向门户」 |
| 8 | `EMAIL.send` 附件能力未验证 | 邮件内二维码画不出来 | v1 只发链接；二维码走 P2 的签名 SVG |
| 9 | 端口被封 | 连不上 | 端口可改；建议在 443 空闲时优先用 443 |
| 10 | 单机多协议端口冲突 | 服务起不来 | 端口推导沿用 `port_base + 9`，启用前做一次「同服务器端口查重」（复用 `singboxPorts` 的去重思路） |

---

## 13. 验收清单

- [ ] 后台「节点管理 → MTProto」能新建节点，Agent 自动装好 mtg-multi 并 `active`
- [ ] 勾选 3 个 UUID 授权，3 条链接互不相同，手机扫码均可连
- [ ] `/sub/<uuid>`、`/batch-configs`、`/clash-config` 输出里搜不到 `tg://`、`t.me/proxy`
- [ ] 撤销其中 1 个：该用户链接立即失效，另外 2 个用户**不断线**
- [ ] 用户中心能看到二维码；点「发送到邮箱」能收到含两条链接的邮件
- [ ] 用户 A 无法通过接口拿到用户 B 的链接
- [ ] 服务器监控卡片出现 `mtg` 服务状态
- [ ] 删除 UUID 后，对应 MTProto 授权自动失效并重渲染
- [ ] `npm run check`、`go test ./...`、`node workers/test_*.mjs` 全绿
