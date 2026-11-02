# 批量操作后台化（Batch Jobs）方案

> **实施状态**：已落地（V14.1）：批量任务框架 + 接口转换 + 任务中心 + 链接指纹/批量写优化；单测 `workers/test_jobs_api.mjs` 26 项通过。
>
> 目标版本：V14.1 · 范围：所有「一键 / 批量」类操作改为**提交即返回、后台执行、可看进度、可取消重试**
> 一句话：点按钮只创建一个任务并立刻返回，真正的循环在 `waitUntil` 里跑一段，跑不完由 cron 续跑，前端在「任务中心」看进度。

---

## 1. 现状与问题

### 1.1 现在哪些操作是「批量」的

| 入口 | 代码位置 | 一次要做的事 | 目标数量级 |
| --- | --- | --- | --- |
| 一键下发全部节点 | `worker.js:8915` `/api/singbox/apply-all` | 逐节点：渲染 → 写 `node_configs` → 同步订阅 → 排队 deploy | 节点数（10~100+） |
| 一键下发（另一入口） | `worker.js:8965` `/api/singbox/deploy-all` | 同上（历史接口） | 同上 |
| 批量授权推送 | `worker.js:8953` `/api/singbox/push` | 服务器 × UUID 组合：建节点 → 授权 → 渲染 → 排队 | 100 × 200 上限 |
| MTProto 全部下发 | `worker.js:10327` `/api/mtproto/apply-all` | 逐节点：渲染 → 写配置 → 排队 | 节点数 |
| MTProto 模板下发 | `worker.js:10094` `/api/mtproto/templates/:id/apply` | 模板下所有节点 | 节点数 |
| MTProto 批量授权 | `worker.js:10338` `/api/mtproto/push` | 服务器 × UUID | 同上 |
| 一键更新 Agent | `worker.js:12658` `/api/agent/push-update` | 逐服务器：比对 manifest → 入队 update_agent | 服务器数 |
| UUID 一致性巡检 | `worker.js:3482` `/api/monitor/uuid-check` | 全节点比对 + 自动修复（内含渲染/下发） | 节点数 |
| 日志清理 | `worker.js:2772` `/api/logs/cleanup` | 分批 DELETE | 万级行 |

### 1.2 为什么会出问题

这些 handler 目前都是**同步 for 循环**，请求要等整个循环跑完才返回：

- **Worker 有时限**：单请求 CPU 上限（付费 30s）、`waitUntil` 最多再把生命周期延 30s；节点多、D1 往返多时会被中途掐断，前端只看到 1101/超时，**且已执行了一半**，无法知道进度。
- **前端被阻塞**：点「一键下发」后页面转圈几十秒，关掉页面就不知道结果。
- **不可恢复**：中途失败只能整批重来，无法只重试失败项。
- **审计缺失**：只有一个总结果，没有「哪个节点失败、为什么」。

### 1.3 已有的可复用件

| 设施 | 位置 | 用途 |
| --- | --- | --- |
| `scheduleBackground(req, promise)` | `worker.js:34` | 把 promise 挂到 `ctx.waitUntil`，响应后继续跑 |
| `handleScheduled()` | `worker.js:10709` | Cron 入口，已跑心跳 / 任务清理 / 巡检 |
| `agentQueueTask()` | worker | 幂等入队（`payload_hash` 去重） |
| `singboxApplyNode()` / `mtprotoApplyAndQueue()` | worker | **单步函数已存在**，批量只是把它们循环 |
| 任务队列 `agent_tasks` | D1 | 面向「节点机」的指令队列，本方案是面向「后台操作」的另一层，不混用 |

> 结论：不需要重写业务，只需要把「同步循环」换成「任务 + 分片执行器」，单步逻辑原样复用。

---

## 2. 设计原则

1. **提交与执行分离**：`POST` 只做「参数校验 + 物化任务项 + 入队」，200ms 内返回。
2. **单步逻辑零改动**：执行器调用现有函数（`singboxApplyNode`、`mtprotoApplyAndQueue`、`agentQueueTask`…），不复制业务。
3. **可续跑**：任何时刻中断，下一次 cron / 下一次 kick 从 `cursor` 继续，已成功的项不重跑。
4. **幂等**：重复执行同一项不产生重复副作用（依赖 `agentQueueTask` 去重、`node_configs` 内容哈希）。
5. **可见**：每个任务有总进度、每项状态与错误；前端可离开页面再回来。
6. **不引新绑定**：只用 D1 + 现有 Cron + `waitUntil`，不要求用户加 Durable Object / Queue 绑定（DO 作为可选增强）。

