# Agent 开发与任务协议

> 版本 2.1.1。源码：`agent/`，模块名 `proxypilot-agent`。
> Agent 是节点上唯一的常驻进程，同时承担心跳、指标、任务执行与**内嵌 HTTP/3 VPN 节点**。

## 1. 进程结构

```
proxypilot-agent
 ├─ 读取配置 /etc/proxypilot-agent.conf
 ├─ 心跳循环：每 60s POST /api/agent/poll
 │    ├─ 上报指标（CPU/内存/磁盘/负载/进程/连接/流量）
 │    ├─ 上报服务状态与组件版本
 │    └─ 领取任务（tasks[]）
 ├─ 长连接：WS /api/agent/ws（终端隧道、即时任务唤醒）
 ├─ 任务执行：internal/runner（exec / deploy）
 └─ 内嵌 VPN 节点：读 /etc/proxypilot/agent/vpn.json → vpnnode.Start()
```

## 2. 配置文件

`/etc/proxypilot-agent.conf`（key=value）：

| 键 | 说明 |
|---|---|
| `server` / `endpoint` | 面板地址（含协议） |
| `token` / `agent_key` | 接入令牌（面板生成） |
| `server_id` | 绑定到的服务器 ID |
| `name` | 节点显示名 |
| `interval` | 心跳间隔（秒，默认 60） |
| `metrics_every` | 指标采集频率倍数 |
| `vpn_config` | 内嵌节点配置文件路径（默认 `/etc/proxypilot/agent/vpn.json`） |
| `insecure` | 跳过面板证书校验（自签面板时用） |

## 3. 与面板的协议

### 3.1 首次接入

```
POST /api/agent/register
{ "key": "<一次性令牌>", "name": "台北", "platform": "linux/amd64", "version": "2.1.1" }
→ { "server_id": 16, "secret": "<长期密钥>" }
```

面板把密钥哈希存 `servers.agent_token_hash`，明文只在此刻返回一次。

### 3.2 心跳 / 领任务

```
POST /api/agent/poll
X-Agent-Key: <secret>
{
  "server_id": 16,
  "version": "2.1.1",
  "metrics": {
    "cpuPercent": 19.3, "memPercent": 58.1, "diskPercent": 11,
    "load1": 0.61, "procs": 115, "uptimeSec": 1104716,
    "net": {"rxRate": 10240, "txRate": 20480, "rxBytes": 123, "txBytes": 456},
    "diskIo": {"readRate": 0, "writeRate": 0},
    "conn": {"total": 42, "established": 12},
    "services": {"sshd":"active","sing-box":"active"},
    "versions": {"sing-box":"1.11.0"},
    "agentVersion": "2.1.1"
  }
}
→ {
  "ok": true,
  "tasks": [ { "id": 3445, "type": "exec", "payload": {...} } ],
  "next_poll_sec": 60,
  "agent_latest": "2.1.1",
  "config_version": 12
}
```

面板侧限流：同一 server 距上次 poll < 55s 时本轮跳过（日志会打印）。

### 3.3 回报结果

```
POST /api/agent/ack
{ "results": [ { "task_id": 3445, "status": "done", "exit_code": 0, "output": "..." } ] }
```

`status` 取值：`done` / `failed`。

### 3.4 长连接

```
WS /api/agent/ws?key=<secret>
← {"type":"shell","session":"abc123"}      // 终端隧道
→ {"type":"stdout","data":"..."}
← {"type":"wake"}                          // 新任务唤醒
```

## 4. 任务协议

### 4.1 exec

```json
{
  "command": "systemctl status proxypilot-agent --no-pager",
  "timeout": 60,
  "system_action": "vpn_apply",
  "service": "proxypilot-masque"
}
```

- `timeout`：秒，超时按失败处理
- `system_action`：可选，执行后触发 systemd 操作（`restart` / `reload`）
- 输出：stdout + stderr 合并，截断到 64KB 存 `agent_tasks.output`

### 4.2 deploy

```json
{
  "path": "/etc/sing-box/config.json",
  "content": "{...}",
  "check_cmd": "sing-box check -c {{TARGET_PATH}}",
  "restart_cmd": "systemctl restart sing-box",
  "service": "sing-box"
}
```

流程：写临时文件 → `check_cmd` 校验 → 原子 rename → `restart_cmd` → 回读校验。

## 5. 内嵌 VPN 节点

配置 `/etc/proxypilot/agent/vpn.json`：

```json
{
  "listen": "0.0.0.0:443",
  "cert_file": "/etc/proxypilot/masque/tls/cert.pem",
  "key_file": "/etc/proxypilot/masque/tls/key.pem",
  "node_id": 3,
  "mesh_ip4": "10.99.0.11",
  "client_cidr4": "10.88.1.0/24",
  "gateway_ip4": "10.88.1.1",
  "tun": "ppvpn0",
  "mtu": 1280,
  "max_datagram": 1360,
  "wan": "",
  "public_key": "<base64 Ed25519>",
  "issuer": "proxypilot",
  "audience": "masque-node",
  "dns": ["10.88.1.1"],
  "configure": true
}
```

- 文件不存在 → 跳过内嵌节点（Agent 其它功能正常）
- 启动时创建 TUN、配地址、开 `ip_forward`、写 iptables MASQUERADE
- **改配置需重启 Agent**（部署脚本用 `nohup sh -c 'sleep 1; systemctl restart ...' &` 异步重启，避免掐断任务回报）

## 6. 自升级

```
1. poll 响应里 agent_latest ≠ 本地版本
2. GET <base>/manifest.json?t=<ts>      // base 来自面板设置
3. 选 files[linux-amd64] → 下载到 /tmp
4. 校验 sha256
5. install -m0755 覆盖 /usr/local/bin/proxypilot-agent
6. systemctl restart proxypilot-agent（异步，避免断回报）
```

`base` 当前为存储桶：`https://s3.proxypilot.yangzifun.top/agent`。

## 7. 构建与测试

```bash
cd agent
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w" -o dist/proxypilot-agent-linux-amd64 ./cmd/proxypilot-agent

go test ./...
```

版本常量：`internal/agent/agent.go` → `const Version = "2.1.1"`。

## 8. 排障

| 现象 | 检查 |
|---|---|
| 面板显示离线 | `systemctl status proxypilot-agent`、`journalctl -u proxypilot-agent -n 50` |
| 任务一直 `sent` | 任务里重启了 Agent 自己 → 改用异步重启 |
| 内嵌节点没起 | `ls /etc/proxypilot/agent/vpn.json`、`ss -lunp \| grep 443` |
| VPN 连不上 | 证书是否覆盖域名：`openssl x509 -in cert.pem -noout -ext subjectAltName` |
| 升级后版本没变 | 检查 bin 路径与 systemd `ExecStart` 是否一致 |
