# API 参考

> **V1.5.2 清理**：旧 Worker 兜底（`PP_LEGACY_BASE`/501）、`/api/node-templates`、`/api/node-configs/preview|push`、`/api/stats/uuid-details`、`/api/uuid-configs/stats`、`/api/servers/services`、`/api/mihomo/stats` 以及旧页面别名 `/me`、`/ui`、`/manage`、`/manage/entry|all-uuid-info|generate-uuid|uuid-check`、`/admin/certs`、`/admin/settings` 已删除；未注册路径统一 404。


> 共 **377 条路由**，全部注册在 `server/internal/api/router.go`。
> 本文件按模块分组列出，可作为接口总目录；具体请求/响应字段见各 handler 实现。

## 通用约定

### 响应格式

```json
{ "success": true, "data": { } }
{ "success": true, "data": [ ], "total": 128 }
{ "error": "该服务器未开启「允许执行命令」" }
```

- 错误一律用 `writeError(w, status, msg)`，前端读 `error` 字段
- 列表接口统一 `data` + `total`，分页参数 `page`（默认 1）/ `size`（默认 10，上限 200）
- 时间戳全部是**毫秒**

### 鉴权

| 令牌 | 请求头 | 可访问 |
|---|---|---|
| 管理员 | `Authorization: Bearer <jwt>` | `/api/*`；标「仅超管」的需要 `role=super` |
| 用户 | `Authorization: Bearer <portal-jwt>` | `/api/portal/*`、`/manage/*` |
| Agent | `X-Agent-Key: <token>` 或注册时下发的密钥 | `/api/agent/*` |

### 异步任务

批量或跨节点操作返回：

```json
{ "success": true, "async": true, "job_id": 103, "total": 19, "message": "已创建后台任务" }
```

进度查询：`GET /api/jobs/{id}?items=1&limit=200`。

### 状态码

| 码 | 含义 |
|---|---|
| 200 | 成功 |
| 400 | 参数错误 |
| 401 | 未登录 / 令牌过期 |
| 403 | 权限不足（非超管、未开启执行命令…） |
| 404 | 资源不存在 |
| 409 | 冲突（IP 已占用、证书不覆盖域名…） |
| 500 | 服务端错误 |

## 登录与账号（21）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `GET` | `/api/admin/users` | `authed` | 仅超管 |
| `POST` | `/api/admin/users` | `authed` | 仅超管 |
| `PUT` | `/api/admin/users` | `authed` | 仅超管 |
| `DELETE` | `/api/admin/users` | `authed` | 仅超管 |
| `POST` | `/api/admin/users/email/code` | `authed` | 管理员 |
| `POST` | `/api/admin/users/email/unbind` | `authed` | 管理员 |
| `POST` | `/api/admin/users/email/verify` | `authed` | 管理员 |
| `POST` | `/api/csp-report` | `cspReport` | 公开 |
| `POST` | `/api/login` | `login` | 公开 |
| `POST` | `/api/login/email-code` | `loginEmailCodeSend` | 公开 |
| `POST` | `/api/login/email-code/mfa` | `loginEmailCodeMfa` | 公开 |
| `POST` | `/api/login/email-code/verify` | `loginEmailCodeVerify` | 公开 |
| `POST` | `/api/mfa/backup-codes/regenerate` | `authed` | 管理员 |
| `POST` | `/api/mfa/disable` | `authed` | 管理员 |
| `POST` | `/api/mfa/init` | `authed` | 管理员 |
| `POST` | `/api/mfa/login-with-backup` | `mfaLoginWithBackup` | 公开 |
| `GET` | `/api/mfa/status` | `authed` | 管理员 |
| `POST` | `/api/mfa/verify-first` | `authed` | 管理员 |
| `GET` | `/api/oidc/callback` | `oidcCallback` | 公开 |
| `GET` | `/api/oidc/login` | `oidcLogin` | 公开 |
| `GET` | `/api/oidc/status` | `oidcStatus` | 公开 |

## 证书（6）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `GET` | `/api/vpn/ca.crt` | `vpnPublicCACert` | 公开 |
| `GET` | `/api/vpn/ca/status` | `authed` | 仅超管 |
| `POST` | `/api/vpn/cert/distribute` | `authed` | 仅超管 |
| `GET` | `/api/vpn/cert/download` | `authed` | 仅超管 |
| `POST` | `/api/vpn/cert/issue` | `authed` | 仅超管 |
| `GET` | `/api/vpn/cert/status` | `authed` | 仅超管 |

## 组网 Mesh（2）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `GET` | `/api/admin/vpn/mesh` | `authed` | 仅超管 |
| `POST` | `/api/admin/vpn/mesh/batch` | `authed` | 仅超管 |