---

## 3. 总体架构

```
┌──────────┐  POST /api/jobs {kind, params}
│ 管理后台  │ ─────────────────────────────┐
│ (任务中心)│ ◀── {job_id, total} 立即返回   │
└────┬─────┘                              ▼
     │ GET /api/jobs/:id          ┌─────────────────┐
     │ (轮询进度)                  │ batch_jobs      │
     ▼                            │ batch_job_items │  ← 物化出 N 个 item
┌──────────┐                     └────────┬────────┘
│ 任务中心  │                              │
└──────────┘                     ┌────────▼─────────┐
                                 │ runBatchJob()    │  ① 请求内 waitUntil 先跑 15~20s
                                 │ 逐项执行 + 记进度 │  ② 跑不完释放锁，等 cron
                                 └────────┬─────────┘
                                          │ 调用现有单步函数
                                 ┌────────▼─────────┐
                                 │ singboxApplyNode │
                                 │ mtprotoApply...  │
                                 │ agentQueueTask   │
                                 └──────────────────┘
```

时序：

```
用户点击 → POST /api/jobs
         → INSERT batch_jobs + batch_job_items（事务/批量）
         → scheduleBackground(runBatchJob(env, jobId, 15s))
         → 返回 { job_id, total }
runBatchJob：
         抢锁 → 循环 item（每项更新状态）→ 到时间预算 → 释放锁（保留 cursor）
cron（每分钟）：
         捞 queued / 锁过期的 running → runBatchJob(…, 20s)
前端：
         每 3s GET /api/jobs/:id，直到 status ∈ {success, partial, failed, canceled}
```

---

## 4. 数据模型

```sql
CREATE TABLE IF NOT EXISTS batch_jobs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    kind          TEXT NOT NULL,          -- singbox.apply_all / mtproto.push / agent.update ...
    title         TEXT,                   -- 展示名（含目标摘要）
    params        TEXT,                   -- 入参快照 JSON
    status        TEXT DEFAULT 'queued',  -- queued|running|success|partial|failed|canceled
    total         INTEGER DEFAULT 0,
    done          INTEGER DEFAULT 0,      -- 已结束（成功+失败+跳过）
    failed        INTEGER DEFAULT 0,
    cursor        INTEGER DEFAULT 0,      -- 最后处理的 seq（续跑用）
    runner_id     TEXT,                   -- 抢锁的实例标识
    locked_at     INTEGER DEFAULT 0,      -- 锁时间（超过 90s 视为过期）
    actor         TEXT,                   -- 操作人
    error         TEXT,                   -- 任务级错误
    summary       TEXT,                   -- 结果摘要 JSON（按 kind 自定义）
    created_at    INTEGER DEFAULT 0,
    started_at    INTEGER DEFAULT 0,
    finished_at   INTEGER DEFAULT 0,
    updated_at    INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_batch_jobs_status ON batch_jobs (status, id);
CREATE INDEX IF NOT EXISTS idx_batch_jobs_created ON batch_jobs (created_at);

CREATE TABLE IF NOT EXISTS batch_job_items (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id     INTEGER NOT NULL,
    seq        INTEGER NOT NULL,          -- 1..total，决定执行顺序
    target     TEXT,                      -- node:12 / server:3 / pair:3:uuid
    label      TEXT,                      -- 展示名（节点名 / 服务器名 / UUID 短写）
    status     TEXT DEFAULT 'pending',    -- pending|running|success|failed|skipped
    message    TEXT,                      -- 失败原因 / 结果摘要
    detail     TEXT,                      -- JSON：耗时、版本号、端口等
    attempts   INTEGER DEFAULT 0,
    started_at INTEGER DEFAULT 0,
    finished_at INTEGER DEFAULT 0,
    UNIQUE(job_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_batch_items_job ON batch_job_items (job_id, seq);
```

要点：

