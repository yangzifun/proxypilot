# 方案：内网 IP 直连，毫秒级传输

> 状态：**待实施**
> 目标：Agent ↔ 面板 经内网 IP 直连，控制类消息毫秒级、数据类亚秒级
> 前置：[PLAN_INTERNAL_NETWORK.md](./PLAN_INTERNAL_NETWORK.md)

---

## 一、时延预算

先说清楚"毫秒级"能到什么程度，避免预期错位。

| 环节 | 公网现状 | 内网直连 | 说明 |
|---|---|---|---|
| TCP 建连 | 1 RTT（10–200ms） | 1 RTT（1–10ms） | 仅建连时发生 |
| TLS 握手 | +1~2 RTT | **去掉** | WARP 已加密，无需 TLS |
| WS Upgrade | +1 RTT | +1 RTT | 仅建连时发生 |
| 控制消息 | 0.5 RTT + 排队 | **0.5 RTT ≈ 1–5ms** | 任务/终端按键 |
| 指标采样 | 2000ms | **200ms** | 采样间隔是主要瓶颈 |
| 面板处理 | 1–5ms | 1–5ms | 入库 + 计算 |
| 浏览器推送 | SSE 1s 轮询 | **即时推送** | 去掉 ticker |

**结论**：

- **控制类（任务下发、终端按键、shell 输出）→ 1–10ms 级** ✅
- **监控数据（CPU/流量）→ 200–300ms 级**（受采样间隔限制）⚠️
- 要做到数据也"毫秒级"，采样必须提到 50ms，代价是 CPU 与写库压力，不建议

---

## 二、目标架构

```
节点 Agent
   ├─ 首选  http://100.96.0.18:8080        ← 内网 IP，明文，WARP 加密
   │        ws://100.96.0.18:8080/api/agent/ws
   └─ 兜底  https://agent.yangzifun.org    ← 保留
                    │
        WARP 私有网络（100.64.0.0/10，WireGuard）
                    │
面板 100.96.0.18
   :8080  内网 Agent 专用（明文，仅 Agent 接口）
   :80    公网网页（Cloudflare）
   :443   公网 Agent 兜底
```

---

## 三、具体改动（4 处）

### 3.1 面板：新增内网监听

```go
// main.go
if addr := cfg.AgentInternalAddr; addr != "" {
    go http.ListenAndServe(addr, agentOnlyHandler(api.New(...)))
}
```

```env
PP_AGENT_INTERNAL_ADDR=100.96.0.18:8080
```

- 复用 `agentOnlyHandler` 白名单（只放行 `/api/agent/*`、`/api/shell/agent/*`、`/healthz`）
- 不套 TLS：WARP 隧道已加密，省 1–2 RTT

### 3.2 面板：设置项与下发

```sql
INSERT INTO app_settings (`key`, value)
VALUES ('agent_internal_url', 'http://100.96.0.18:8080');
```

WS 帧推送到 Agent：

```json
{"type":"endpoint","internal":"http://100.96.0.18:8080","external":"https://agent.yangzifun.org"}
```

### 3.3 Agent：选路与毫秒级采样

| 配置 | 默认 | 说明 |
|---|---|---|
| `PROXYPILOT_INTERNAL_URL` | `http://100.96.0.18:8080` | 内网地址 |
| `PROXYPILOT_URL` | `https://agent.yangzifun.org` | 兜底 |
| `PROXYPILOT_METRIC_MS` | `200` | 内网时采样间隔 |
| `PROXYPILOT_METRIC_MS_EXTERNAL` | `2000` | 公网回落时降频 |

逻辑：

```
启动 → 探测内网 /healthz（超时 1s）
  通 → 用内网，采样 200ms
  不通 → 用公网，采样 2000ms
每 60 秒复检内网，恢复后切回（同 session 重建连接）
```

### 3.4 面板：SSE 即时推送（去掉 1 秒 ticker）

现状：`server_stream.go` 每 1 秒对比快照才推。
改为：**指标入库后立即触发广播**，浏览器端 `throttle 100ms` 合并。

```go
// 入库后
hub.broadcast(serverID)   // 立即推送，不再等 ticker
```

---

## 四、实测方法

```bash
# 1. 内网连通性
curl -m 1 -w 'total=%{time_total}s\n' http://100.96.0.18:8080/healthz

# 2. RTT（WS ping）
#    面板侧记录 pong 往返，写入 servers.agent_rtt_ms

# 3. 控制消息端到端
#    面板下发 probe，记录 created_at → finished_at 差值

# 4. 数据新鲜度
#    sampled_at 与 now 的差值 p95
```

目标值：

| 指标 | 目标 |
|---|---|
| 内网 RTT | ≤ 10ms |
| 控制消息端到端 | ≤ 50ms（p95） |
| 数据新鲜度 | ≤ 300ms（p95） |
| 建连耗时（含 WS） | ≤ 30ms |

---

## 五、安全

```
1. 监听仅绑定 100.96.0.18，不监听 0.0.0.0
2. iptables 仅允许 100.64.0.0/10 访问 8080
   iptables -A INPUT -p tcp --dport 8080 ! -s 100.64.0.0/10 -j DROP
3. 握手仍校验 ag_ 令牌
4. 明文传输仅在 WARP 隧道内，公网不可达
```

---

## 六、实施顺序

| 步 | 内容 | 影响 |
|---|---|---|
| 1 | 面板加内网监听 + 设置项 | 无（新端口） |
| 2 | 防火墙放行 100.64.0.0/10 → 8080 | 无 |
| 3 | 单节点手工设 `PROXYPILOT_INTERNAL_URL` 验证 | 单节点 |
| 4 | 全量下发 endpoint 帧，Agent 自动切换 | 可回滚 |
| 5 | 采样间隔 200ms + SSE 即时推送 | 需观察负载 |

**回滚**：`app_settings.agent_internal_url` 清空 → 下次探测（≤60s）自动回公网。

---

## 七、资源影响

18 台 × 200ms 采样 = **90 样本/秒**
- 每条 ~600 字节 → 约 54 KB/s 入站
- 写库：按需聚合（1 秒落 1 条）→ 18 行/秒，MySQL 无压力
- 面板 CPU：18 连接 × 5 帧/秒 = 90 帧/秒，可忽略

若要进一步降负载，可改为**变化触发**（流量无变化不发帧），但会牺牲曲线平滑度。

---

## 八、待确认

1. 端口用 **8080** 可以吗？
2. 采样 **200ms** 是否接受（数据亚秒级）？还是 50ms（更激进，CPU 翻倍）？
3. 是否保留公网 443 兜底？（建议保留）
4. 是否现在就实施？预计 1 轮完成面板+Agent 改造，1 轮验证灰度。