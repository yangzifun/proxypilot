> 注：本文件按「方法 × 路径」展开；唯一路径覆盖率以 server/tools/check_coverage.py 为准（106/106）。

# 路由清单（从 worker.js 静态提取）

> 生成方式：解析 `workers/worker.js` 中的 `path === '...'`、`path.indexOf('...')`、`path.match(...)`、`new URLPattern(...)`。
> `method` 为向上查找最近的 `method === 'X'` 守卫，动态路由可能显示为 `ANY`；以最终实现为准。
> 共 133 条（去重后）。`/api/apikeys`、`/api/servers/:id/*` 等正则子路由未全部展开。

## 页面 / 静态资源（10）

| 方法 | 路径 | 原处理函数 | 状态 | 行号 |
|---|---|---|---|---|
| `GET` | `/` | `-` | ➡️ 仍由 Worker 提供 | 14157 |
| `GET` | `/admin` | `-` | ➡️ 仍由 Worker 提供 | 14173 |
| `GET` | `/admin/` | `-` | ➡️ 仍由 Worker 提供 | 14173 |
| `ANY` | `/assets/world.svg` | `-` | ➡️ 仍由 Worker 提供 | 13754 |
| `GET` | `/login` | `-` | ➡️ 仍由 Worker 提供 | 14168 |
| `GET` | `/login/` | `-` | ➡️ 仍由 Worker 提供 | 14168 |
| `GET` | `/me` | `-` | ➡️ 仍由 Worker 提供 | 14157 |
| `GET` | `/me/` | `-` | ➡️ 仍由 Worker 提供 | 14157 |
| `GET` | `/ui` | `-` | ➡️ 仍由 Worker 提供 | 14173 |
| `GET` | `/ui/` | `-` | ➡️ 仍由 Worker 提供 | 14173 |

## 订阅输出（3）

| 方法 | 路径 | 原处理函数 | 状态 | 行号 |
|---|---|---|---|---|
| `ANY` | `/batch-configs/:uuid` | `-` | ✅ 已迁移（动态） | 13784 |
| `GET` | `/clash-config` | `-` | ✅ 已迁移 | 13852 |
| `ANY` | `/sub/:uuid` | `-` | ✅ 已迁移（动态） | 13782 |

## 安装脚本（3）

| 方法 | 路径 | 原处理函数 | 状态 | 行号 |
|---|---|---|---|---|
| `GET` | `/agent/install.sh` | `handleAgentInstallScript` | ⏳ 待迁移 | 13761 |
| `GET` | `/api/agent/install.sh` | `handleAgentInstallScript` | ⏳ 待迁移 | 4072 |
| `GET` | `/install.sh` | `handleAgentInstallScript` | ⏳ 待迁移 | 13761 |

## 用户中心（25）

