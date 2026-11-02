# ProxyPilot Agent

服务器侧常驻程序：**主动出站**连 Worker（`https://<你的域名>`），用**接入密钥**（`ag_` 开头）完成鉴权，
一次 poll 完成「心跳 + 指标上报 + 领取任务」，随后执行任务并回传结果（ack）。

- 依赖只有一个 `github.com/creack/pty`（已 `vendor/` 进仓库，离线可构建），`CGO_ENABLED=0` 静态单文件（linux/amd64 约 6.9MB、linux/arm64 约 6.4MB）
- 与 Worker 的 wire 协议**逐字段对齐**（沿用 V8.1 的服务端实现，服务端无需改动）
- **实时终端（v1.3.0 起）**：poll 带回 `shell.session` 时主动出站连 `wss://<域名>/api/shell/agent/:id`，
  在 PTY（`/bin/bash -il`，无 bash 回落 `/bin/sh -i`）里跑交互式 shell，浏览器输入输出经 Durable Object 双向转发
- 语言/运行形态：Go ≥ 1.21（`build.sh` 已固定 `GOTOOLCHAIN=local`：用本机工具链构建，离线/受限网络亦可）；systemd 常驻服务（`Restart=always`），亦支持 `once`（cron / timer）
- **采集指标（v1.1.0 起）**：主机信息 / CPU（`/proc/stat` 时间片）/ 内存（`/proc/meminfo`）/ 磁盘使用率（`statfs`）/ 负载 / 运行时长 /
  服务状态（`systemctl is-active`）/ 监听端口（`/proc/net/tcp*`）+
  **网络累计流量**（`/proc/net/dev`，排除回环与虚拟接口）、**磁盘 IO 累计读写**（`/proc/diskstats`，排除分区与虚拟设备）、
  **TCP/UDP 连接状态计数**（`/proc/net/{tcp,tcp6,udp,udp6}`）、**进程总数与 Top5**（`/proc/<pid>/stat`）、**CPU 温度**（thermal / hwmon，读不到为 0）

## 目录结构

```
agent/
  cmd/proxypilot-agent/main.go     CLI：run / once / install / uninstall / doctor / version
  internal/config/                 配置解析（/etc/proxypilot-agent.conf，兼容 shell agent 写法）
  internal/metrics/                指标采集（/proc、statfs、systemctl；纯解析函数可单测）
  internal/client/                 poll / ack 客户端 + wire 协议解析
  internal/runner/                 任务执行（deploy / restart / exec / probe）
  internal/state/                  next_poll / last_poll / applied 状态文件
  internal/shell/                  实时终端：最小 WebSocket 客户端 + PTY（v1.3.0）
  internal/agent/                  编排：采集 → poll → 执行 → ack，以及常驻循环
  internal/installer/              写二进制 / 配置 / systemd 单元
  build.sh                         多架构构建 + SHA256SUMS + manifest.json
  install.sh                       安装引导脚本
```

## 构建与发布

```bash
cd agent
./build.sh                     # 版本取自 internal/agent.Version（自动加 v 前缀）
VERSION=v1.0.1 ./build.sh      # 指定版本
TARGETS="linux/amd64" ./build.sh
```

产物在 `dist/`：`proxypilot-agent-linux-<arch>`、`SHA256SUMS`、`manifest.json`（结构见下）。
构建要求：**Go ≥ 1.21**（`go.mod` 下限）；`build.sh` 内已 `export GOTOOLCHAIN=local`，低于下限会直接报「Go 版本过低」，不会尝试联网下载工具链。
上传到对象存储（**扁平布局**：文件直接放桶根目录；Worker 侧无需任何绑定，只需 `AGENT_BINARY_BASE_URL` 指向桶的公开基址）：

```powershell
# Windows（PowerShell + aws-cli；用 -DryRun 先看命令，MinIO 等 S3 兼容服务改 -Endpoint 即可）
$env:AWS_ACCESS_KEY_ID = "…"; $env:AWS_SECRET_ACCESS_KEY = "…"
./publish.ps1 -Endpoint https://s3.yangzifun.org -Bucket proxypilot
```

```bash
# 或 aws-cli
aws --endpoint-url https://s3.yangzifun.org s3 cp dist/ s3://proxypilot/ --recursive --acl public-read \
  --exclude '*' --include 'proxypilot-agent-*' --include 'manifest.json'

# 或 mc（MinIO）
mc cp dist/proxypilot-agent-linux-amd64 dist/proxypilot-agent-linux-arm64 dist/manifest.json myminio/proxypilot/

# 或控制台 / 网盘手工上传：把 dist/proxypilot-agent-* 与 dist/manifest.json 放到桶根目录
```