## VPN（25）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `DELETE` | `/api/admin/vpn/grants/{id}` | `authed` | 仅超管 |
| `GET` | `/api/admin/vpn/nodes` | `authed` | 仅超管 |
| `POST` | `/api/admin/vpn/nodes` | `authed` | 仅超管 |
| `GET` | `/api/admin/vpn/nodes/{id}` | `authed` | 仅超管 |
| `PUT` | `/api/admin/vpn/nodes/{id}` | `authed` | 仅超管 |
| `DELETE` | `/api/admin/vpn/nodes/{id}` | `authed` | 仅超管 |
| `POST` | `/api/admin/vpn/nodes/{id}/action` | `authed` | 仅超管 |
| `POST` | `/api/admin/vpn/nodes/{id}/deploy` | `authed` | 仅超管 |
| `GET` | `/api/admin/vpn/nodes/{id}/grants` | `authed` | 仅超管 |
| `POST` | `/api/admin/vpn/nodes/{id}/grants` | `authed` | 仅超管 |
| `GET` | `/api/admin/vpn/settings` | `authed` | 仅超管 |
| `PUT` | `/api/admin/vpn/settings` | `authed` | 仅超管 |
| `GET` | `/api/admin/vpn/users/{uuid}/access` | `authed` | 仅超管 |
| `PUT` | `/api/admin/vpn/users/{uuid}/access` | `authed` | 仅超管 |
| `GET` | `/api/admin/vpn/users/{uuid}/grants` | `authed` | 仅超管 |
| `POST` | `/api/portal/vpn/connect` | `vpnPortalConnect` | 公开 |
| `POST` | `/api/portal/vpn/device/approve` | `vpnPortalDeviceApprove` | 公开 |
| `GET` | `/api/portal/vpn/devices` | `vpnPortalDevices` | 公开 |
| `DELETE` | `/api/portal/vpn/devices/{id}` | `vpnPortalDeviceItem` | 公开 |
| `POST` | `/api/portal/vpn/disconnect` | `vpnPortalDisconnect` | 公开 |
| `GET` | `/api/portal/vpn/entitlement` | `vpnPortalEntitlement` | 公开 |
| `GET` | `/api/portal/vpn/nodes` | `vpnPortalNodes` | 公开 |
| `POST` | `/api/vpn/device/code` | `vpnDeviceCodeRequest` | 公开 |
| `POST` | `/api/vpn/device/token` | `vpnDeviceCodeToken` | 公开 |
| `POST` | `/api/vpn/mesh/heartbeat` | `vpnMeshHeartbeat` | 公开 |

## 用户（UUID）管理（18）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `GET` | `/api/uuids` | `authed` | 管理员 |
| `POST` | `/api/uuids` | `authed` | 管理员 |
| `PUT` | `/api/uuids` | `authed` | 管理员 |
| `DELETE` | `/api/uuids` | `authed` | 管理员 |
| `GET` | `/api/uuids/{uuid}` | `authed` | 管理员 |
| `PUT` | `/api/uuids/{uuid}` | `authed` | 管理员 |
| `DELETE` | `/api/uuids/{uuid}` | `authed` | 管理员 |
| `GET` | `/api/uuids/{uuid}/configs` | `authed` | 管理员 |
| `POST` | `/api/uuids/{uuid}/configs` | `authed` | 管理员 |
| `GET` | `/api/uuids/{uuid}/configs/{id}` | `authed` | 管理员 |
| `PUT` | `/api/uuids/{uuid}/configs/{id}` | `authed` | 管理员 |
| `DELETE` | `/api/uuids/{uuid}/configs/{id}` | `authed` | 管理员 |
| `GET` | `/api/uuids/{uuid}/nodes` | `authed` | 管理员 |
| `PUT` | `/api/uuids/{uuid}/nodes` | `authed` | 管理员 |
| `POST` | `/api/uuids/{uuid}/nodes` | `authed` | 管理员 |
| `GET` | `/api/uuids/{uuid}/services` | `authed` | 管理员 |
| `PUT` | `/api/uuids/{uuid}/services` | `authed` | 管理员 |

## 订阅与模板（25）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `GET` | `/api/templates` | `templatesList` | 公开 |
| `GET` | `/api/templates/manage` | `authed` | 管理员 |
| `POST` | `/api/templates/manage` | `authed` | 管理员 |
| `PUT` | `/api/templates/manage` | `authed` | 管理员 |
| `DELETE` | `/api/templates/manage` | `authed` | 管理员 |
| `GET` | `/api/templates/manage/{id}` | `authed` | 管理员 |
| `PUT` | `/api/templates/manage/{id}` | `authed` | 管理员 |
| `DELETE` | `/api/templates/manage/{id}` | `authed` | 管理员 |
| `POST` | `/api/templates/manage/set-default` | `authed` | 管理员 |
| `GET` | `/api/templates/manage/{id}/content` | `authed` | 管理员 |
| `PUT` | `/api/templates/manage/{id}/content` | `authed` | 仅超管 |
| `POST` | `/api/templates/manage/{id}/validate` | `authed` | 管理员 |
| `POST` | `/api/templates/manage/{id}/parse` | `authed` | 管理员 |
| `POST` | `/api/templates/manage/{id}/patch-section` | `authed` | 管理员 |
| `POST` | `/api/templates/manage/{id}/publish` | `authed` | 仅超管 |
| `POST` | `/api/templates/manage/{id}/import-url` | `authed` | 仅超管 |
| `POST` | `/api/templates/manage/{id}/preview` | `authed` | 管理员 |
| `GET` | `/api/templates/manage/{id}/versions` | `authed` | 管理员 |
| `GET` | `/api/templates/manage/{id}/versions/{vid}` | `authed` | 管理员 |
| `POST` | `/api/templates/manage/{id}/versions/{vid}/restore` | `authed` | 仅超管 |

