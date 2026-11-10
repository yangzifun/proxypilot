# 运维手册

> 面向部署、升级、日常维护与故障处置。开发细节见 [DEVELOPMENT.md](../DEVELOPMENT.md)。

## 1. 部署

```bash
# 单机部署（Debian 12/13、Ubuntu 22.04+、Rocky/Alma 9）
sudo ./deploy/install.sh --binary ./proxypilot-linux-amd64 --port 80 --agent-dist ./agent-dist

# 升级（自动备份 → 替换 → 健康检查 → 失败回滚）
sudo ./deploy/upgrade.sh --binary ./proxypilot-linux-amd64

# 卸载（--purge 连数据库一起删）
sudo ./deploy/uninstall.sh
```

安装脚本会：装 MySQL、建库建用户、生成 `.env`（含随机 JWT 密钥与 admin 口令）、
写 `proxypilot.service`、轮询 `/healthz` 直到就绪。

## 2. 日常检查清单

```bash
# 面板
systemctl status proxypilot
curl -s localhost/healthz
journalctl -u proxypilot -n 50 --no-pager

# Agent（任一节点）
systemctl status proxypilot-agent
journalctl -u proxypilot-agent -n 50 --no-pager | grep -E 'poll|exec|vpn'

# 磁盘与数据库
df -h / /opt
mysql -uroot -e "SELECT table_schema, ROUND(SUM(data_length+index_length)/1024/1024) AS mb
  FROM information_schema.tables GROUP BY table_schema"
```

## 3. 版本与分发

| 渠道 | 内容 | 操作 |
|---|---|---|
| 面板本地 | `/opt/proxypilot/agent-dist/` | 复制文件 |
| 存储桶（R2） | `agent/` 前缀 | `bash /root/publish-r2.sh` |
| GitHub Release | 面板二进制 + 脚本 | 打 tag `V*` 触发 CI |

**Agent 全量升级**

```bash
TOKEN=$(bash /root/mint_admin.sh)
curl -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"all":true}' "http://127.0.0.1/api/agent/push-update?refresh=1"
# 结果：agent_tasks 与 servers.agent_version
```

**验证**

```bash
mysql -uroot -e "SELECT agent_version, COUNT(*) FROM servers WHERE enabled=1 GROUP BY agent_version" proxypilot
```

## 4. 备份与恢复

```bash
# 导出（面板页面「备份管理」或 CLI）
mysqldump --single-transaction --routines proxypilot | gzip > proxypilot-$(date +%F).sql.gz

# 恢复
gunzip -c proxypilot-YYYY-MM-DD.sql.gz | mysql proxypilot
```

- 建议连同 `/opt/proxypilot/bucket`（附件）与 `.env` 一起备份
- **不要**备份 binlog：曾因 34GB binlog 打满磁盘导致面板挂掉（已在 `deploy/maintenance.sh` 加清理与 crontab）

## 5. 故障处置树

### 5.1 面板打不开

```
curl /healthz 失败？
 ├─ systemctl status proxypilot → 挂了 → journalctl 看原因（多半是 DB 或磁盘）
 ├─ 磁盘满 → df -h / mysql binlog 清理
 └─ 端口被占 → ss -tlnp | grep :80
```

### 5.2 节点离线

```
server 表 last_status=offline
 ├─ 面板能 SSH 到节点？→ 否：网络/防火墙
 ├─ systemctl status proxypilot-agent → 失败原因
 ├─ 任务卡在 sent → 该任务重启了 Agent 自身（改异步重启）
 └─ 内网 IP 节点（172.x）→ 只能靠 Agent 自愈，无法直连排查
```

### 5.3 VPN 连不上

```
客户端报「HTTP/3 鉴权失败」
 ├─ 节点 UDP 443 是否监听：ss -lunp | grep :443
 ├─ vpn.json 是否存在：ls /etc/proxypilot/agent/vpn.json（不存在 = 从未部署）
 ├─ 证书是否覆盖域名：openssl x509 -in cert.pem -noout -ext subjectAltName
 ├─ 域名解析是否指向节点公网 IP：dig +short hk2-node.yzfn.top
 └─ 客户端日志：%LOCALAPPDATA%\ProxyPilot\logs\client.log
```

### 5.4 能连但没流量

```
客户端「隧道流量」↓0 ↑x
 ├─ 节点日志「流隧道流量 rx/tx」
 ├─ rx>0 但 tx=0 → 节点出站/NAT 问题：iptables -t nat -S POSTROUTING
 ├─ 都 0 → 客户端没写入流：查「数据面：首个报文已写入隧道流」
 └─ 域名解析正常但网页打不开 → 检查 MTU（改小到 1280 试）
```