`manifest.json`（安装脚本从 `<base>/manifest.json` 读取并据此校验下载；`file` 与桶内对象名一致）：

```json
{
  "version": "v1.0.0",
  "released_at": "2026-10-21T06:27:42Z",
  "files": {
    "linux-amd64": { "file": "proxypilot-agent-linux-amd64", "size": 6889632, "sha256": "a7e5…0465" },
    "linux-arm64": { "file": "proxypilot-agent-linux-arm64", "size": 6412288, "sha256": "b8f1…2210" }
  }
}
```

> 换存储基址：Worker 环境变量 `AGENT_BINARY_BASE_URL`（默认 `https://s3.yangzifun.org/proxypilot`），或在安装命令后追加 `--base https://…`。
> 桶里若暂时没有 `manifest.json`，可在安装命令后追加 `--sha256 <hex>` 直接指定校验值。

## 安装 / 升级 / 卸载

安装（后台「服务器管理」列表 / 表单点「安装命令」即可看到明文密钥与安装命令，可随时查看；在目标机执行下述命令。脚本会先 keycheck 验密钥，再从对象存储下载二进制、校验 sha256，写配置与 systemd 单元并启动，最后自动跑一次 doctor）：

```bash
curl -fsSL https://<你的域名>/api/agent/install.sh | sudo sh -s -- --key ag_xxx
```

已有二进制时也可直接安装（需 root）：

```bash
sudo ./proxypilot-agent install --url https://<你的域名> --key ag_xxx
```

升级：重跑安装命令（或替换 `/usr/local/bin/proxypilot-agent` 后 `systemctl restart proxypilot-agent`）。
卸载：`sudo proxypilot-agent uninstall`（状态目录 `/var/lib/proxypilot-agent` 保留，可手动删除）。

## 配置

`/etc/proxypilot-agent.conf`（0600，兼容 shell agent 写法：`KEY=VALUE` / `KEY="VALUE"`）：

| 键 | 说明 |
| --- | --- |
| `PROXYPILOT_URL` | Worker 地址（等价 `origin`），如 `https://pp.example.com` |
| `PROXYPILOT_TOKEN` | 接入密钥（等价 `key`），`ag_` + 64 位十六进制 |
| `INTERVAL` | 轮询间隔兜底值（10–900 秒）；服务端会下发 `next_poll_sec` 覆盖 |
| `STATE_DIR` | 状态目录，默认 `/var/lib/proxypilot-agent` |
| `SERVICE_LIST` | 上报 `systemctl is-active` 的服务名（空格/逗号分隔，非法名忽略） |
| `PROXYPILOT_INSECURE` | `1` 时跳过 TLS 证书校验（仅排查用） |

命令行参数优先于配置文件：`-c/--config`、`--url`、`--key`、`--interval`、`--state-dir`、`--services`、`--insecure`。
环境变量（`PROXYPILOT_*`）在配置文件之后应用，便于临时排查。

## 排错

```bash
sudo proxypilot-agent doctor     # 打印配置摘要 + 一次 poll（不执行任务、不 ack，安全）
journalctl -u proxypilot-agent -f
```

| 现象 | 处理 |
| --- | --- |
| `poll 失败：HTTP 401/403` | 密钥无效、已轮换，或该服务器被禁用；后台重新生成密钥并重装 |
| `poll 失败：HTTP 429` | 轮询过于频繁（服务端 5s 防刷），日志会给出建议重试秒数 |
| 后台显示离线 | 检查 `3 × next_poll_sec` 内是否成功上报；`doctor` 能否拿到响应 |
| 指标全 0 | 非 Linux 或 `/proc` 不可用（`disk`/`mem`/`cpu_ticks` 依赖 `/proc`、`statfs`） |
| deploy 失败 `sha256 不一致` | 传输内容损坏或版本错配；目标文件**不会被改动**（先校验后替换） |

## 与 V8.1 shell agent 的差异

| 项 | shell agent | Go agent |
| --- | --- | --- |
| 运行方式 | systemd timer 一次性执行 | systemd 常驻服务（内置间隔 + 指数退避），亦支持 `once` |
| 依赖 | curl + base64 + coreutils | 无（静态二进制） |
| probe 输出 | `probe ok` | 主机信息摘要（hostname/os/kernel/load/mem/disk/端口/服务） |
| sha256 校验 | 先试 `sha256sum`/`shasum`/`openssl` | 内置 `crypto/sha256` |
| ack 重试 | 单次 | 最多 3 次（退避 2s/5s，服务端幂等） |

wire 协议、任务语义（deploy 的「备份 → 写临时文件 → 校验 → 原子替换 → check → restart → applied」顺序）、
`applied` 状态文件格式与 `next_poll` 节流规则均与 shell agent 保持一致。