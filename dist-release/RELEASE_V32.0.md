# Release V32.0 — 移除容器部署，改为单机直接安装

发布日期：2026-11-07

## 破坏性变更

- **移除全部容器化部署方案**：删除 `server/Dockerfile` 与 `server/docker-compose.yml`，
  不再提供镜像构建与 Compose 编排。
- ProxyPilot 现在**只支持直接安装在服务器上**：系统包管理器装 MySQL，主程序以二进制 + systemd 运行。

> 如果你之前用 `docker compose up` 部署，请按下方「迁移」一节改用安装脚本；
> 数据库数据可直接 `mysqldump` 导出后导入新库。

## 新增

### 单机安装脚本 `deploy/`

| 脚本 | 用途 |
|---|---|
| `deploy/install.sh` | 全新安装：装 MySQL、建库建用户、生成 `.env`、装 systemd 单元、健康检查 |
| `deploy/upgrade.sh` | 升级二进制：自动备份 → 替换 → 重启 → 健康检查 → **失败自动回滚** |
| `deploy/uninstall.sh` | 卸载：`--purge` 连数据库与系统用户一起删除 |
| `deploy/README.md` | 部署说明、参数、目录结构、常见问题 |

`install.sh` 特性：

- 支持 Debian 12/13、Ubuntu 22.04/24.04/26.04、Rocky/AlmaLinux 9
- 自动生成 `PP_JWT_SECRET` 与管理员初始口令
- 幂等：重复执行只升级二进制与补齐配置，不动已有数据
- 支持 `--dsn` 使用外部 MySQL、`--agent-dist` 一并部署节点 Agent 分发包
- 结束打印后台地址与初始口令

```bash
sudo ./deploy/install.sh --binary ./proxypilot-linux-amd64 --port 80
```

### 发布产物

GitHub Release 现在除二进制外还附带 `install.sh` / `upgrade.sh` / `uninstall.sh` / `deploy/README.md`，
下载后即可在服务器上一键安装。

## 文档

- `readme.md`：快速开始与「生产部署」两节改写为单机安装流程，目录结构补 `deploy/`
- `server/README.md`：所有 `docker compose` 指令替换为安装脚本
- `docs/migration/*`、`docs/architecture-review.md`：清理历史容器描述

## 一并包含的前序修复

- **CPU 指标虚报 100%**：云主机 `steal` 高达 88% 时旧算法把抢占算成自身占用；
  现改用 `busy/(busy+idle)` 并单独给出 `cpuStealPercent`（Agent 1.10.8）
- **Agent 服务探测降频**：由「每服务 2 次 systemctl」改为单次 `systemctl show` 批量查询 + 15 秒缓存
- **端口规则**：支持来源 IP/CIDR、端口范围、编辑；`22` 交由 SSH 安全防护统一管理

## 升级方式

```bash
# 已用旧方式（二进制 + systemd）部署的机器
sudo ./deploy/upgrade.sh --binary ./proxypilot-linux-amd64

# 从容器迁移
mysqldump -h 127.0.0.1 -P 3306 -uroot -p proxypilot > pp.sql
sudo ./deploy/install.sh --binary ./proxypilot-linux-amd64
mysql -uroot proxypilot < pp.sql
sudo systemctl restart proxypilot
```

## 兼容性

- 数据库结构向后兼容，启动时自动迁移
- 配置文件 `.env` 字段不变，旧配置可直接复用
- Agent 1.10.8 支持旧服务端；旧 Agent 也可被新版服务端管理