### 订阅模板编辑器（Clash/Mihomo，task-20）

模板内容不再只依赖 R2 URL：`config_templates` 增加 `draft_content` / `published_content`
与版本表 `config_template_versions`（迁移 `0057_template_editor.sql`）。
运行时 `GET /clash-config?uuid=&template=` **优先使用 `published_content`**（秒级生效、
不受 S3/CF 抖动影响），为空才回退模板 URL；响应头 `X-Template-Source: db|url`，
`X-Template-Used` 保持兼容（URL）。

- `GET .../content`：返回 `draft_content`/`published_content`/`draft_sha256`/`published_sha256`/
  `content_version`/`source`（draft|published|url）/`content`/`summary`/`managed`/`publishable`；
  无草稿时自动回退已发布内容或 URL。
- `PUT .../content` `{content, base_sha256?}`：保存草稿；`base_sha256` 与当前草稿（无草稿时为
  已发布）不一致返回 409（乐观锁）。
- `POST .../validate` `{content?}`：YAML 语法/重复键/顶层 map、proxy-groups 类型/重名/正则、
  rules 格式与引用（警告）、rule-providers 必填/URL/behavior、dns/tun 类型、proxies 保护。
- `POST .../parse` `{content?}`：返回 `sections{base,dns,tun,proxy_groups,rules,rule_providers}`
  与 `summary{top_keys,proxy_groups,rules,rule_providers,proxies,anchors,aliases,comments,size_bytes}`。
- `POST .../patch-section` `{content,section,data}`：只替换目标区段（保留其它区段注释/锚点），
  `section` 支持 `base/dns/tun/proxy_groups/rules/rule_providers`（也接受 `proxy-groups` 等别名），
  `proxies` 禁止编辑；`base` 语义为按键替换/新增（值为 null 删除），其余区段整体替换。
- `POST .../publish` `{content?}`：校验 → 备份旧 R2 对象到 `rule/configs/.versions/` →
  写回原 URL → 更新 `published_content`/版本/审计；仅允许受管 R2（URL host 为
  `s3.proxypilot.yangzifun.org` 或 `cdn_base_url` 配置域，且 path 以 `/rule/configs/` 开头、
  以 `.yaml/.yml` 结尾），外部模板返回 400「外部模板只读」。
- `POST .../import-url`：从当前 URL 拉取内容写入草稿（迁移/首次编辑）。
- `POST .../preview` `{content?,uuid?}`：用真实 UUID 的订阅节点注入 proxies，返回完整 YAML 与
  统计；**不写访问日志**。
- `GET .../versions`、`GET .../versions/{vid}`、`POST .../versions/{vid}/restore`（恢复为草稿）。

安全：内容 ≤1MB、UTF-8、禁止自定义 YAML tag、别名 ≤500、节点数/嵌套深度限制；
`proxies` 为系统注入位，只允许空或直连占位。

