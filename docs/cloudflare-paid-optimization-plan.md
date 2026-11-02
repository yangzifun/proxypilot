# Cloudflare Workers Paid 优化方案

> 状态：**方案（待实施）** · 适用版本：V14.1 · 目标：在已开通 Workers Paid（$5/月）的前提下，把异步能力、部署一致性和 D1 延迟做到当前架构下的最优，不引入外部服务。

> **实施记录（V14.2）**：P0 已完成（`wrangler.toml`、批量预算 25s、`idx_configs_singbox_node` / `idx_singbox_nodes_server`、R2 发布参数）；P1 已完成（Queues 生产/消费、预算耗尽自动续投与退避、cron 兜底、7 天/200 条保留期清理），并在 `test_jobs_api` 增加 Queue 消费/入队/清理等 12 项断言；item 级自动重试已实现（3 次，400/800ms 退避，预算不足时保留 pending 下一轮继续）。P2 已实现为「DO 长轮询唤醒」：Agent 在 poll 后挂起 `/api/agent/wait`，任务入队时 DO 立即唤醒，Agent 补足 5s 防刷冷却后立刻 poll（下发延迟约 2–5s）；未绑定 `AGENT_HUB` 时自动回退普通轮询。未采用 WebSocket，避免改 Agent 传输层。

---

## 1. 现状与瓶颈

| 维度 | 现状 | 瓶颈 |
| --- | --- | --- |
| 批量任务 | `batch_jobs` + `waitUntil` 先跑 15s，跑不完由 cron 每分钟续 20s | 段落之间最多空 1 分钟；无自动重试/退避 |
| 单次执行 | `BATCH_BUDGET_MS = 15000`、`BATCH_CRON_BUDGET_MS = 20000` | 免费版 CPU 限制下不敢放大预算 |
| D1 | 单 Worker 串行执行大量 D1 查询（apply-all、订阅同步） | Worker 可能离 D1 主库很远，往返延迟被放大 |
| 部署 | 控制台粘贴 `workers/worker.js`，无 `wrangler.toml` | 绑定/定时器/placement 无法版本化，易漂移 |
| Agent 二进制 | 外部 S3（`publish.ps1`，需 AWS 凭证） | 多一个外部依赖与凭证管理 |
| Agent 下发 | Agent 轮询 `agent_tasks` | 下发要等一个轮询周期，不是秒级 |
| 指数 | `init_database.sql` 只覆盖部分热点 | 新表（`batch_jobs`、`singbox_nodes`、`agent_tasks`）缺索引 |

## 2. 目标与非目标

**目标**

1. 批量任务提交后**连续执行**，不再依赖每分钟 cron 的节奏；失败项自动重试。
2. Worker 与 D1 的物理距离可控（Smart Placement），D1 查询成本可观测。
3. 部署配置可版本化（`wrangler.toml`），控制台只做兜底。
4. Agent 二进制托管回到 Cloudflare（R2），去掉外部 S3 依赖。
5. 为「秒级下发」预留可选的 DO 推送通道，不阻塞前四步。

**非目标（本期不做）**

- 不引入 Workflows / 外部 Redis / SQS。
- 不重写 Agent 协议（DO 推送是可选三期）。
- 不改 `batch_jobs` 表结构（只加索引与清理）。

## 3. 总览

| 阶段 | 内容 | 工作量 | 收益 |
| --- | --- | --- | --- |
| **P0** | `wrangler.toml`、CPU 预算、Smart Placement、D1 索引、R2 二进制 | 0.5 天 | 立刻降低 D1 延迟、部署可复现、去掉 S3 |
| **P1** | Queues 替换 cron 续跑 + 自动重试 + 保留期清理 | 1–2 天 | 批量任务连续执行、失败自愈 |
| **P2** | Durable Objects Agent 推送（可选） | 3–5 天 | 下发从「等轮询」变秒级 |
| 不做 | Workflows、外部队列、全量 DO | — | 避免过度设计 |

---

## 4. P0：配置与基础设施（半天）

### 4.1 新增 `wrangler.toml`

