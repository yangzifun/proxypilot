# ProxyPilot V24.0 发布说明

发布日期：2026-11-07
上一版本：V23.x 系列

---

## 一、版本概述

V24.0 是一次架构级更新，核心目标是把「节点上报」从 Cloudflare 中转改为**源站直连**，同时把后台与用户端的关键数据做到准实时，并统一全站视觉主题。

主要变化：

1. **双域名架构**：网页走 CDN，节点走源站 443 直连
2. **Agent 1.8.15**：自动迁移直连地址、30 秒统计采集
3. **用户流量页**：完整图表体系 + 用户流向图
4. **服务器页**：SSE 统一推送、多主题
5. **方案 A 代码就绪**：按用户拆入站（默认关闭）

---

## 二、架构变化

### 2.1 双域名

| 域名 | 用途 | 链路 | 证书 |
|---|---|---|---|
| `proxypilot.yangzifun.org` | 管理后台 / 用户端 | Cloudflare → 源站 :80 | Cloudflare |
| `agent.yangzifun.org` | 节点 Agent 上报 | 直连源站 :443 | Let's Encrypt |

### 2.2 443 入口白名单

直连端口仅放行 Agent 相关路径：

```
/api/agent/*
/agent-dist/*
/install.sh
/healthz
```

其他路径返回 404，**源站 IP 无法打开后台页面**。

### 2.3 防火墙

```
-A INPUT -p tcp --dport 80  -j ACCEPT
-A INPUT -p tcp --dport 443 -j ACCEPT
```

> 建议安装 `netfilter-persistent` 固化规则，否则重启后丢失。

---

## 三、Agent 变化（1.8.15）

| 项 | 旧 | 新 |
|---|---|---|
| 指标上报 | 2 秒 | 2 秒（不变） |
| 统计采集 | 45 秒 | **30 秒** |
| 连接地址 | 固定 CDN 域名 | 支持服务端下发 `agent_access_url` 自动迁移 |
| 迁移保护 | — | 先探测 `/healthz`，失败保持原地址；配置备份 `.bak` |

### 迁移机制

```
面板 poll 响应 → agent_access_url
Agent → 探测 https://agent.yangzifun.org/healthz
      → 备份 /etc/proxypilot-agent.conf
      → 改写 PROXYPILOT_URL
      → 退出，systemd 拉起
```

关闭方式：配置 `PROXYPILOT_ALLOW_URL_MIGRATION=0`。

---

## 四、功能更新

### 4.1 服务器页

- 左侧 3×2 指标宫格（活跃/空闲/未接入/上下行/总流量）
- 右侧 3D 点阵地球，节点光点实时刷新
- SSE 单条流统一推送全部服务器状态
  - 心跳 10 秒、重连 1.5 秒
  - 失败回落 2 秒轮询
- 视图切换（卡片/表格）、错误横幅、空状态

### 4.2 用户流量页

| 图表 | 说明 |
|---|---|
| 流量趋势 | 7/14/30 天，可按用户筛选 |
| 全站实时速率 | 15/60/180 分钟，10 秒刷新 |
| Top 10 用户 | 当前区间流量排行 |
| 节点流量 Top 8 | 今日/7 天/累计 |
| 协议分布 | 环形图，按 UUID 数 |
| 在线分布 | 环形图 |
| 用户增长 | 近 30 天新增 UUID |

### 4.3 用户详情流向图

- 左侧用户圆圈（名称显示在圈内）
- 右侧节点列表（国旗图片 + 名称 + 流量）
- 贝塞尔曲线连接，活跃节点绿色高亮 + 流光动画
- 圆圈对齐右列垂直中心

### 4.4 全站主题

顶栏统一切换，登录页、后台、Portal 全部生效：

- 默认 / 翠绿 / 极简 / 扁平 / 毛玻璃
- AntD Token（主色、圆角）同步
- 图表配色随主题
- 持久化到 `localStorage`

---

## 五、配置项

| 位置 | 键 | 默认 | 说明 |
|---|---|---|---|
| 系统管理 → 高级设置 | `agent_access_url` | `https://agent.yangzifun.org` | 节点直连地址，用于安装命令 |
| app_settings | `singbox_per_user_enabled` | `0` | 方案 A 开关（未启用） |
| app_settings | `singbox_port_base` | `20000` | 每用户入站端口起始 |

---

## 六、升级步骤