## 服务器与 Agent（67）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `POST` | `/api/agent/ack` | `agentAck` | 公开 |
| `GET` | `/api/agent/bootstrap` | `agentBootstrap` | 公开 |
| `POST` | `/api/agent/bootstrap` | `agentBootstrap` | 公开 |
| `GET` | `/api/agent/install.sh` | `agentInstallScript` | 公开 |
| `GET` | `/api/agent/keycheck` | `agentKeycheck` | 公开 |
| `POST` | `/api/agent/keycheck` | `agentKeycheck` | 公开 |
| `GET` | `/api/agent/latest` | `authed` | 管理员 |
| `POST` | `/api/agent/poll` | `agentPoll` | 公开 |
| `POST` | `/api/agent/push-update` | `authed` | 管理员 |
| `GET` | `/api/agent/tasks` | `authed` | 管理员 |
| `POST` | `/api/agent/tasks` | `authed` | 仅超管 |
| `DELETE` | `/api/agent/tasks/{id}` | `authed` | 管理员 |
| `POST` | `/api/agent/tasks/{id}/cancel` | `authed` | 管理员 |
| `POST` | `/api/agent/vpn/heartbeat` | `vpnAgentHeartbeat` | 公开 |
| `POST` | `/api/agent/vpn/register` | `vpnAgentRegister` | 公开 |
| `GET` | `/api/agent/wait` | `agentWait` | 公开 |
| `POST` | `/api/agent/wait` | `agentWait` | 公开 |
| `GET` | `/api/agent/ws` | `agentWS` | 公开 |
| `GET` | `/api/local/files` | `authed` | 仅超管 |
| `DELETE` | `/api/local/files/{name}` | `authed` | 仅超管 |
| `GET` | `/api/local/firewall` | `authed` | 仅超管 |
| `POST` | `/api/local/firewall` | `authed` | 仅超管 |
| `PUT` | `/api/local/firewall/{id}` | `authed` | 仅超管 |
| `DELETE` | `/api/local/firewall/{id}` | `authed` | 仅超管 |
| `POST` | `/api/local/firewall/sync` | `authed` | 仅超管 |
| `GET` | `/api/local/overview` | `authed` | 仅超管 |
| `GET` | `/api/local/ports` | `authed` | 仅超管 |
| `GET` | `/api/local/processes` | `authed` | 仅超管 |
| `GET` | `/api/local/ssh-guard` | `authed` | 仅超管 |
| `POST` | `/api/local/ssh-guard` | `authed` | 仅超管 |
| `POST` | `/api/local/upload` | `authed` | 仅超管 |
| `GET` | `/api/monitor/notify` | `authed` | 管理员 |
| `POST` | `/api/monitor/notify` | `authed` | 管理员 |
| `POST` | `/api/monitor/notify/test` | `authed` | 管理员 |
| `GET` | `/api/monitor/uuid-check` | `authed` | 管理员 |
| `POST` | `/api/monitor/uuid-check` | `authed` | 管理员 |
| `POST` | `/api/nodes/{id}/apply` | `authed` | 管理员 |
| `GET` | `/api/nodes/{id}/connections` | `authed` | 管理员 |
| `GET` | `/api/nodes/bindings` | `authed` | 管理员 |
| `GET` | `/api/nodes/check` | `authed` | 管理员 |
| `POST` | `/api/nodes/check/fix` | `authed` | 管理员 |
| `GET` | `/api/servers` | `authed` | 管理员 |
| `POST` | `/api/servers` | `authed` | 管理员 |
| `GET` | `/api/servers/{id}` | `authed` | 管理员 |
| `PUT` | `/api/servers/{id}` | `authed` | 管理员 |
| `DELETE` | `/api/servers/{id}` | `authed` | 管理员 |
| `GET` | `/api/servers/{id}/{action}` | `authed` | 管理员 |
| `POST` | `/api/servers/{id}/{action}` | `authed` | 管理员 |
| `POST` | `/api/servers/{id}/agent-token` | `authed` | 管理员 |
| `GET` | `/api/servers/{id}/ssh-guard` | `authed` | 管理员 |
| `POST` | `/api/servers/{id}/ssh-guard` | `authed` | 仅超管 |
| `GET` | `/api/servers/{id}/traffic-limit` | `authed` | 管理员 |
| `PUT` | `/api/servers/{id}/traffic-limit` | `authed` | 仅超管 |
| `POST` | `/api/servers/{id}/traffic-limit/reset` | `authed` | 仅超管 |
| `POST` | `/api/servers/{id}/traffic-limit/unsuspend` | `authed` | 仅超管 |
| `GET` | `/api/servers/{id}/unlock` | `authed` | 管理员 |
| `POST` | `/api/servers/{id}/unlock` | `authed` | 管理员 |
| `GET` | `/api/servers/monitor` | `authed` | 管理员 |
| `GET` | `/api/servers/status` | `authed` | 管理员 |
| `GET` | `/api/servers/stream` | `sseServersStream` | 公开 |
| `GET` | `/api/servers/traffic-limits` | `authed` | 管理员 |
| `GET` | `/api/shell/agent/{id}` | `shellAgent` | 公开 |
| `GET` | `/api/shell/browser/{id}` | `shellBrowser` | 公开 |
| `GET` | `/api/shell/local` | `shellLocal` | 公开 |
| `GET` | `/api/shell/precheck/{id}` | `authed` | 管理员 |
| `GET` | `/api/shell/status` | `authed` | 管理员 |