- **物化目标**：创建时就写满 `items`，执行阶段不再解析「要处理谁」；UI 也能立刻显示「共 N 项」。
- **顺序即 seq**：按节点 id / 服务器 id 升序，稳定、可续跑。
- **不回写业务表**：任务只记录过程，真正的状态仍在 `node_configs` / `agent_tasks` / `configs`。
- **保留策略**：只留最近 7 天或 200 个任务（cron 清理，先删 items 再删 jobs）。

---

## 5. 执行器

### 5.1 抢锁

```js
async function claimJob(env, jobId, runnerId) {
  const now = Date.now();
  const r = await env.DB.prepare(
    `UPDATE batch_jobs
        SET status='running', runner_id=?, locked_at=?, started_at=COALESCE(NULLIF(started_at,0),?), updated_at=?
      WHERE id=? AND status IN ('queued','running')
        AND (locked_at IS NULL OR locked_at = 0 OR locked_at < ?)`
  ).bind(runnerId, now, now, now, jobId, now - 90_000).run();
  if (!Number((r.meta && r.meta.changes) || 0)) return null;   // 别人在跑 / 已结束
  return await env.DB.prepare('SELECT * FROM batch_jobs WHERE id=?').bind(jobId).first();
}
```

- 90 秒锁过期：覆盖 `waitUntil` 被掐断、实例崩溃的情况。
- 同一任务永远只有一个 runner（D1 的单条 UPDATE 是原子的）。

### 5.2 主循环

```js
async function runBatchJob(env, jobId, budgetMs = 15000) {
  const runnerId = crypto.randomUUID();
  const job = await claimJob(env, jobId, runnerId);
  if (!job) return;
  const deadline = Date.now() + budgetMs;
  let cursor = Number(job.cursor || 0);

  while (Date.now() < deadline) {
    if (await isCanceled(env, jobId)) return await finishJob(env, jobId, 'canceled');
    const items = await env.DB.prepare(
      `SELECT * FROM batch_job_items WHERE job_id=? AND seq>? AND status='pending'
        ORDER BY seq ASC LIMIT 5`
    ).bind(jobId, cursor).all();

    if (!items.results.length) return await finishJob(env, jobId, 'auto');
    for (const item of items.results) {
      await markItem(env, item.id, 'running');
      let res;
      try { res = await runBatchItem(env, job, item); }
      catch (e) { res = { ok: false, message: (e && e.message) || String(e) }; }
      await markItem(env, item.id, res.ok ? 'success' : 'failed', res.message, res.detail);
      cursor = item.seq;
      await bumpProgress(env, jobId, res.ok, cursor);
      if (Date.now() >= deadline) break;
    }
  }
  await releaseJob(env, jobId, cursor);   // status 仍为 running，锁清空，等 cron
}
```

### 5.3 单项执行：复用现有函数

```js
const BATCH_KINDS = {
  'singbox.apply_all': {
    target: 'singbox_node',
    run: async (env, job, item) => {
      const ctx = await loadSingboxNodeContext(env, item.target_id);
      if (ctx.error) return { ok: false, message: ctx.error };
      const users = await singboxUsersWithUuids(ctx.template, JSON.parse(ctx.node.uuid_list || '[]'));
      const tplEff = Object.assign({}, singboxEffectiveTemplate(ctx.template, ctx.node), { users_json: JSON.stringify(users) });
      const applied = await singboxApplyNode(env, ctx.node, ctx.server, tplEff, { enableOnDeploy: true });
      if (!applied.ok) return { ok: false, message: applied.error };
      const queued = await singboxQueueDeployFiles(env, ctx.server, applied.files, job.actor);
      return { ok: true, message: `变更 ${applied.files.filter(f => f.changed).length} 个文件，排队 ${queued.filter(q => q.ok).length} 条`, detail: { changed: applied.changed } };
    }
  },
  // 'mtproto.apply_all' / 'agent.update' / 'uuid.reconcile' / 'singbox.push' ...
};
```

- **部分成功**：单项失败不中断整批；最终 `failed>0 && done>failed` → `partial`。
- **跳过**：目标已被删除（节点/服务器不存在）→ `skipped`，不计失败。
- **限速**：每项之间可 `await sleep(0)`；对同一服务器连续部署时天然由 `agentQueueTask` 去重。

### 5.4 收尾