```toml
name = "proxypilot"
main = "workers/worker.js"
compatibility_date = "2026-10-30"   # 按 wrangler 支持的最近日期调整

# 让 Worker 自动靠近 D1 主库（D1 密集操作收益最大）
[placement]
mode = "smart"

# 付费版默认 30s CPU，这里显式写死，避免控制台被改回默认
[limits]
cpu_ms = 30000

[[d1_databases]]
binding = "DB"
database_name = "proxypilot-db"
database_id = "<D1_DATABASE_ID>"

# Queue（P1 启用，先留注释）
# [[queues.producers]]
# binding = "JOB_QUEUE"
# queue = "proxypilot-jobs"
# [[queues.consumers]]
# queue = "proxypilot-jobs"
# max_batch_size = 1
# max_retries = 5
# dead_letter_queue = "proxypilot-jobs-dlq"

[triggers]
crons = ["* * * * *"]

[vars]
# 非敏感变量放这里；JWT_SECRET / TURNSTILE_SECRET_KEY 等用 `wrangler secret put`
AGENT_BINARY_BASE_URL = "https://agent.example.com"
```

**从控制台迁移到 wrangler 的步骤**

1. `npx wrangler d1 list` 拿到 `database_id`，填入 toml。
2. `npx wrangler secret put JWT_SECRET`（以及 `TURNSTILE_SECRET_KEY`、`SENDER_EMAIL` 等现有 secret）。
3. `npx wrangler deploy --dry-run --outdir dist-check` 对比产物，确认只多出 binding 配置。
4. 首次 `npx wrangler deploy` 后，在控制台核对：D1 binding、cron、placement、CPU limit。
5. 回滚：控制台仍保留上一版本代码，可直接回滚；`wrangler rollback` 亦可。

> 建议保留「控制台粘贴」作为应急通道，但日常以 `wrangler deploy` 为准。

### 4.2 放大批量任务预算

`workers/worker.js`：

```diff
-const BATCH_BUDGET_MS = 15000;
-const BATCH_CRON_BUDGET_MS = 20000;
+const BATCH_BUDGET_MS = 25000;
+const BATCH_CRON_BUDGET_MS = 25000;
```

CPU 上限 30s 下留 5s 余量给收尾（写 completed/failed、释放锁）。验收：单次 apply-all 100 节点在 25s 内能推进的项数比现在提升 ≥40%。

### 4.3 D1 索引

先跑 `EXPLAIN QUERY PLAN` 确认是否走索引，再落 DDL。现有索引已覆盖 `agent_tasks(server_id,status)`、`batch_jobs(status,id)`、`batch_job_items(job_id,seq)`（UNIQUE 自带索引），**真正缺的是下面两个**：

```sql
-- 订阅同步按节点查/删（singboxSyncUuidConfigs 每次按 node_id 过滤并删行）
CREATE INDEX IF NOT EXISTS idx_configs_singbox_node ON configs(singbox_node_id);

-- 节点按服务器查（绑定查询 / 推送 / 巡检）
CREATE INDEX IF NOT EXISTS idx_singbox_nodes_server ON singbox_nodes(server_id);
```

迁移方式：写入 `workers/init_database.sql`，并在 `ensureServerTables` 里幂等 `CREATE INDEX IF NOT EXISTS`；线上用 `wrangler d1 execute proxypilot-db --remote --file=...` 或控制台执行。

### 4.4 R2 托管 Agent 二进制

1. 创建 bucket `proxypilot-agent`，绑定自定义域 `agent.example.com`（或直接用 r2.dev 测试域）。
2. R2 兼容 S3 API：`publish.ps1` 只改 `-Endpoint https://<account_id>.r2.cloudflarestorage.com`、`-Bucket proxypilot-agent`，凭证换成 R2 API Token。
3. Worker 侧只改环境变量：`AGENT_BINARY_BASE_URL=https://agent.example.com`（代码无需改，`agentLatestManifest` 已有 5 分钟缓存）。
4. 验收：后台「更新 Agent」能读到 1.5.3 manifest；`curl https://agent.example.com/manifest.json` 正常。

> 可选：后续把 R2 挂成 Worker binding，由 Worker 直接返回文件并加 `Cache-Control`，省掉公开桶；本期不做。

### 4.5 观测基线

上线前记录三项基线，P1 后对比：

- 单次 `apply-all` 100 节点的 D1 查询数（`wrangler tail` 或 D1 指标）
- 批量任务从提交到完成的 P50/P95
- `batch_jobs` 每分钟续跑次数（cron 日志）

---

## 5. P1：Queues 替换 cron 续跑（1–2 天）

### 5.1 目标架构

```
管理端 POST /api/jobs
   └─ batchCreateJob() 物化 items
        └─ batchKick(): JOB_QUEUE.send({ jobId }, { delaySeconds: 0 })
             └─ Queue consumer: runBatchJob(env, jobId, 25000)
                  ├─ 全部完成 → 结束
                  ├─ 预算用尽 → 再 send({ jobId, attempt+1 }, { delaySeconds: 5 })
                  └─ 基础设施异常 → 抛出，交给 Queues 重试/死信

cron（保留，降级为兜底）
   ├─ runQueuedBatchJobs()：捞 15 分钟没动的 running/queued（消息丢失时）
   └─ batchCleanup()：删除 7 天前 / 超过 200 条的已结束任务
```