## sing-box 节点（34）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `GET` | `/api/node-configs` | `authed` | 管理员 |
| `POST` | `/api/node-configs` | `authed` | 管理员 |
| `GET` | `/api/node-configs/{id}` | `authed` | 管理员 |
| `PUT` | `/api/node-configs/{id}` | `authed` | 管理员 |
| `DELETE` | `/api/node-configs/{id}` | `authed` | 管理员 |
| `POST` | `/api/node-configs/{id}/push` | `authed` | 管理员 |
| `GET` | `/api/node-configs/logs` | `authed` | 管理员 |
| `GET` | `/api/singbox` | `authed` | 管理员 |
| `POST` | `/api/singbox/apply-all` | `authed` | 管理员 |
| `POST` | `/api/singbox/deploy-all` | `authed` | 管理员 |
| `GET` | `/api/singbox/nodes` | `authed` | 管理员 |
| `POST` | `/api/singbox/nodes` | `authed` | 管理员 |
| `PUT` | `/api/singbox/nodes/{id}` | `authed` | 管理员 |
| `DELETE` | `/api/singbox/nodes/{id}` | `authed` | 管理员 |
| `GET` | `/api/singbox/nodes/{id}` | `authed` | 管理员 |
| `POST` | `/api/singbox/nodes/{id}/apply` | `authed` | 管理员 |
| `POST` | `/api/singbox/nodes/{id}/deploy` | `authed` | 管理员 |
| `GET` | `/api/singbox/nodes/{id}/links` | `authed` | 管理员 |
| `GET` | `/api/singbox/nodes/{id}/preview` | `authed` | 管理员 |
| `POST` | `/api/singbox/nodes/{id}/warp` | `authed` | 管理员 |
| `POST` | `/api/singbox/preview` | `authed` | 管理员 |
| `POST` | `/api/singbox/push` | `authed` | 管理员 |
| `POST` | `/api/singbox/reconcile` | `authed` | 管理员 |
| `GET` | `/api/singbox/releases` | `authed` | 管理员 |
| `GET` | `/api/singbox/templates` | `authed` | 管理员 |
| `POST` | `/api/singbox/templates` | `authed` | 管理员 |
| `GET` | `/api/singbox/templates/{id}` | `authed` | 管理员 |
| `PUT` | `/api/singbox/templates/{id}` | `authed` | 管理员 |
| `DELETE` | `/api/singbox/templates/{id}` | `authed` | 管理员 |
| `POST` | `/api/singbox/templates/from-config` | `authed` | 管理员 |
| `POST` | `/api/singbox/templates/preview` | `authed` | 管理员 |
| `GET` | `/api/singbox/uuids` | `authed` | 管理员 |

## mihomo（26）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `GET` | `/api/mihomo/bin/{arch}` | `mihomoBin` | 公开 |
| `GET` | `/api/mihomo/check` | `authed` | 管理员 |
| `POST` | `/api/mihomo/check/fix` | `authed` | 管理员 |
| `GET` | `/api/mihomo/export` | `authed` | 管理员 |
| `GET` | `/api/mihomo/nodes` | `authed` | 管理员 |
| `POST` | `/api/mihomo/nodes` | `authed` | 管理员 |
| `PUT` | `/api/mihomo/nodes/{id}` | `authed` | 管理员 |
| `DELETE` | `/api/mihomo/nodes/{id}` | `authed` | 管理员 |
| `POST` | `/api/mihomo/nodes/{id}/apply` | `authed` | 管理员 |
| `GET` | `/api/mihomo/nodes/{id}/preview` | `authed` | 管理员 |
| `POST` | `/api/mihomo/nodes/{id}/render` | `authed` | 管理员 |
| `POST` | `/api/mihomo/nodes/bulk` | `authed` | 管理员 |
| `POST` | `/api/mihomo/report` | `mihomoConnReport` | 公开 |
| `GET` | `/api/mihomo/templates` | `authed` | 管理员 |
| `POST` | `/api/mihomo/templates` | `authed` | 管理员 |
| `GET` | `/api/mihomo/templates/{id}` | `authed` | 管理员 |
| `PUT` | `/api/mihomo/templates/{id}` | `authed` | 管理员 |
| `DELETE` | `/api/mihomo/templates/{id}` | `authed` | 管理员 |
| `POST` | `/api/mihomo/templates/{id}/enabled` | `authed` | 管理员 |
| `GET` | `/api/mihomo/templates/{id}/versions` | `authed` | 管理员 |
| `GET` | `/api/mihomo/templates/{id}/versions/{vid}` | `authed` | 管理员 |
| `POST` | `/api/mihomo/templates/{id}/versions/{vid}/restore` | `authed` | 管理员 |
| `POST` | `/api/mihomo/templates/parse` | `authed` | 管理员 |
| `POST` | `/api/mihomo/templates/preview-server` | `authed` | 管理员 |
| `GET` | `/api/mihomo/traffic/summary` | `authed` | 管理员 |