```js
async function finishJob(env, jobId, mode) {
  const job = await getJob(env, jobId);
  const failed = Number(job.failed || 0);
  const status = mode === 'canceled' ? 'canceled' : (failed ? 'partial' : 'success');
  await env.DB.prepare(
    `UPDATE batch_jobs SET status=?, finished_at=?, locked_at=0, runner_id='', updated_at=? WHERE id=?`
  ).bind(status, Date.now(), Date.now(), jobId).run();
  await recordJobLog(env, jobId, status);   // 写一条 node_push_logs（action='batch-job'）
}
```

---

## 6. 触发方式选型

| 方案 | 优点 | 缺点 | 结论 |
| --- | --- | --- | --- |
| **`waitUntil` + cron 续跑** | 零新绑定；点击后立刻开跑；中断可恢复 | 单次最多 ~30s；cron 粒度 1 分钟 | ✅ 采用 |
| Durable Object（`JobRunner`） | alarm 精确续跑（秒级）、单实例强一致 | 新绑定 + class 迁移；要改部署配置 | ⏳ P3 可选增强 |
| Cloudflare Queues | 天然重试/并发 | 需开通、按消息计费、本地调试麻烦 | ❌ 不采用 |
| 纯 cron（点击只入队） | 最简单 | 点击后最多等 1 分钟才开始，体感差 | ❌ |

> 采用「立即 kick + cron 兜底」：90% 的任务在 `waitUntil` 的 15~20s 内跑完；大的（100+ 节点）由 cron 每分钟续跑，最长延迟 1 分钟，完全可接受。

cron 侧改动（`handleScheduled` 最前面）：

```js
// ① 先把后台批量任务推进一轮（每次最多 3 个任务、每个 20s）
await runQueuedBatchJobs(env, { maxJobs: 3, budgetMs: 20000 });
```

---

## 7. 接口设计

### 7.1 通用任务接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/jobs` | `{kind, params}` → 校验、物化 items、kick、返回 `{job_id,total}` |
| GET | `/api/jobs` | 列表：`?status=active|all&kind=&limit=20&offset=0`，返回进度摘要 |
| GET | `/api/jobs/:id` | 详情 + items（`?items=1&offset&limit`） |
| POST | `/api/jobs/:id/cancel` | 置 `canceled`；执行器每项前检查，剩余项标 `skipped` |
| POST | `/api/jobs/:id/retry` | 失败项重置 `pending`、`cursor=0`、`status=queued` 后重新 kick |

权限：与对应业务接口一致（后台管理员即可）；`agent.update` / 含危险操作的 kind 仅超管（沿用 `denyUnlessSuperAdmin`）。

### 7.2 旧接口兼容

保留原路径，行为改为「入队」：

```http
POST /api/singbox/apply-all
→ 200 { "success": true, "async": true, "job_id": 42, "total": 15 }
```

- 支持 `?sync=1` 走原同步逻辑（用于脚本 / 现有单测；限制最多 20 项，超过返回 400 并提示用异步）。
- 也支持 `?wait=1`：入队后最多等 20s，尽量直接返回最终结果（前端旧代码/第三方脚本可平滑过渡）。

### 7.3 请求校验

- `kind` 必须在白名单；
- `params` 按 kind 校验（服务器/节点是否存在、UUID 是否合法）；
- **同一 kind + 相同参数**在 60s 内重复提交 → 直接返回已有 `job_id`（防连点）；
- 单任务 items 上限 500；全局同时运行任务 ≤ 3（超出的保持 `queued`）。

---

## 8. 前端交互

### 8.1 任务中心

- 顶栏加「任务」按钮 + 运行中数量徽标（`GET /api/jobs?status=active` 每 10s 轮询；有运行中任务时 3s）。
- 抽屉/页面：
  - 列表：类型、标题、进度条 `done/total`、失败数、耗时、操作人、状态标签；
  - 详情：逐项表格（目标 / 状态 / 耗时 / 消息），失败项「重试」、运行中「取消」；
  - 空态与保留期说明。

### 8.2 按钮行为

| 按钮 | 现在 | 改后 |
| --- | --- | --- |
| 一键下发全部节点 | 转圈等完 | 立刻 toast：「已创建后台任务 #42（15 个节点）」+「查看进度」 |
| 批量授权推送 | 同上 | 同上 |
| 一键更新 Agent | 同上 | 同上 |
| UUID 一致性巡检 | 同上 | 同上（可取消） |

