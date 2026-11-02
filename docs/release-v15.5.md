# ProxyPilot V15.4 → V15.5 升级日志

> 版本：V15.5  
> 发布日期：2026-11-02  
> 对比范围：`V15.4`（c66823e）→ `V15.5`（93e53d8）  
> 升级内容：WARP 模块、登录安全加固、用户中心与管理端导航调整

---

## 1. 升级概览

| 类别 | 内容 |
| --- | --- |
| 新增 | Cloudflare WARP（MASQUE）模块：服务端注册、按 UUID 授权、mihomo 配置下载 |
| 新增 | 用户中心「代理服务」一级入口，包含 Telegram 代理与 WARP |
| 调整 | 管理端「运维」拆分为「服务器运维」「节点运维」 |
| 安全 | 修复外部订阅 SSRF；修复登录 Turnstile 绕过；增加登录失败限速；加固 OIDC |
| 修复 | WARP 设置读取失败、启用状态丢失、授权列表排序规则冲突 |
| 数据库 | 新增迁移 `0008_masque_warp.sql`、`0009_masque_warp_collation.sql` |

---

## 2. 新增功能

### 2.1 Cloudflare WARP（MASQUE）

管理端新增 **运维 → 节点运维 → WARP** 页面。

能力：

- 控制面服务器直接调用 Cloudflare WARP 注册接口，无需节点参与
- 每个 UUID 一份 WARP 设备与密钥
- 管理员手动授权 / 重新授权 / 撤销
- 授权列表直接展示全部 UUID，可搜索、可一键授权
- 生成 mihomo `type: masque` 配置
- 支持刷新（删除旧设备并重新注册）
- 撤销时删除云上 WARP 设备并标记本地撤销

数据表：

- `masque_warp_settings`：全局 WARP 设置（SNI、接入 IP、端口、MTU、DNS）
- `masque_warp_profiles`：每个 UUID 一份注册结果（密钥加密存储）

默认配置：

- SNI：`zt-masque.cloudflareclient.com`
- 接入地址：`162.159.198.1/2`、`162.159.199.1/2`、`2606:4700:103::1/2`、`2606:4700:104::1/2`
- 端口：`443, 500, 1701, 4500, 4443, 8443, 8095`
- DNS：`1.1.1.1`、`2606:4700:4700::1111`
- MTU：`1280`

### 2.2 用户中心「代理服务」

用户中心一级入口由「Telegram 代理」改为 **「代理服务」**：

```text
代理服务
├─ Telegram 代理
└─ WARP
```

WARP 卡片：

- 未启用 / 未授权有提示
- 已授权显示 WARP IPv4 / IPv6
- 支持下载 mihomo YAML

### 2.3 管理端导航拆分

```text
服务器运维
├─ 服务器管理
└─ 服务器监控

节点运维
├─ 普通代理（原普通节点）
├─ MTProto
└─ WARP
```

---

## 3. 安全修复

### 3.1 外部订阅源 SSRF

- 新增 `subsource_guard.go`
- 拨号前解析域名并校验所有 IP
- 拦截 loopback / 私网 / 链路本地 / CGNAT / 保留网段
- 只连接校验过的 IP，防 DNS rebinding
- 重定向逐跳校验，最多 5 跳
- 禁用环境代理

### 3.2 登录 Turnstile 绕过与爆破

- 密码 + Turnstile 通过后签发 5 分钟 `mfa_token`
- MFA 第二步只接受 `mfa_token`，不再重发密码
- 不能再通过 `step=verify_totp` 绕过 Turnstile
- 备份码登录同样改为 `mfa_token`
- 新增登录失败限速：10 分钟 8 次失败，锁定 15 分钟

> 兼容性：`/api/mfa/login-with-backup` 请求体由 `{username,password,backup_code}` 改为 `{mfa_token,backup_code}`；前端已同步。

### 3.3 OIDC 加固

- `redirect_uri` 优先使用 `PP_PUBLIC_BASE_URL`
- OIDC 事务 Cookie `Path` 从 `/` 收窄到 `/api/oidc`

---

## 4. 问题修复

| 问题 | 修复 |
| --- | --- |
| WARP 页面提示「读取 WARP 设置失败」 | 修正 SELECT 列顺序（mtu/dns 顺序错误导致 Scan 失败） |
| 启用 WARP 后再打开显示未启用 | 同上；GET 500 导致前端回退默认值 |
| 授权列表提示「读取 WARP 授权失败」 | `masque_warp_profiles.uuid` 与 `uuid_info.uuid` 排序规则冲突，增加 COLLATE 并统一列排序规则 |
| WARP 设置页切换 Tab 后为空 | 表单 Tab 增加 `forceRender`，并提供默认值 |
| 菜单没有 WARP 入口 | 补回 `PAGE_META.masque` |
| 文案仍为 MASQUE | 页面/门户统一改为 WARP |