| 方法 | 路径 | 原处理函数 | 状态 | 行号 |
|---|---|---|---|---|
| `POST` | `/api/portal/email/send-code` | `handlePortalEmailSendCode` | ✅ 已迁移 | 4052 |
| `POST` | `/api/portal/email/verify` | `handlePortalEmailVerify` | ✅ 已迁移 | 4053 |
| `POST` | `/api/portal/login` | `handlePortalLogin` | ✅ 已迁移 | 4050 |
| `GET` | `/api/portal/mtproto` | `handlePortalMtproto` | ⏳ 待迁移 | 4055 |
| `POST` | `/api/portal/send-code` | `handlePortalSendCode` | ✅ 已迁移 | 4049 |
| `GET` | `/api/portal/uuids` | `handlePortalUuids` | ✅ 已迁移 | 4051 |
| `GET` | `/manage` | `-` | ⏳ 待迁移 | 14163 |
| `GET` | `/manage/` | `-` | ⏳ 待迁移 | 14163 |
| `ANY` | `/manage/all-uuid-info` | `-` | ⏳ 待迁移（动态） | 13787 |
| `GET` | `/manage/all-uuid-info` | `handleGetAllUuidInfo` | ⏳ 待迁移 | 13839 |
| `POST` | `/manage/configs` | `portalGate` | ✅ 已迁移 | 13860 |
| `PUT` | `/manage/configs` | `portalGateByRow` | ✅ 已迁移 | 13889 |
| `ANY` | `/manage/configs/:uuid` | `-` | ⏳ 待迁移（动态） | 13780 |
| `DELETE` | `/manage/configs/batch` | `-` | ⏳ 待迁移 | 13912 |
| `ANY` | `/manage/configs/id/:id` | `-` | ⏳ 待迁移（动态） | 13781 |
| `POST` | `/manage/entry` | `-` | ⏳ 待迁移 | 13882 |
| `POST` | `/manage/generate-uuid` | `-` | ⏳ 待迁移 | 13882 |
| `ANY` | `/manage/stats/:uuid` | `-` | ⏳ 待迁移（动态） | 13783 |
| `ANY` | `/manage/uuid-check/:uuid` | `-` | ⏳ 待迁移（动态） | 13786 |
| `POST` | `/manage/uuid-info` | `portalGate` | ⏳ 待迁移 | 13867 |
| `ANY` | `/manage/uuid-info/:uuid` | `-` | ⏳ 待迁移（动态） | 13785 |
| `GET` | `/manage/uuid-sub-sources` | `handleSubSources` | ✅ 已迁移 | 13832 |
| `POST` | `/manage/uuid-sub-sources` | `handleSubSources` | ✅ 已迁移 | 13874 |
| `PUT` | `/manage/uuid-sub-sources` | `handleSubSources` | ✅ 已迁移 | 13896 |
| `DELETE` | `/manage/uuid-sub-sources` | `handleSubSources` | ✅ 已迁移 | 13905 |

## 认证与账号（17）

| 方法 | 路径 | 原处理函数 | 状态 | 行号 |
|---|---|---|---|---|
| `POST` | `/api/admin/users` | `sysGate` | ✅ 已迁移 | 4104 |
| `POST` | `/api/admin/users/email/code` | `handleAdminEmailCode` | ⏳ 待迁移 | 4105 |
| `POST` | `/api/admin/users/email/unbind` | `handleAdminEmailUnbind` | ⏳ 待迁移 | 4107 |
| `POST` | `/api/admin/users/email/verify` | `handleAdminEmailVerify` | ⏳ 待迁移 | 4106 |
| `GET` | `/api/change-password` | `handleChangePassword` | ✅ 已迁移 | 4097 |
| `POST` | `/api/login` | `handleLogin` | ✅ 已迁移 | 4059 |
| `POST` | `/api/login/email-code` | `handleEmailCodeSend` | ⏳ 待迁移 | 4060 |
| `POST` | `/api/login/email-code/mfa` | `handleEmailCodeMfa` | ⏳ 待迁移 | 4062 |
| `POST` | `/api/login/email-code/verify` | `handleEmailCodeVerify` | ⏳ 待迁移 | 4061 |
| `POST` | `/api/mfa/backup-codes/regenerate` | `handleMfaRegenerateBackupCodes` | ✅ 已迁移 | 4102 |
| `POST` | `/api/mfa/disable` | `handleMfaDisable` | ✅ 已迁移 | 4101 |
| `POST` | `/api/mfa/init` | `handleMfaInit` | ✅ 已迁移 | 4098 |
| `POST` | `/api/mfa/login-with-backup` | `handleMfaLoginWithBackup` | ✅ 已迁移 | 4065 |
| `POST` | `/api/mfa/status` | `handleMfaStatus` | ✅ 已迁移 | 4100 |
| `POST` | `/api/mfa/verify-first` | `handleMfaVerifyFirst` | ✅ 已迁移 | 4099 |
| `GET` | `/api/oidc/callback` | `handleOidcCallback` | ⏳ 待迁移 | 4064 |
| `GET` | `/api/oidc/login` | `handleOidcLogin` | ⏳ 待迁移 | 4063 |

## 服务器 / Agent（24）