- 统一封装 `runJob(kind, params)`：POST → toast → 打开任务中心详情。
- 任务完成后若当前页与任务相关（节点/服务器列表），自动 `reload()` 一次。
- 页面刷新/关闭不影响任务；重新打开任务中心能看到历史。

### 8.3 新的「批量选择」入口（可选，P2）

服务器/节点列表支持多选，底部出现批量动作条：下发、重启服务、更新 Agent、停用 SSH…全部走 `/api/jobs`，item 就是选中的行。这样「多个功能批量执行」有统一入口，而不是散落在各个页面。

---

## 9. 改造清单

| # | 文件 / 位置 | 改动 |
| --- | --- | --- |
| 1 | `workers/init_database.sql` + `ensureServerTables` | 新增 `batch_jobs` / `batch_job_items` + 索引 |
| 2 | `worker.js` 新增段落 | `BATCH_KINDS`、`createJob`、`runBatchJob`、`runQueuedBatchJobs`、`handleJobsApi` |
| 3 | `worker.js:10709 handleScheduled` | 开头调用 `runQueuedBatchJobs(env, {maxJobs:3, budgetMs:20000})` |
| 4 | `worker.js:8915 / 8965 / 8953` | 改为入队（保留 `?sync=1`） |
| 5 | `worker.js:10327 / 10338`（MTProto） | 同上 |
| 6 | `worker.js:12658`（Agent 更新） | 同上 |
| 7 | `worker.js:3482`（巡检） | 同上 |
| 8 | 路由挂载 | `/api/jobs` 系列 |
| 9 | `web-antd/src` 新组件 | `JobCenter.jsx`、`useJobs()`、`runJob()` 封装 |
| 10 | 各页面批量按钮 | 改为 `runJob()`，去掉本地 loading 长等待 |
| 11 | `workers/test_jobs_unit.mjs` / `test_jobs_api.mjs` | 执行器、锁、预算、取消、重试、幂等 |
| 12 | `web-antd/scripts/e2e.mjs` | 任务中心与按钮行为 |

---

## 10. 可观测 / 运维

- **任务日志**：任务开始/结束各写一条 `node_push_logs`（`action='batch-job'`），失败项在 `batch_job_items.message`。
- **D1 写入量**：每项 2 次更新（开始/结束）+ 每项 1 次计数更新；100 项 ≈ 300 次写，远低于限额。可优化为「每 5 项合并一次计数更新」。
- **清理**：cron 每天一次，删除 `created_at < now-7d` 或第 200 个之前的任务（先 items 后 jobs）。
- **告警**：连续 3 个任务 `failed` 或某任务运行超过 30 分钟未完成 → 写监控通知（复用 `notifyServerStatusChange` 的邮件通道，可选）。

---

## 11. 效率审查与优化

> 这一节是对「批量任务真正跑起来要花多少 D1 往返 / 多少写」的静态盘点（按函数逐条数 SQL），
> 以及在**不改变业务语义**前提下的优化项。结论先说：**最大的开销不是循环本身，而是「每个节点重写订阅链接」和「每项三次入队查询」**。

### 11.1 单节点 / 单服务器的 D1 往返估算（现状）

| 阶段 | 代码 | 查询数（约） |
| --- | --- | --- |
| 加载上下文 | `loadSingboxNodeContext()` | 3 SELECT（node / template / server） |
| 渲染落库 | `singboxApplyNode()` | 1 SELECT（已有 node_configs）+ 1~2 写 + 1 UPDATE 节点 |
| 排队下发 | `singboxQueueDeployFiles()` | 1 SELECT + 每文件：`loadNodeTemplate` 1 + `buildNodeDeploy`（uuid 为空时 0）+ `agentQueueTask` 2~3 |
| 订阅同步 | `singboxSyncUuidConfigs()` | 2 SELECT + **每条变化链接 1 写**（删除/插入各算一次） |
| 合计（1 个配置文件、无链接变化） | | **≈ 12~16 次** |
| 合计（模板改了端口，10 个用户 × 6 协议） | | 12 + **60 次链接重写** |

`agentQueueTask()` 每次固定 2~3 条：查重 SELECT + 待执行计数 SELECT + INSERT。

### 11.2 瓶颈排序（100 个节点的「一键下发」）