## MTProto（27）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `GET` | `/api/mtproto` | `authed` | 管理员 |
| `POST` | `/api/mtproto/apply-all` | `authed` | 管理员 |
| `DELETE` | `/api/mtproto/clients/{id}` | `authed` | 管理员 |
| `GET` | `/api/mtproto/clients/{id}/link` | `authed` | 管理员 |
| `POST` | `/api/mtproto/clients/{id}/restore` | `authed` | 管理员 |
| `POST` | `/api/mtproto/clients/{id}/revoke` | `authed` | 管理员 |
| `POST` | `/api/mtproto/clients/{id}/rotate` | `authed` | 管理员 |
| `POST` | `/api/mtproto/clients/{id}/send-mail` | `authed` | 管理员 |
| `GET` | `/api/mtproto/nodes` | `authed` | 管理员 |
| `POST` | `/api/mtproto/nodes` | `authed` | 管理员 |
| `GET` | `/api/mtproto/nodes/{id}` | `authed` | 管理员 |
| `PUT` | `/api/mtproto/nodes/{id}` | `authed` | 管理员 |
| `DELETE` | `/api/mtproto/nodes/{id}` | `authed` | 管理员 |
| `POST` | `/api/mtproto/nodes/{id}/apply` | `authed` | 管理员 |
| `GET` | `/api/mtproto/nodes/{id}/clients` | `authed` | 管理员 |
| `POST` | `/api/mtproto/nodes/{id}/clients` | `authed` | 管理员 |
| `POST` | `/api/mtproto/nodes/{id}/preview` | `authed` | 管理员 |
| `POST` | `/api/mtproto/push` | `authed` | 管理员 |
| `POST` | `/api/mtproto/reconcile` | `authed` | 管理员 |
| `GET` | `/api/mtproto/templates` | `authed` | 管理员 |
| `POST` | `/api/mtproto/templates` | `authed` | 管理员 |
| `GET` | `/api/mtproto/templates/{id}` | `authed` | 管理员 |
| `PUT` | `/api/mtproto/templates/{id}` | `authed` | 管理员 |
| `DELETE` | `/api/mtproto/templates/{id}` | `authed` | 管理员 |
| `POST` | `/api/mtproto/templates/{id}/apply` | `authed` | 管理员 |
| `POST` | `/api/mtproto/templates/{id}/preview` | `authed` | 管理员 |
| `GET` | `/api/mtproto/uuids` | `authed` | 管理员 |

## WARP / MASQUE（7）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `GET` | `/api/masque/warp/profiles` | `authed` | 管理员 |
| `POST` | `/api/masque/warp/profiles` | `authed` | 管理员 |
| `DELETE` | `/api/masque/warp/profiles/{uuid}` | `authed` | 管理员 |
| `GET` | `/api/masque/warp/profiles/{uuid}/config` | `authed` | 管理员 |
| `POST` | `/api/masque/warp/profiles/{uuid}/refresh` | `authed` | 管理员 |
| `GET` | `/api/masque/warp/settings` | `authed` | 管理员 |
| `PUT` | `/api/masque/warp/settings` | `authed` | 管理员 |

## CDN 优选（12）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `GET` | `/api/cdn-preferred` | `authed` | 管理员 |
| `POST` | `/api/cdn-preferred` | `authed` | 仅超管 |
| `PUT` | `/api/cdn-preferred/{id}` | `authed` | 仅超管 |
| `DELETE` | `/api/cdn-preferred/{id}` | `authed` | 仅超管 |
| `POST` | `/api/cdn-preferred/check` | `authed` | 仅超管 |
| `POST` | `/api/cdn-preferred/test` | `authed` | 管理员 |
| `GET` | `/api/cdn-preferred/enabled` | `authed` | 管理员 |
| `PUT` | `/api/cdn-preferred/enabled` | `authed` | 仅超管 |
| `GET` | `/api/cdn-preferred/groups` | `authed` | 管理员 |
| `POST` | `/api/cdn-preferred/import` | `authed` | 仅超管 |
| `GET` | `/api/cdn-preferred/settings` | `authed` | 管理员 |
| `PUT` | `/api/cdn-preferred/settings` | `authed` | 仅超管 |

### CDN 优选：单地址测试（task-18）

`POST /api/cdn-preferred/test` 由**面板进程本身**发起 TCP→TLS→HTTP 测试（不经过节点/Agent），
用于「检测设置」里对某条记录或任意地址单独测速。复用 SSRF 防护：解析结果命中内网/保留地址直接拒绝。

请求二选一：

```json
{"id": 123}
{"address": "162.159.198.1", "port": 443, "sni": "cdn.example.com", "path": "/vlws"}
```

- `id`：取库里该记录的 `address`/`port`（记录不存在 404）；`address`：直接测试任意地址；
- `port` 默认 443；`sni`/`path` 不传时取全局检测设置（`/api/cdn-preferred/settings`），
  显式传空串表示跳过该阶段；
- 成功 200：

```json
{"success": true, "data": {
  "address": "162.159.198.1", "port": 443, "resolved": ["104.16.x.x"],
  "ok": true, "latency_ms": 42,
  "tcp_ok": true, "tls_ok": true, "http_status": 101, "error": ""
}}
```

- 测试失败仍返回 200（`ok=false`，`error` 以 `TCP:` / `TLS:` / `HTTP:` / `目标地址不允许` 开头说明阶段）；
  参数错误 400、id 不存在 404。`http_status` 仅在配置了 `path` 时非 0；未配置 SNI 时只做 TCP 连通性检查。

> 分组说明：默认分组已从 `default` 改名为 `cloudflare`（迁移 `0056_cdn_preferred_cloudflare_group.sql`）。
> 创建/更新/导入时 `group_name` 为空或 `default` 会统一写入 `cloudflare`；模板里显式配置
> `group=default` 的旧配置仍然映射到 `cloudflare`。其它分组（如 `aliyun`/`tencent`/`overseas`）
> 原样保留并隔离，用于其他类型 CDN 优选。