| 方法 | 路径 | 原处理函数 | 状态 | 行号 |
|---|---|---|---|---|
| `POST` | `/api/agent/ack` | `handleAgentAck` | ✅ 已迁移 | 4071 |
| `GET` | `/api/agent/bootstrap` | `handleAgentBootstrap` | ⏳ 待迁移 | 4074 |
| `GET` | `/api/agent/keycheck` | `handleAgentKeycheck` | ✅ 已迁移 | 4073 |
| `POST` | `/api/agent/latest` | `handleAgentTasksApi` | ✅ 已迁移 | 4115 |
| `GET` | `/api/agent/latest` | `-` | ✅ 已迁移 | 13027 |
| `POST` | `/api/agent/poll` | `handleAgentPoll` | ✅ 已迁移 | 4070 |
| `POST` | `/api/agent/push-update` | `handleAgentTasksApi` | ⏳ 待迁移 | 4115 |
| `POST` | `/api/agent/tasks` | `handleAgentTasksApi` | ⏳ 待迁移 | 4114 |
| `GET` | `/api/agent/tasks` | `-` | ⏳ 待迁移 | 13134 |
| `POST` | `/api/agent/tasks/*` | `handleAgentTasksApi` | ⏳ 待迁移（动态） | 4114 |
| `GET` | `/api/agent/wait` | `handleAgentWait` | ⏳ 待迁移 | 4069 |
| `GET` | `/api/agent/ws` | `handleAgentWs` | ⏳ 待迁移 | 4068 |
| `POST` | `/api/monitor/notify` | `-` | ⏳ 待迁移 | 3510 |
| `POST` | `/api/monitor/notify/test` | `-` | ⏳ 待迁移 | 3539 |
| `ANY` | `/api/monitor/uuid-check` | `-` | ⏳ 待迁移 | 3486 |
| `POST` | `/api/servers` | `handleServersApi` | ✅ 已迁移 | 4120 |
| `GET` | `/api/servers` | `-` | ✅ 已迁移 | 6737 |
| `POST` | `/api/servers/*` | `handleServersApi` | ⏳ 待迁移（动态） | 4120 |
| `GET` | `/api/servers/monitor` | `-` | ⏳ 待迁移 | 6655 |
| `GET` | `/api/servers/services` | `-` | ⏳ 待迁移 | 6655 |
| `GET` | `/api/servers/status` | `-` | ✅ 已迁移 | 6628 |
| `GET` | `/api/shell/*` | `handleShellApi` | ⏳ 待迁移（动态） | 4077 |
| `GET` | `/api/shell/status` | `handleShellApi` | ⏳ 待迁移 | 4077 |
| `ANY` | `/api/shell/status` | `-` | ⏳ 待迁移 | 11948 |

## 节点编排（33）