### 5.2 消息协议

```json
{ "jobId": 123, "attempt": 1, "reason": "kick" }
```

- `attempt` 只用于日志/退避，不参与幂等（幂等靠 D1 的 job 锁 + item 状态）。
- 消费端先 `batchClaimJob`；抢不到锁说明别的 runner 在跑，直接 ack，不再重发。

### 5.3 代码改造点

| 位置 | 改动 |
| --- | --- |
| `batchKick(req, env, jobId)` | 优先 `env.JOB_QUEUE.send({ jobId, attempt: 0 })`；没有绑定则回退 `scheduleBackground(runBatchJob)`（本地/测试兼容） |
| `export default` | 新增 `async queue(batch, env, ctx)`，遍历 `batch.messages` 调 `runBatchJob(env, jobId, 25000)` |
| `runBatchJob` | 返回值已含 `partial/done`；consumer 据此决定是否续发 |
| `batchCreateJob` / 接口 | 不需要改：仍先落库再 kick |
| `handleScheduled` | 保留 `runQueuedBatchJobs` 作为兜底；新增 `batchCleanup` |

**消费者伪代码**

```js
async queue(batch, env, ctx) {
  for (const msg of batch.messages) {
    const jobId = Number(msg.body && msg.body.jobId);
    if (!jobId) { msg.ack(); continue; }
    const r = await runBatchJob(env, jobId, 25000);
    if (r && r.partial) {
      const attempt = Number(msg.body.attempt || 0) + 1;
      // 指数退避：5s, 15s, 45s，最多 5 次；超过就交给 cron 兜底
      const delay = Math.min(45, 5 * Math.pow(3, Math.min(attempt - 1, 2)));
      if (attempt <= 5) await env.JOB_QUEUE.send({ jobId, attempt }, { delaySeconds: delay });
      msg.ack();
      continue;
    }
    msg.ack();
  }
}
```

### 5.4 幂等与并发

- 现有 `batchClaimJob` 的 D1 条件更新（`locked_at` 过期）继续作为唯一锁；重复消息只会有一个 runner 成功。
- `batch_job_items` 只跑 `status='pending'`，已成功项不重跑。
- 消费者 `max_batch_size = 1`，避免单条消息处理多任务导致超时；并发由 Queues 的消费者数控制。
- 失败重试在 **item 级**由 `attempts` 字段记录（后续可加自动重试：失败项 attempts < 3 时重置为 pending）。

### 5.5 配置

```toml
[[queues.producers]]
binding = "JOB_QUEUE"
queue = "proxypilot-jobs"

[[queues.consumers]]
queue = "proxypilot-jobs"
max_batch_size = 1
max_retries = 5
dead_letter_queue = "proxypilot-jobs-dlq"
```

创建命令：

```bash
npx wrangler queues create proxypilot-jobs
npx wrangler queues create proxypilot-jobs-dlq
```

### 5.6 保留期清理

`handleScheduled` 每分钟（或每 10 分钟）执行：

```sql
DELETE FROM batch_job_items WHERE job_id IN (
  SELECT id FROM batch_jobs
  WHERE status IN ('success','partial','failed','canceled')
    AND finished_at < ?            -- 7 天前
  ORDER BY id DESC LIMIT 200 OFFSET 200
);
DELETE FROM batch_jobs WHERE status IN (...) AND finished_at < ?;
```

### 5.7 成本

- Queues：每月 100 万次操作包含在计划内；本系统一天最多几百次消息，成本≈0。
- 消费者 CPU 与 Worker 同价，已包含在 $5 计划。

### 5.8 验收标准

- 提交 100 项任务后，从提交到完成 **P95 < 90s**（现在最差要 2–3 分钟）。
- 人为让 3 项失败 → 自动重试 3 次后标记 failed，任务中心可见 attempts。
- 杀掉消费者/消息丢失时，cron 兜底仍能在 15 分钟内接管。
- `wrangler tail` 中不再出现「每分钟固定续跑」的空转日志。

---

## 6. P2（可选）：Durable Objects 做 Agent 推送

### 6.1 为什么

当前 Agent 轮询间隔决定了下发延迟。若要求「点下发后 2–5 秒生效」，轮询做不到；DO WebSocket 可以把延迟降到秒级。