| # | 瓶颈 | 量级 | 说明 |
| --- | --- | --- | --- |
| ① | **订阅链接全量重写** | 节点 × 用户 × 协议 | 端口/域名一改，每条 `ss://` 链接都变；100 节点 × 10 用户 × 6 协议 = **6000 次写**，这是真正的大头 |
| ② | **每项 3 次入队查询** | 节点 × 文件 × 3 | 100 节点 × 1~2 文件 ≈ 300~600 条任务 ≈ **900~1800 次查询** |
| ③ | **逐节点重复加载上下文** | 节点 × 3 | 节点/模板/服务器本可以一次 JOIN 查完（**省 300 次**） |
| ④ | **进度逐项写** | 项 × 2 | 100 项 = 200 次 UPDATE，可合并成 `DB.batch` |
| ⑤ | **物化任务项逐条 INSERT** | 项 | 500 项 = 500 次，可多行 INSERT |
| ⑥ | 前端轮询 | 3s × 管理员数 | 每次都拉整个列表，数据量随历史增长 |

### 11.3 优化清单

| 优化 | 做法 | 预期收益 |
| --- | --- | --- |
| **A. 用「链接指纹」跳过订阅同步** | 不能只看 `applied.changed`：`node.domain` 变化时 ss/anytls 的配置不变、但链接会变。正确做法是：把 `singboxClientLinks()` 的输出做 sha256，与 `singbox_nodes.last_link_hash`（新增列）比对；一致才跳过 `singboxSyncUuidConfigs`，不一致或 `force` 才走同步 | 幂等「一键下发」从 O(节点×链接) 降到 **O(节点)**；100 节点从 ~10s 降到 **<1s**（只多一次本地哈希计算） |
| **B. 链接批量写** | 收集 `inserts[] / deletes[]`，用 `env.DB.batch()` 按 50 条一组提交；删除改 `DELETE ... WHERE id IN (...)` | 6000 次往返 → **~120 次** |
| **C. 上下文一次查完** | `SELECT n.*, s.*, t.* FROM singbox_nodes n LEFT JOIN servers s ...`，循环内只用内存对象；模板 Map 缓存一次 | 省 3×N 次 SELECT |
| **D. 入队批量化** | 预查本服务器 pending 任务的 `payload_hash` 集合（一次 SELECT），循环内只做内存去重，最后多行 INSERT；或加唯一索引 + `INSERT OR IGNORE` | 任务入队从 3×M 次 → **1 + M/50 次** |
| **E. 进度批量写** | 每处理完 1 项只更新内存计数；每 5 项或每 2 秒 `DB.batch([item 更新, job 计数])` 一次 | 写次数降到 1/5 |
| **F. 物化分批** | `INSERT INTO batch_job_items (...) VALUES (...),(...)` 每批 50 行 | 500 项从 500 次 → 10 次 |
| **G. 只读任务列表** | `GET /api/jobs` 用一条聚合 SQL 出 `done/total/failed`，详情才拉 items 且分页 | 列表查询与历史条数解耦 |
| **H. 轮询降频 + 条件请求** | 有运行任务 3s、否则 15s；响应带 `updated_at`，前端比较时间戳跳过渲染 | 空转流量降一个数量级 |
| **I. 有限并发** | 项之间按「服务器」分组，最多 2~3 个并发（不同服务器互不影响）；同一服务器严格串行 | 墙钟时间再降 30%~50% |

### 11.4 修订后的预算

| 场景 | 现状估算 | 优化后估算 |
| --- | --- | --- |
| 100 节点、配置无变化 | ~8–12s，~1000+ 查询 | **<1s，~400 查询**（A+C） |
| 100 节点、端口变更（10 用户/节点） | 25s+，~7500 查询 | **~6–8s，~1500 查询**（A+B+C+D） |
| 20 服务器 × 50 UUID 批量授权 | 组合数直接顶到上限，需分批 | 按服务器拆 job，单 job ≤500 项 |

> 说明：以上是**静态计数**（逐个函数数 SQL），不是压测值；D1 单查询在 1–3ms 量级，实际以 job summary 里的 `durationMs` 为准。

### 11.5 对执行器的两点修订