| 方法 | 路径 | 原处理函数 | 状态 | 行号 |
|---|---|---|---|---|
| `POST` | `/api/mtproto` | `handleMtprotoApi` | ⏳ 待迁移 | 4129 |
| `POST` | `/api/mtproto/*` | `handleMtprotoApi` | ⏳ 待迁移（动态） | 4129 |
| `POST` | `/api/mtproto/apply-all` | `-` | ⏳ 待迁移 | 10511 |
| `GET` | `/api/mtproto/nodes` | `-` | ✅ 已迁移 | 10290 |
| `POST` | `/api/mtproto/nodes` | `-` | ✅ 已迁移 | 10301 |
| `POST` | `/api/mtproto/push` | `-` | ⏳ 待迁移 | 10529 |
| `POST` | `/api/mtproto/reconcile` | `-` | ⏳ 待迁移 | 10581 |
| `GET` | `/api/mtproto/templates` | `-` | ✅ 已迁移 | 10189 |
| `POST` | `/api/mtproto/templates` | `-` | ✅ 已迁移 | 10199 |
| `GET` | `/api/mtproto/uuids` | `-` | ⏳ 待迁移 | 10500 |
| `POST` | `/api/node-configs` | `handleNodeConfigsApi` | ⏳ 待迁移 | 4121 |
| `GET` | `/api/node-configs` | `-` | ⏳ 待迁移 | 10753 |
| `POST` | `/api/node-configs/*` | `handleNodeConfigsApi` | ⏳ 待迁移（动态） | 4121 |
| `GET` | `/api/node-configs/logs` | `-` | ⏳ 待迁移 | 7199 |
| `POST` | `/api/node-configs/preview` | `-` | ⏳ 待迁移 | 7215 |
| `POST` | `/api/node-configs/push` | `-` | ⏳ 待迁移 | 7239 |
| `POST` | `/api/node-templates` | `handleNodeTemplatesApi` | ⏳ 待迁移 | 4123 |
| `GET` | `/api/node-templates` | `-` | ⏳ 待迁移 | 7105 |
| `POST` | `/api/node-templates/*` | `handleNodeTemplatesApi` | ⏳ 待迁移（动态） | 4123 |
| `POST` | `/api/singbox` | `handleSingboxApi` | ⏳ 待迁移 | 4125 |
| `POST` | `/api/singbox/*` | `handleSingboxApi` | ⏳ 待迁移（动态） | 4125 |
| `POST` | `/api/singbox/apply-all` | `-` | ✅ 已迁移 | 8998 |
| `POST` | `/api/singbox/deploy-all` | `-` | ✅ 已迁移 | 9063 |
| `GET` | `/api/singbox/nodes` | `-` | ✅ 已迁移 | 9110 |
| `POST` | `/api/singbox/nodes` | `-` | ✅ 已迁移 | 9116 |
| `POST` | `/api/singbox/preview` | `-` | ✅ 已迁移 | 8968 |
| `POST` | `/api/singbox/push` | `-` | ✅ 已迁移 | 9044 |
| `GET` | `/api/singbox/releases` | `-` | ✅ 已迁移 | 8673 |
| `GET` | `/api/singbox/templates` | `-` | ✅ 已迁移 | 8920 |
| `POST` | `/api/singbox/templates` | `-` | ✅ 已迁移 | 8924 |
| `POST` | `/api/singbox/templates/from-config` | `-` | ✅ 已迁移 | 8706 |
| `POST` | `/api/singbox/templates/preview` | `-` | ✅ 已迁移 | 8891 |
| `GET` | `/api/singbox/uuids` | `-` | ✅ 已迁移 | 8983 |

## 批量任务（3）

| 方法 | 路径 | 原处理函数 | 状态 | 行号 |
|---|---|---|---|---|
| `POST` | `/api/jobs` | `handleJobsApi` | ✅ 已迁移 | 4127 |
| `GET` | `/api/jobs` | `-` | ✅ 已迁移 | 13595 |
| `POST` | `/api/jobs/*` | `handleJobsApi` | ⏳ 待迁移（动态） | 4127 |

## 配置 / 统计 / 对象存储（15）

| 方法 | 路径 | 原处理函数 | 状态 | 行号 |
|---|---|---|---|---|
| `GET` | `/api/bucket/*` | `handleBucketApi` | ⏳ 待迁移（动态） | 4090 |
| `GET` | `/api/bucket/file` | `-` | ⏳ 待迁移 | 3995 |
| `PUT` | `/api/bucket/file` | `-` | ⏳ 待迁移 | 4009 |
| `DELETE` | `/api/bucket/file` | `-` | ⏳ 待迁移 | 4030 |
| `GET` | `/api/bucket/files` | `-` | ⏳ 待迁移 | 3979 |
| `POST` | `/api/logs/cleanup` | `sysGate` | ✅ 已迁移 | 4110 |
| `POST` | `/api/logs/summary` | `sysGate` | ✅ 已迁移 | 4109 |
| `POST` | `/api/stats` | `handleGetStats` | ✅ 已迁移 | 4108 |
| `POST` | `/api/stats/uuid-details` | `handleGetUUIDAccessDetails` | ✅ 已迁移 | 4111 |
| `GET` | `/api/templates` | `-` | ✅ 已迁移 | 13767 |
| `POST` | `/api/templates/manage` | `handleTemplates` | ✅ 已迁移 | 3574 |
| `POST` | `/api/templates/manage/set-default` | `handleTemplateSetDefault` | ⏳ 待迁移 | 3575 |
| `POST` | `/api/uuid-configs` | `handleUuidConfigs` | ✅ 已迁移 | 3571 |
| `POST` | `/api/uuid-configs/stats` | `handleTemplates` | ✅ 已迁移 | 3572 |
| `POST` | `/api/uuids` | `handleUuids` | ✅ 已迁移 | 3561 |

