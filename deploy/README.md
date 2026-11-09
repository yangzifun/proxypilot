# ProxyPilot 单机安装（不使用容器）

本目录提供**直接安装在服务器上**的部署方案：系统包管理器装 MySQL，ProxyPilot 以二进制 + systemd 运行。不再提供 Docker / Docker Compose。

## 文件

| 文件 | 用途 |
|---|---|
| `install.sh` | 全新安装（装 MySQL、建库、生成配置、装 systemd 单元） |
| `upgrade.sh` | 升级二进制（自动备份、健康检查、失败回滚） |
| `uninstall.sh` | 卸载（`--purge` 连数据库一起删） |
| `maintenance.sh` | 磁盘体检与回收（binlog / 旧日志 / 旧备份），建议加入 crontab |
| `web-guard/` | 站点 80/443 仅允许组网 + Cloudflare 回源（可选加固） |

## 支持的系统

- Debian 12 / 13
- Ubuntu 22.04 / 24.04 / 26.04
- Rocky Linux 9 / AlmaLinux 9

其余发行版可自行准备 MySQL 后用 `--dsn` 指向外部数据库。

## 一、准备二进制

在开发机（或 CI）上构建：

```bash
git clone <repo> && cd proxypilot
cd web-antd && node scripts/build.mjs && cd ..        # 构建前端并嵌入 Go
cd server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w" -o ../dist/proxypilot-linux-amd64 ./cmd/proxypilot
```

产物在 `dist/proxypilot-linux-amd64`。

## 二、安装

把 `proxypilot` 与 `deploy/install.sh` 上传到服务器，然后：

```bash
sudo chmod +x install.sh
sudo ./install.sh --binary ./proxypilot --port 80
```

脚本会依次完成：

1. 识别系统并补齐 `curl/tar/openssl`
2. 安装并启动 MySQL（已有则复用），创建数据库 `proxypilot` 与用户，并写入 `skip-log-bin` 配置防止 binlog 写满磁盘
3. 创建系统用户 `proxypilot` 与目录 `/opt/proxypilot`
4. 安装二进制（保留最近备份）
5. 生成 `.env`（自动生成 JWT 密钥与管理员口令）
6. 写入并启动 `proxypilot.service`
7. 健康检查 `http://127.0.0.1:<端口>/healthz`

安装结束会打印后台地址与初始口令。

### 常用参数

```bash
--port 80                 # 监听端口（默认 8080）
--binary ./proxypilot     # 二进制路径
--agent-dist ./agent-dist # 一并部署节点 Agent 分发包
--public-url https://pp.example.com
--dsn "user:pass@tcp(IP:3306)/proxypilot?..."  # 用外部 MySQL
--admin-pass 'YourPass'   # 指定初始管理员口令
-y                        # 全自动（不交互）
```

## 三、升级

```bash
sudo ./upgrade.sh --binary ./proxypilot-linux-amd64
# 或从 URL 拉取
sudo ./upgrade.sh --url https://example.com/proxypilot-linux-amd64
```

升级流程：备份 → 替换 → 重启 → 健康检查；**失败自动回滚**到上一版本。

## 四、卸载

```bash
sudo ./uninstall.sh           # 停服、删程序与配置，保留数据库
sudo ./uninstall.sh --purge   # 连数据库与系统用户一起删
```

## 五、目录与文件

```
/opt/proxypilot/
├── proxypilot            # 主程序
├── .env                  # 运行配置（600，含密钥）
├── proxypilot.bak.*      # 历史版本备份
├── data/bucket/          # 本地对象存储（附件等）
├── agent-dist/           # 节点 Agent 分发（/agent-dist）
└── uploads/              # 本机服务器页面上传目录

/etc/systemd/system/proxypilot.service
```

## 六、反向代理（可选）

面板默认直接监听端口。若前面挂 Cloudflare 或 Nginx：

- 建议面板只监听 `127.0.0.1:<端口>`，由反代转发
- 用 `deploy/web-guard/` 限制 80/443 仅允许组网与 Cloudflare 回源

## 七、磁盘写满（面板「所有页面无数据」）

**症状**：页面能打开，但列表/统计全空；`curl http://127.0.0.1/healthz` 返回 `{"db":"down"}`。

**最常见原因**：MySQL binlog 把磁盘写满。单机面板没有主从复制与按时间点恢复需求，
但 MySQL 默认开启 binlog（`max_binlog_size=100M`，默认保留 30 天），高频写入下几天就能堆到几十 GB，
磁盘 100% 后 MySQL 无法写入，面板所有查询失败。

**排查**

```bash
df -h /                          # 使用率 100%？
du -sh /var/lib/mysql/binlog.* | tail -1
mysql -uroot -e "SHOW VARIABLES LIKE 'log_bin'"
```

**修复**

```bash
# 1) 立即回收
mysql -uroot -e "PURGE BINARY LOGS BEFORE NOW();"

# 2) 永久关闭 binlog（install.sh 已默认写入该配置）
cat > /etc/mysql/mysql.conf.d/99-proxypilot.cnf <<'EOF'
[mysqld]
skip-log-bin
binlog_expire_logs_seconds = 259200
EOF
systemctl restart mysql
```

**日常体检/自动回收**

```bash
sudo ./deploy/maintenance.sh          # 只体检
sudo ./deploy/maintenance.sh --fix    # 清 binlog / 旧日志 / 旧备份

# 建议加入 crontab（每日 04:17）
17 4 * * * /opt/proxypilot/deploy/maintenance.sh --fix >/dev/null 2>&1
```

**其他可能**

| 现象 | 检查 |
|---|---|
| `db: down` 且磁盘正常 | `systemctl status mysql`、`journalctl -u mysql -n 50` |
| 磁盘正常但接口 500 | `journalctl -u proxypilot -n 100`，多为迁移失败 |
| 页面空白无报错 | 浏览器强刷（Ctrl+Shift+R），或检查 `/admin` HTML 是否被 CDN 缓存 |

## 八、常见问题

**服务起不来**

```bash
journalctl -u proxypilot -n 100 --no-pager
```

**数据库连不上**

```bash
mysqladmin ping -h 127.0.0.1
grep PP_MYSQL_DSN /opt/proxypilot/.env
```

**忘记管理员密码**

```bash
sudo sed -i "s|^PP_ADMIN_PASSWORD=.*|PP_ADMIN_PASSWORD=新口令|" /opt/proxypilot/.env
sudo systemctl restart proxypilot
```

**迁移到另一台机器**

```bash
mysqldump -uroot proxypilot > pp.sql       # 旧机导出
mysql -uroot proxypilot < pp.sql           # 新机导入（先建库）
sudo ./install.sh --binary ./proxypilot    # 新机安装
```