1. **item 里存「目标 id」而不是快照**：执行时再查最新配置，避免任务排队期间模板被改导致下发旧内容。
2. **`summary` 里记录 `durationMs` / `changed` / `queued`**：每个 kind 的 runner 返回 `detail:{ ms, changed, queued }`，任务结束后汇总，方便定位「哪个 kind 变慢了」。

### 11.6 这些优化不只服务批量任务

A（链接指纹跳过同步，需给 `singbox_nodes` 加一列 `last_link_hash`）和 B（链接批量写）对**现在的同步接口**同样生效：单节点「下发」、UUID 授权、巡检自动修复都会走 `singboxSyncUuidConfigs`。建议把 A/B/C 作为**独立的小 PR 先合**，再上批量任务框架，这样批量任务一上线就是「快」的。

## 12. 风险与对策

| 风险 | 影响 | 对策 |
| --- | --- | --- |
| `waitUntil` 被实例回收 | 任务停在 running | 锁 90s 过期 + cron 续跑；items 幂等 |
| cron 频率只有 1 分钟 | 大任务尾部延迟 | 首次 kick 跑 15~20s；必要时加一个每分钟 cron（若当前不是） |
| 两个 runner 同时跑 | 重复下发 | D1 原子抢锁 + `agentQueueTask` 去重 + `node_configs` 哈希幂等 |
| 任务量爆炸 | D1 膨胀 / 队列拥塞 | 同参数 60s 去重、单任务 ≤500 项、全局并发 ≤3、7 天保留 |
| 取消后仍有项在跑 | 用户以为立即停 | 取消只阻止后续项；UI 明确「当前项跑完后停止」 |
| 失败项重试把成功项再跑一遍 | 重复副作用 | `retry` 只重置 `failed` 项，`cursor` 重置到最小失败 seq |
| 旧脚本依赖同步返回 | 脚本报错 | `?sync=1` / `?wait=1` 兼容 |

---

## 13. 实施拆分

| 阶段 | 内容 | 预估 |
| --- | --- | --- |
| **P0** | 表结构 + 执行器（锁 / 预算 / 进度）+ `handleJobsApi` + 改 `singbox.apply_all`、`agent.update` 两个 kind + 单测 | 1.5 人日 |
| **P0-** | **热路径优化先行**：A 跳过未变更同步、B 链接批量写、C 上下文 JOIN（独立 PR，不改接口） | 1 人日 |
| **P1** | 其余 kind（mtproto、push、巡检）+ 旧接口 `?sync/?wait` 兼容 + 前端任务中心 + 按钮改造 | 1.5 人日 |
| **P2** | 取消 / 重试 / 保留清理 / 并发上限 / 批量多选入口 + e2e | 1 人日 |
| **P3（可选）** | Durable Object 版 runner（秒级续跑） | 1 人日 |

合计约 **4 人日**（不含 P3）。

---

## 14. 验收清单

- [ ] 点「一键下发全部节点」后 **< 500ms** 返回，页面不转圈
- [ ] 任务中心能看到 `done/total` 实时增长，逐项状态可见
- [ ] 关闭浏览器 / 刷新页面，任务继续跑完（cron 兜底）
- [ ] 中途 kill 请求（模拟实例回收），任务最终仍能完成（锁过期 + 续跑）
- [ ] 单个节点渲染失败不影响其他节点，最终状态为 `partial`，可只重试失败项
- [ ] 取消后不再处理新项，已成功的项不回滚
- [ ] 同一操作连点两次只产生一个任务（60s 去重）
- [ ] `?sync=1` 仍返回旧结构（现有脚本/测试不炸）
- [ ] 任务记录 7 天后自动清理
- [ ] 100 节点幂等下发 **< 2s**（无变更时），端口变更场景 **< 10s**
- [ ] 单任务 D1 往返次数可在 summary 里查到，且比现状下降 ≥ 60%
- [ ] `test_jobs_api.mjs` / `test_jobs_unit.mjs` / e2e 全绿

---

## 15. 待确认

1. **旧接口默认切异步** 是否接受？（建议：是，保留 `?sync=1`；前端同步改造）
2. 任务中心做成 **全局抽屉** 还是独立页面 `#jobs`？（建议抽屉 + 顶栏徽标）
3. 是否需要 **批量多选入口**（服务器/节点列表多选后执行多种动作）？还是先只把现有按钮后台化？
4. 任务保留期 7 天 / 200 条是否合适？