---

## 5. 数据库变更

升级时由服务自动执行，无需手工导入。

### 0008_masque_warp.sql

- 创建 `masque_warp_settings`
- 创建 `masque_warp_profiles`
- 写入默认 WARP 设置（默认关闭）

### 0009_masque_warp_collation.sql

- 将 `masque_warp_profiles.uuid` 排序规则改为 `utf8mb4_0900_as_cs`
- 与 `uuid_info.uuid` 保持一致，修复 JOIN 报错

> 两个迁移只新增表/调整排序规则，不修改现有业务数据。

---

## 6. 升级步骤

### 6.1 升级前

```bash
# 备份数据库
mysqldump ... > proxypilot-before-v15.5.sql

# 备份二进制
cp /opt/proxypilot/proxypilot /opt/proxypilot/proxypilot.bak.$(date +%Y%m%d%H%M%S)
```

### 6.2 构建

```bash
# 前端（会同步到 server/internal/api/assets_web）
node web-antd/scripts/build.mjs

# 服务端
cd server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w -X main.version=V15.5" \
  -o bin/proxypilot-linux-amd64 ./cmd/proxypilot
```

### 6.3 部署

```bash
# 上传新二进制到 /opt/proxypilot/proxypilot.new
chmod 755 /opt/proxypilot/proxypilot.new
mv /opt/proxypilot/proxypilot.new /opt/proxypilot/proxypilot
systemctl restart proxypilot
```

启动日志应出现：

```text
数据库迁移完成 applied=["0008_masque_warp","0009_masque_warp_collation"]
HTTP 服务已启动 addr=:80 version=V15.5
```

### 6.4 首次配置 WARP

1. 后台 → 节点运维 → WARP → WARP 设置
2. 打开「启用 WARP」并保存
3. 用户授权 → 搜索 UUID → 点击「授权」
4. 用户中心 → 代理服务 → WARP → 下载 YAML
5. mihomo **Alpha 内核**导入（稳定版不支持 `type: masque`）

---

## 7. 验证清单

- [ ] `/admin` boot 版本为 `V15.5`
- [ ] 服务 `systemctl is-active proxypilot` 为 active
- [ ] WARP 设置可正常加载，启用后刷新仍为开启
- [ ] 用户授权列表能显示全部 UUID
- [ ] 授权 UUID 后能下载 YAML
- [ ] mihomo Alpha 连接成功，`curl ifconfig.me` 返回 WARP IP
- [ ] 撤销后旧配置不可用
- [ ] 登录密码 + MFA 正常
- [ ] 现有 sing-box / MTProto 节点与订阅不受影响
- [ ] Agent 轮询与任务正常

---

## 8. 兼容性与限制

### 客户端

- WARP MASQUE 需要 **mihomo Alpha** / ClashMi
- mihomo 稳定版会报 `unsupport proxy type: masque`

### WARP 免费版

- 所有接入点共用同一个 Cloudflare 出口 IP
- 不能选择国家/地区
- 不是 ZTNA / Zero Trust，不能访问私有网络
- 注册接口为非公开 API，存在频控/变更风险；已加限速与错误提示

### 部署

- WARP 不需要节点、CDN、TUN 或 NAT
- 节点服务器无需安装任何新组件
- 不进入 sing-box 订阅，不影响现有协议

---

## 9. 回滚

```bash
cp /opt/proxypilot/proxypilot.bak.<时间戳> /opt/proxypilot/proxypilot
systemctl restart proxypilot
```

- 旧版本会忽略新增的 WARP 表
- `0009` 只调整 `masque_warp_profiles.uuid` 排序规则，不回滚也不影响旧业务
- 如需彻底清理：删除 `masque_warp_profiles`、`masque_warp_settings`，并从 `schema_migrations` 移除 `0008/0009`

---

## 10. 提交与标签

```text
V15.4  c66823e
V15.5  93e53d8
```

主要提交：

| Commit | 内容 |
| --- | --- |
| `dbcd663` | 修复外部订阅 SSRF、登录 Turnstile 绕过并增加登录限速 |
| `595c8fb` | 服务端注册 WARP 并按 UUID 授权，新增 WARP 管理页与门户配置下载 |
| `0d73a11` | 授权页展示 UUID 列表并支持直接授权 |
| `af988e1` | 修复 WARP 授权列表排序规则冲突 |
| `319a438` | 用户中心一级入口改为「代理服务」 |
| `f948510` | 运维拆分为服务器运维 / 节点运维 |
| `93e53d8` | V15.5 版本号与更新日志 |