### 6.2 设计

```
Agent ──WS──> Worker /api/agent/ws?token=... ──> AgentHub DO(serverId)
Worker 入队 agent_tasks ──> DO.push(task) ──> Agent 执行 ──> ack
Agent 断线 ──> 回退轮询（现有 /api/agent/poll 保留）
```

- DO 命名：`idFromName('agent:' + serverId)`，一台服务器一个实例。
- 鉴权：复用现有 `authenticateAgentRequest`，WS 升级前校验 token。
- 推送失败/超时：保持任务为 pending，由 Agent 下次轮询兜底。
- DO 不存业务数据，只存 socket 与最近一次 ack；任务仍在 D1。

### 6.3 改动面

| 端 | 改动 |
| --- | --- |
| Worker | `export class AgentHub`（DO）、`/api/agent/ws` 路由、`agentQueueTask` 后 `hub.push(taskId)` |
| Agent (Go) | 新增 WS 客户端（`nhooyr.io/websocket` 或 `gorilla/websocket`），重连退避、心跳、收到任务后复用现有 runner |
| 配置 | `[[durable_objects.bindings]]` + `[[migrations]]`；`wrangler deploy` 必须（控制台不便管理 migration） |

### 6.4 灰度

1. 先只对 1 台服务器开 WS，轮询仍每 60s 兜底。
2. 观察 24h：任务 ack 延迟、WS 断线率、DO 错误。
3. 全量后再把轮询间隔放宽到 5 分钟，进一步省请求。

### 6.5 成本

DO 请求约 $0.15/百万 + 持续时间费用；十几台服务器的心跳量级每月几美分。

---

## 7. 明确不做

| 方案 | 不做的理由 |
| --- | --- |
| Cloudflare Workflows | 适合多步骤持久化流程；当前 batch_jobs 已覆盖，迁移只增复杂度 |
| 外部 Redis / SQS | 多一套运维与网络出口，Queues 足够 |
| 全量 Durable Objects | 几十台规模下 D1 条件更新锁够用；DO 只在秒级推送时才有明显收益 |
| 把渲染搬到 cron | 语义混乱，且 cron 同样受 CPU 限制 |

---

## 8. 实施计划

| 里程碑 | 内容 | 验收 |
| --- | --- | --- |
| M1（0.5 天） | wrangler.toml、CPU 预算、索引、R2 迁移 | `wrangler deploy` 成功；D1 查询计划走索引；后台能读 R2 manifest |
| M2（1–2 天） | Queues 生产/消费、重试、清理 | 100 项任务 P95 < 90s；失败自动重试；cron 仅兜底 |
| M3（可选） | DO 推送 + Agent WS | 单台下发的 ack 延迟 < 5s；断线自动回退轮询 |

**风险与回滚**

- `wrangler deploy` 覆盖控制台配置 → 先 dry-run + 保留控制台旧版本；失败可 `wrangler rollback`。
- Queues 消息重复/乱序 → D1 锁 + item 状态幂等；消费者 ack 后再续发。
- Smart Placement 可能让首次 HTML 稍慢 → 用 `cf-placement` 响应头观测；若不满意可关闭。
- R2 自定义域未生效 → 先保留 S3 地址作为 `AGENT_BINARY_BASE_URL` 回滚值。

---

## 9. 附录

### 9.1 关键常量与函数索引

| 名称 | 位置 | 说明 |
| --- | --- | --- |
| `BATCH_BUDGET_MS` / `BATCH_CRON_BUDGET_MS` | `workers/worker.js` | 批量任务单段预算 |
| `batchKick` | `workers/worker.js` | 提交后触发执行（P1 改为 Queue send） |
| `runBatchJob` | `workers/worker.js` | 抢锁 + 逐项执行 |
| `runQueuedBatchJobs` | `workers/worker.js` | cron 兜底 |
| `handleScheduled` | `workers/worker.js` | cron 入口 |
| `agentQueueTask` | `workers/worker.js` | Agent 任务入队（P2 后加 DO 推送） |
| `publish.ps1` | `agent/` | 二进制发布（P0 改 R2 endpoint） |

### 9.2 验收用命令

```bash
# 部署前检查
npx wrangler deploy --dry-run --outdir dist-check

# 查看线上日志（含 batch-job / agent task）
npx wrangler tail --format pretty

# 验证索引
npx wrangler d1 execute proxypilot-db --remote \
  --command "EXPLAIN QUERY PLAN SELECT * FROM configs WHERE singbox_node_id = 1"
```