## 已迁移的实现位置

| 路由 | Go 文件 |
|---|---|
| `GET /healthz` | `server/internal/api/router.go` |
| `POST /api/login` | `server/internal/api/login.go` |
| `POST /api/change-password` | `server/internal/api/account.go` |
| `GET /api/mfa/status` | `server/internal/api/account.go` |
| `POST /api/portal/send-code` | `server/internal/api/portal.go` |
| `POST /api/portal/login` | `server/internal/api/portal.go` |
| `GET /api/portal/uuids` | `server/internal/api/portal.go` |
| `POST /api/portal/email/send-code` | `server/internal/api/portal.go` |
| `POST /api/portal/email/verify` | `server/internal/api/portal.go` |
| `GET /sub/:uuid` | `server/internal/api/sub.go` |
| `GET /batch-configs/:uuid` | `server/internal/api/sub.go` |
| `GET/POST/PUT /api/uuids` | `server/internal/api/uuids.go` |
| `GET /api/servers` | `server/internal/api/servers.go` |
| `GET /api/servers/status` | `server/internal/api/servers.go` |
| `POST /api/servers` | `server/internal/api/servers_write.go` |
| `GET/PUT/DELETE /api/servers/{id}` | `server/internal/api/servers_write.go` |
| `POST /api/servers/{id}/agent-token` | `server/internal/api/servers_write.go` |
| `DELETE /api/uuids` | `server/internal/api/uuids_delete.go` |
| `GET /clash-config` | `server/internal/api/clash.go` |
| `GET /api/templates` | `server/internal/api/templates.go` |
| `/api/uuid-configs` CRUD | `server/internal/api/configs.go` |
| `/api/apikeys` CRUD/轮换 | `server/internal/api/apikeys_admin.go` |
| `/api/agent/keycheck` / `latest` | `server/internal/api/agent.go` |
| `/api/singbox/templates` / `nodes` / `uuids` / `preview` | `server/internal/api/singbox_api*.go` |
| sing-box 渲染内核 | `server/internal/api/singbox*.go` |
| `/api/jobs` 批量任务 | `server/internal/api/jobs.go` |
| `/api/mtproto/*` MTProto 模板/节点/客户端 | `server/internal/api/mtproto*.go` |
| `/api/stats/uuid-details` / `/api/uuid-configs/stats` | `server/internal/api/stats_details.go` |
| `/api/agent/poll` / `ack` | `server/internal/api/agent_poll.go` |
| 节点渲染（占位符 / NODES / PROXIES_YAML） | `server/internal/api/node_render.go` |
| `/api/admin/users` CRUD | `server/internal/api/admin_users.go` |
| MFA 写操作 / 备份码登录 | `server/internal/api/mfa.go` |
| `/manage/configs*` / `uuid-sub-sources` | `server/internal/api/portal_configs.go` |
| `/api/templates/manage` CRUD | `server/internal/api/templates_manage.go` |
| `GET /api/stats` | `server/internal/api/stats.go` |
| `/api/logs/summary` / `cleanup` | `server/internal/api/logs.go` |

未匹配的 `/api/*`、`/manage*`、`/sub*`：配置了 `PP_LEGACY_BASE` 时由
`server/internal/api/proxy.go` 原样转发旧 Worker；未配置时由 `stub.go` 返回 501，
便于前端灰度时快速区分“没迁移”和“真的 404”。