## 统计与日志（19）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `GET` | `/api/jobs` | `authed` | 管理员 |
| `POST` | `/api/jobs` | `authed` | 管理员 |
| `GET` | `/api/jobs/{id}` | `authed` | 管理员 |
| `POST` | `/api/jobs/{id}/cancel` | `authed` | 管理员 |
| `POST` | `/api/jobs/{id}/retry` | `authed` | 管理员 |
| `POST` | `/api/jobs/clear` | `authed` | 管理员 |
| `POST` | `/api/logs/cleanup` | `authed` | 管理员 |
| `GET` | `/api/logs/summary` | `authed` | 管理员 |
| `GET` | `/api/stats` | `authed` | 管理员 |
| `GET` | `/api/stats/protocols` | `authed` | 管理员 |
| `POST` | `/api/stats/traffic/clear` | `authed` | 管理员 |
| `GET` | `/api/stats/traffic/flow` | `authed` | 管理员 |
| `GET` | `/api/stats/traffic/nodes` | `authed` | 管理员 |
| `GET` | `/api/stats/traffic/rate` | `authed` | 管理员 |
| `GET` | `/api/stats/traffic/series` | `authed` | 管理员 |
| `GET` | `/api/stats/traffic/users` | `authed` | 管理员 |
| `GET` | `/api/stats/traffic/users/{uuid}` | `authed` | 管理员 |
| `GET` | `/api/stats/users/growth` | `authed` | 管理员 |

## 附件与存储（14）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `GET` | `/api/attachments` | `authed` | 管理员 |
| `DELETE` | `/api/attachments` | `authed` | 管理员 |
| `GET` | `/api/attachments/content` | `authed` | 管理员 |
| `PUT` | `/api/attachments/content` | `authed` | 管理员 |
| `POST` | `/api/attachments/copy` | `authed` | 管理员 |
| `GET` | `/api/attachments/download` | `authed` | 管理员 |
| `POST` | `/api/attachments/folder` | `authed` | 管理员 |
| `POST` | `/api/attachments/move` | `authed` | 管理员 |
| `GET` | `/api/attachments/status` | `authed` | 管理员 |
| `POST` | `/api/attachments/upload` | `authed` | 管理员 |
| `GET` | `/api/bucket/file` | `authed` | 管理员 |
| `POST` | `/api/bucket/file` | `authed` | 管理员 |
| `PUT` | `/api/bucket/file` | `authed` | 管理员 |
| `GET` | `/api/bucket/files` | `authed` | 管理员 |

## 系统设置（10）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `GET` | `/api/apikeys` | `authed` | 仅超管 |
| `POST` | `/api/apikeys` | `authed` | 仅超管 |
| `PUT` | `/api/apikeys/{id}` | `authed` | 仅超管 |
| `DELETE` | `/api/apikeys/{id}` | `authed` | 仅超管 |
| `POST` | `/api/apikeys/{id}/rotate` | `authed` | 仅超管 |
| `GET` | `/api/backup/export` | `authed` | 管理员 |
| `POST` | `/api/backup/import` | `authed` | 管理员 |
| `GET` | `/api/settings` | `authed` | 仅超管 |
| `PUT` | `/api/settings` | `authed` | 仅超管 |
| `POST` | `/api/settings` | `authed` | 仅超管 |

## 用户中心（31）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `POST` | `/api/portal/email/send-code` | `portalEmailSendCode` | 公开 |
| `POST` | `/api/portal/email/verify` | `portalEmailVerify` | 公开 |
| `POST` | `/api/portal/login` | `portalLogin` | 公开 |
| `GET` | `/api/portal/masque` | `masqueWarpPortal` | 公开 |
| `GET` | `/api/portal/masque/config` | `masqueWarpPortalConfig` | 公开 |
| `GET` | `/api/portal/mtproto` | `portalMtproto` | 公开 |
| `POST` | `/api/portal/mtproto/{id}/deliver` | `portalMtprotoDeliver` | 公开 |
| `POST` | `/api/portal/mtproto/{id}/send-mail` | `portalMtprotoSendMail` | 公开 |
| `POST` | `/api/portal/send-code` | `portalSendCode` | 公开 |
| `GET` | `/api/portal/traffic` | `portalTraffic` | 公开 |
| `GET` | `/api/portal/uuids` | `portalUuids` | 公开 |
| `GET` | `/manage` | `pageIndex` | 公开 |
| `GET` | `/manage/` | `pageIndex` | 公开 |
| `POST` | `/manage/configs` | `manageConfigsAdd` | 公开 |
| `PUT` | `/manage/configs` | `manageConfigsUpdate` | 公开 |
| `GET` | `/manage/configs/{uuid}` | `manageConfigsList` | 公开 |
| `DELETE` | `/manage/configs/{uuid}` | `manageConfigsClear` | 公开 |
| `DELETE` | `/manage/configs/batch` | `manageConfigsBatchDelete` | 公开 |
| `DELETE` | `/manage/configs/id/{id}` | `manageConfigDeleteByID` | 公开 |
| `GET` | `/manage/stats/{uuid}` | `manageStats` | 公开 |
| `GET` | `/manage/uuid-info` | `manageUUIDInfoGet` | 公开 |
| `POST` | `/manage/uuid-info` | `manageUUIDInfoSave` | 公开 |
| `GET` | `/manage/uuid-info/{uuid}` | `manageUUIDInfoGet` | 公开 |
| `GET` | `/manage/uuid-sub-sources` | `manageSubSourcesList` | 公开 |
| `POST` | `/manage/uuid-sub-sources` | `manageSubSourcesAdd` | 公开 |
| `PUT` | `/manage/uuid-sub-sources` | `manageSubSourcesUpdate` | 公开 |
| `DELETE` | `/manage/uuid-sub-sources` | `manageSubSourcesDelete` | 公开 |

