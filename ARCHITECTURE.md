# ProxyPilot 架构（Go + MySQL + Ant Design）

> 本分支：`feat/go-backend-migration`
> 目标：以 **Go 后端 + MySQL 8 + Ant Design 前端** 为生产主架构；
> 旧 Cloudflare Worker 实现已从本分支移除；`server/` + `web-antd/` + `agent/` 是唯一代码路径。历史迁移记录保留在 `docs/migration/`。

## 1. 技术栈

| 层 | 选型 | 位置 |
|---|---|---|
| 前端 | React 18 + Ant Design 6 + antd-mobile（Vite 构建） | `web-antd/` |
| 后端 | Go 1.23（标准库 `net/http`，无 Web 框架） | `server/` |
| 数据库 | MySQL 8（`utf8mb4_0900_as_cs`） | `server/migrations/` |
| 节点 Agent | Go（轮询 / WebSocket 唤醒 / PTY 终端） | `agent/` |
| 本地存储 | 对象桶目录 + Agent 分发目录 | `PP_BUCKET_DIR` / `PP_AGENT_DIST_DIR` |

## 2. 组件关系

```text
                    ┌─────────────────────────────┐
浏览器 ──HTTPS/WS──▶ │  Go server (server/)         │
  /  /admin         │  - 静态前端 + boot 注入      │
  /api/*            │  - REST API / JWT / API Key  │
  /sub /clash       │  - 订阅渲染                  │
  /api/shell/*      │  - WebSSH WebSocket 中继     │
                    └───────┬─────────────┬────────┘
                            │             │
                    MySQL 8 │             │ 本地目录（桶 / agent 分发）
                            ▼             ▼
                    ┌──────────────┐  ┌────────────────────┐
                    │ MySQL 数据  │  │ data/bucket        │
                    │ 26+ 张表    │  │ agent/dist         │
                    └──────────────┘  └────────────────────┘
                            ▲
                            │ Agent 出站：poll / ack / ws / shell
                    ┌───────┴───────┐
                    │ proxypilot-   │
                    │ agent         │──▶ sing-box / xray / mtg 配置落盘
                    └───────────────┘
```

- Agent 只主动出站，节点服务器无需开放管理端口。
- 配置以任务队列下发：`deploy` / `restart` / `exec` / `probe`，ack 回写版本。
- WebSSH 不依赖 Cloudflare Durable Object：浏览器 WS ↔ Go 内存会话 Hub ↔ Agent WS ↔ PTY（详见 `server/README.md`）。
- 所有路由由 Go 处理；`PP_LEGACY_BASE` 仅为旧部署迁移期的可选兜底，不是必需组件。

## 3. 目录职责

```text
proxypilot/
├── server/                 # 主后端（Go）
│   ├── cmd/proxypilot/     # 入口
│   ├── internal/api/       # 路由与业务（150 条路径）
│   ├── internal/auth/      # JWT / PBKDF2 / TOTP
│   ├── internal/db/        # 连接池 + 迁移执行器
│   ├── migrations/         # MySQL DDL
│   ├── testdata/           # Clash / sing-box / MTProto 黄金用例
│   ├── internal/api/assets_web/  # 内嵌前端快照（免构建兜底）
│   └── Dockerfile / docker-compose.yml / .env.example
├── web-antd/               # 前端源码（Ant Design），构建产物 dist/
├── agent/                  # Go Agent + install.sh + build.sh + dist/
├── rule/                   # Clash 模板 / 规则集
├── docs/                   # 架构、迁移文档
```

## 4. 前端策略（Ant Design 为主）

1. **源码唯一入口**：`web-antd/src`（React + AntD）。
2. 生产推荐构建：

   ```bash
   cd web-antd && npm ci && npm run build
   # 产物 web-antd/dist
   ```

   启动时设置 `PP_WEB_DIST=/绝对路径/web-antd/dist`，Go 会把 `index.html` 注入
   `window.__PP_BOOT__`（admin/portal 两种模式）并托管 `/assets/*`。

3. **内嵌产物**：`node web-antd/scripts/build.mjs` 构建后会自动同步
   `app.js` / `app.css` / `meta.json` 到 `server/internal/api/assets_web/`，由 Go `go:embed` 打包。
   未设置 `PP_WEB_DIST` 时使用内嵌产物，因此空环境也能直接启动。

> 改 UI 时应改 `web-antd/src` 并重新构建；不要手改内嵌快照。

## 5. 配置分层

| 层 | 用途 |
|---|---|
| 环境变量（`.env`） | 启动必需：MySQL DSN、JWT/Agent 密钥、监听地址、目录路径 |
| 后台 `/admin/settings`（`app_settings` 表） | 运行期可改：登录方式、Turnstile、SMTP、OIDC、站点/Agent 地址 |
| 数据表 | 业务配置：模板、节点、订阅分组、服务器、任务等 |

后台设置 30 秒缓存，保存即失效；环境变量作为“未在后台配置时”的默认值。

## 6. 部署拓扑

```text
DNS → 反向代理（Caddy/Nginx，TLS + Upgrade）
        → 127.0.0.1:8080（Go，docker compose）
             → MySQL 8（同机容器 / 外部实例）
             → 本地卷：data/bucket、agent/dist
```

- 单实例即可承载当前功能；WebSSH 会话在进程内存，多实例需要按 `server_id` 粘性。
- 批量任务当前同步执行；大规模部署建议分批或后续改异步 worker。
- 备份：MySQL 数据卷 + `data/bucket` + `agent/dist`（或改回对象存储）。

## 7. 旧版代码移除策略

- 本分支已删除 `workers/`、`wrangler.toml` 与依赖 Worker 的测试脚本。
- 黄金用例仍保留在 `server/testdata/`，由 Go 单测使用。
- `PP_LEGACY_BASE` 配置项保留用于迁移期兜底，但仓库不再包含 Worker 源码。
- 新功能只加在 `server/`、`web-antd/`、`agent/`。

## 8. 验证入口

```bash
cd server
go test ./...                    # 104 个单测
go vet ./...
go build ./cmd/proxypilot
python3 tools/check_coverage.py --repo ..   # 150/150
server/tools/smoke.sh https://<域名>        # 部署后冒烟
```


> 落地评审与未完成项见 [docs/architecture-review.md](docs/architecture-review.md)。