### 6.1 面板

```bash
# 1. 上传新二进制
scp proxypilot root@<panel>:/opt/proxypilot/proxypilot.new

# 2. 执行部署脚本（自动备份、校验、重启）
ssh root@<panel> 'bash /root/redeploy.sh'

# 3. 校验
curl -s http://127.0.0.1/healthz
```

预期输出：`{"db":"up","ok":true,"service":"proxypilot-api","version":"V24.0"}`

### 6.2 证书（首次启用直连）

```bash
# 临时放行 80 用于 HTTP-01
iptables -I INPUT 1 -p tcp --dport 80 -j ACCEPT

systemctl stop proxypilot
/root/.acme.sh/acme.sh --issue -d agent.yangzifun.org --standalone --httpport 80 --server letsencrypt
/root/.acme.sh/acme.sh --install-cert -d agent.yangzifun.org --ecc \
  --key-file /etc/proxypilot/tls/agent.key \
  --fullchain-file /etc/proxypilot/tls/agent.crt
systemctl start proxypilot
```

证书存在时，进程会自动额外监听 `:443`。

### 6.3 Agent

面板 → 服务器 → 一键更新 Agent（`/api/agent/push-update`），推送 1.8.15。

Agent 会自动：

1. 下载新版本（本地 `/agent-dist` 或 R2）
2. 替换二进制并重启
3. 收到 `agent_access_url` 后迁移到直连

验证：

```sql
SELECT agent_version, COUNT(1) FROM servers GROUP BY agent_version;
-- 期望 1.8.15 / 18
```

---

## 七、验证清单

- [ ] `/healthz` 返回 `V24.0`
- [ ] 18 台节点 `agent_last_seen` 在 6 秒内
- [ ] `ss -tn '( sport = :443 )'` 有大量 ESTAB（直连生效）
- [ ] 用户流量页 30 秒自动刷新
- [ ] 用户详情流向图正常渲染、有流光动画
- [ ] 顶栏主题切换后登录页/后台同步
- [ ] Cloudflare 控制台请求量下降

---

## 八、回滚

### 面板

```bash
ssh root@<panel> 'ls /opt/proxypilot/proxypilot.bak.* | tail -1'
ssh root@<panel> 'cp /opt/proxypilot/proxypilot.bak.<ts> /opt/proxypilot/proxypilot && systemctl restart proxypilot'
```

### Agent

```bash
# 节点上改回旧地址
sudo sed -i 's#https://agent.yangzifun.org#https://proxypilot.yangzifun.org#g' /etc/proxypilot-agent.conf
sudo systemctl restart proxypilot-agent
```

### 证书 / 端口

```bash
rm -f /etc/proxypilot/tls/agent.crt /etc/proxypilot/tls/agent.key
systemctl restart proxypilot      # 443 监听自动关闭
```

---

## 九、已知限制

| 项 | 说明 |
|---|---|
| 用户活跃时间精度 | 30 秒（受 sing-box 计数刷新周期限制） |
| 连接级在线 | 需启用方案 A（按用户拆入站），当前关闭 |
| iptables 持久化 | 需手动安装 `netfilter-persistent` |
| 证书续期 | acme.sh 自动续期，依赖 80 端口开放 |

---

## 十、后续计划（V24.x）

1. 方案 A 试点：洛杉矶单节点、端口段 20000+
2. 订阅端口按用户重写
3. 连接级实时在线
4. WebSocket 常驻通道（可选，降低请求量）

---

## 附：文件变更概览

```
server/internal/api/
  server_stream.go         SSE 统一推送
  uuid_ports.go            方案 A 端口分配
  singbox_per_user.go      按用户入站渲染
  singbox_bundle.go        渲染流程支持追加
  stats_traffic.go         活跃时间 / 节点分布
  stats_traffic_series.go  流量趋势
  stats_traffic_rate.go    实时速率
  stats_users_growth.go    用户增长
  stats_protocols.go       协议分布
server/migrations/
  0025_uuid_port_map.sql
  0026_user_node_recent.sql
web-antd/src/
  theme.js                 全站主题
  pages/UserTraffic.jsx    用户流量页 + 流向图
  pages/ServersUnified.jsx 服务器页
  pages/Settings.jsx       Agent 访问地址
agent/internal/
  agent/agent.go           1.8.15、迁移、30 秒采集
  config/config.go         AllowURLMigration
```