### 5.5 断开后整机断网

```powershell
# 客户端 2.7+ 设置页点「修复网络」；手动：
route delete 0.0.0.0 mask 128.0.0.0
route delete 128.0.0.0 mask 128.0.0.0
Get-DnsClientNrptRule | Where-Object DisplayName -like 'ProxyPilot*' | Remove-DnsClientNrptRule -Force
netsh interface ipv4 set address name="ProxyPilot VPN" source=dhcp
```

### 5.6 DNS 泄露

```
连接后解析器仍显示本地运营商
 ├─ 客户端详情页「DNS 泄露防护」是否「已启用」
 ├─ Get-DnsClientNrptRule 是否有 ProxyPilot 规则
 ├─ 浏览器是否开了 DoH（会走 443，正常，不算泄露）
 └─ 详见 docs/DNS-LEAK.md
```

## 6. 证书

```bash
# 查看当前功能证书
TOKEN=$(bash /root/mint_admin.sh)
curl -s -H "Authorization: Bearer $TOKEN" localhost/api/vpn/cert/status

# 续签（Cloudflare DNS-01）
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"zone":"yzfn.top"}' localhost/api/vpn/cert/issue

# 下发到所有节点
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{}' localhost/api/vpn/cert/distribute
```

内部 CA：`GET /api/vpn/ca.crt`（公开），客户端 `-ca` 指定或自动拉取。

## 7. SSH 防护

节点侧规则（`PP_SSH_GUARD` 链）：

```
-A PP_SSH_GUARD -s 100.96.0.0/12           -j ACCEPT
-A PP_SSH_GUARD -s 10.99.0.0/16            -j ACCEPT
-A PP_SSH_GUARD -s 2606:4700:cf1:1000::/64 -j ACCEPT
-A PP_SSH_GUARD -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
-A PP_SSH_GUARD -j DROP
```

- 自愈：`pp-ssh-guard.timer` 开机 90s + 每 30 分钟重放
- **本机（控制服务器）不做限制**，避免锁死面板
- 开启/关闭：面板「服务器 → SSH 安全防护」

## 8. 常用 SQL

```sql
-- 节点在线情况
SELECT id,name,agent_version,last_status,FROM_UNIXTIME(last_check_at/1000) last
FROM servers WHERE enabled=1 ORDER BY id;

-- 任务积压
SELECT status, COUNT(*) FROM agent_tasks GROUP BY status;

-- 失败任务明细
SELECT id,server_id,type,LEFT(output,300),FROM_UNIXTIME(created_at/1000)
FROM agent_tasks WHERE status='failed' ORDER BY id DESC LIMIT 10;

-- VPN 会话
SELECT id,device_id,node_id,ip4,FROM_UNIXTIME(started_at/1000),bytes_tx,bytes_rx
FROM vpn_sessions ORDER BY id DESC LIMIT 10;

-- 清理 7 天前任务
DELETE FROM agent_tasks WHERE created_at < UNIX_TIMESTAMP(DATE_SUB(NOW(), INTERVAL 7 DAY))*1000;
```

## 9. 脚本清单

| 脚本 | 用途 |
|---|---|
| `deploy/install.sh` | 安装 |
| `deploy/upgrade.sh` | 升级（备份 + 回滚） |
| `deploy/uninstall.sh` | 卸载 |
| `deploy/maintenance.sh` | 磁盘/binlog 清理（配 crontab） |
| `deploy/web-guard/*` | 限制 80/443 仅组网与 CF 回源 |
| `scripts/build-release.ps1` | 一键发布 |
| `/root/redeploy.sh` | 面板重启用（部署用） |
| `/root/publish-r2.sh` | Agent 发布到 R2 |
| `/root/mint_admin.sh` | 生成临时管理员 token |
| `server/tools/smoke.sh` | 冒烟测试 |

## 10. 容量与性能

| 指标 | 正常范围 | 处置 |
|---|---|---|
| 面板 CPU | < 20% | 高则查慢查询与任务积压 |
| MySQL 磁盘 | < 70% | 清理 binlog、归档旧任务/日志 |
| 单节点带宽 | 看套餐 | 面板「用户流量」排行找异常 |
| Agent 内存 | < 80MB | 异常则查任务输出是否超大 |
| VPN 单连接吞吐 | 视 RTT 与窗口 | 见 readme「性能与调优要点」 |

## 11. 安全基线

- 面板只暴露 80/443，由 Nginx/Caddy 终止 TLS
- 后台强制强密码；建议开 TOTP
- `PP_JWT_SECRET` 不落仓库；`.env` 权限 600
- 节点 SSH 只允许组网网段
- 定期 `mysqldump` 备份并异地存放
- 附件存储桶不要开公共读
