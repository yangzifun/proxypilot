# 新旧后台接口对照（按前缀启发式匹配，机器生成，仅供初筛）

> ⚠️ 本文件由脚本按「前缀/归一化」粗略匹配，存在误判。
> 权威结论与人肉核对结果见 **audit/PARITY.md**。

> 老后台接口 37 个，新版覆盖 30 个，**疑似缺口 7 个**

## 老后台接口覆盖情况

| 老接口 | 新版 |
|---|---|
| `admin/users` | ✅ 有 |
| `admin/users/email/code` | ✅ 有 |
| `admin/users/email/unbind` | ✅ 有 |
| `admin/users/email/verify` | ✅ 有 |
| `agent/push-update` | ❌ **缺** |
| `agent/tasks` | ❌ **缺** |
| `apikeys` | ✅ 有 |
| `bucket/file` | ✅ 有 |
| `bucket/files` | ✅ 有 |
| `change-password` | ✅ 有 |
| `logs/cleanup` | ✅ 有 |
| `logs/summary` | ✅ 有 |
| `mfa/backup-codes/regenerate` | ✅ 有 |
| `mfa/disable` | ✅ 有 |
| `mfa/init` | ✅ 有 |
| `mfa/status` | ✅ 有 |
| `mfa/verify-first` | ✅ 有 |
| `monitor/notify` | ❌ **缺** |
| `monitor/notify/test` | ❌ **缺** |
| `monitor/uuid-check` | ❌ **缺** |
| `node-configs/logs` | ✅ 有 |
| `node-templates` | ❌ **缺** |
| `servers` | ✅ 有 |
| `servers/monitor` | ✅ 有 |
| `singbox/apply-all` | ✅ 有 |
| `singbox/deploy-all` | ✅ 有 |
| `singbox/nodes` | ✅ 有 |
| `singbox/push` | ❌ **缺** |
| `singbox/templates` | ✅ 有 |
| `singbox/templates/from-config` | ✅ 有 |
| `singbox/templates/preview` | ✅ 有 |
| `singbox/uuids` | ✅ 有 |
| `stats` | ✅ 有 |
| `templates/manage` | ✅ 有 |
| `templates/manage/set-default` | ✅ 有 |
| `uuid-configs` | ✅ 有 |
| `uuids` | ✅ 有 |

## 新版多出来的接口

- `admin/users`
- `apikeys`
- `apikeys/:id`
- `apikeys/:id/rotate`
- `bucket/file`
- `bucket/files`
- `change-password`
- `logs/cleanup`
- `logs/summary`
- `mfa/backup-codes/regenerate`
- `mfa/disable`
- `mfa/init`
- `mfa/status`
- `mfa/verify-first`
- `node-configs/logs`
- `servers`
- `servers/:id`
- `servers/:id/agent-key`
- `servers/:id/agent-token`
- `servers/:id/tasks`
- `servers/monitor`
- `singbox/apply-all`
- `singbox/deploy-all`
- `singbox/nodes`
- `singbox/templates`
- `singbox/templates/:id`
- `singbox/uuids`
- `stats`
- `templates/manage`
- `templates/manage/set-default`
- `uuid-configs`
- `uuids`