## 其他（35）

| 方法 | 路径 | Handler | 权限 |
|---|---|---|---|
| `GET` | `/{$}` | `pageIndex` | 公开 |
| `GET` | `/admin` | `pageIndex` | 公开 |
| `GET` | `/admin-app.css` | `adminAppCSS` | 公开 |
| `GET` | `/admin-app.js` | `adminAppJS` | 公开 |
| `GET` | `/admin/` | `pageIndex` | 公开 |
| `GET` | `/agent-dist/` | `agentDistFile` | 公开 |
| `GET` | `/agent/install.sh` | `agentInstallScript` | 公开 |
| `POST` | `/api/change-password` | `authed` | 管理员 |
| `POST` | `/api/client-auth/approve` | `clientAuthApprove` | 公开 |
| `GET` | `/api/client-auth/poll` | `clientAuthPoll` | 公开 |
| `POST` | `/api/client-auth/start` | `clientAuthStart` | 公开 |
| `POST` | `/api/cloudflare/check` | `authed` | 仅超管 |
| `GET` | `/api/flag/{cc}` | `flagImage` | 公开 |
| `GET` | `/api/traffic/nodes` | `authed` | 管理员 |
| `GET` | `/assets/` | `pageAssets` | 公开 |
| `GET` | `/assets/world.svg` | `worldSVG` | 公开 |
| `GET` | `/batch-configs/{uuid}` | `batchSubscription` | 公开 |
| `GET` | `/clash-config` | `clashConfig` | 公开 |
| `GET` | `/client-auth` | `clientAuthPage` | 公开 |
| `GET` | `/favicon.ico` | `favicon` | 公开 |
| `GET` | `/favicon.svg` | `favicon` | 公开 |
| `GET` | `/g6-app.js` | `g6AppJS` | 公开 |
| `GET` | `/healthz` | `health` | 公开 |
| `GET` | `/install.sh` | `agentInstallScript` | 公开 |
| `GET` | `/login` | `pageIndex` | 公开 |
| `GET` | `/login/` | `pageIndex` | 公开 |
| `GET` | `/sub/{uuid}` | `rawSubscription` | 公开 |
| `GET` | `/sub/mihomo/{uuid}` | `mihomoSubscribe` | 公开 |
| `GET` | `/xterm-app.js` | `xtermAppJS` | 公开 |

## 主要接口示例

### 登录（密码）

```bash
curl -X POST https://<面板>/api/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"***","turnstileToken":"..."}'
# → {"success":true,"token":"eyJ...","needMfa":false}
```

### 用户换票（VPN 客户端）

```bash
curl -X POST https://<面板>/api/portal/vpn/connect \
  -H "Authorization: Bearer <portal-jwt>" \
  -H 'Content-Type: application/json' \
  -d '{"device_id":3,"node_id":4}'
# → {"session_id":123,"token":"<节点 JWT>","ip4":"10.88.2.10","mtu":1280,
#     "client_cidr":"10.88.2.0/24","dns":["10.88.2.1"],"mesh_cidr":"10.99.0.0/16",
#     "node":{"id":4,"name":"香港-2","endpoint":"hk2-node.yzfn.top:443"},
#     "tls":{"server_name":"hk2-node.yzfn.top","mode":"acme"},"expires_in":1200}
```

### 给节点下发命令

```bash
curl -X POST https://<面板>/api/agent/tasks \
  -H "Authorization: Bearer <admin-jwt>" \
  -H 'Content-Type: application/json' \
  -d '{"server_id":16,"type":"exec","title":"检查","command":"systemctl status proxypilot-agent --no-pager"}'
```

### 部署 VPN 节点

```bash
curl -X POST https://<面板>/api/admin/vpn/nodes/4/deploy \
  -H "Authorization: Bearer <admin-jwt>" -H 'Content-Type: application/json' -d '{}'
# → {"ok":true,"task_id":3445,"server_id":13,"embedded":true}
```

### 推全量 Agent 升级

```bash
curl -X POST "https://<面板>/api/agent/push-update?refresh=1" \
  -H "Authorization: Bearer <admin-jwt>" -H 'Content-Type: application/json' \
  -d '{"all":true}'
```
