# 数据字典

> 数据库：MySQL 8，库名 `proxypilot`，共 **61 张表**（不含视图）。
> 所有时间字段均为 **毫秒时间戳**（`bigint`），布尔字段用 `bigint/tinyint` 0/1 表示。
> 迁移脚本在 `server/migrations/`，启动时自动执行，已执行记录在 `schema_migrations`。

## 迁移机制

```
server/migrations/NNNN_name.sql  →  启动时按文件名顺序执行
                              →  成功后写入 schema_migrations
```

- 只增不改：已发布的迁移文件不再编辑
- 新表用 `CREATE TABLE IF NOT EXISTS`，加列用 `ALTER TABLE ... ADD COLUMN`
- 每次改动同步更新本文件

## 账号与安全

### `admin_users`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `username` | varchar(191) | 否 | UNI |
| `password_hash` | varchar(255) | 否 |  |
| `mfa_enabled` | bigint | 是 |  |
| `mfa_secret` | varchar(255) | 是 |  |
| `last_mfa_login` | bigint | 是 |  |
| `last_backup_login` | bigint | 是 |  |
| `email` | varchar(320) | 是 |  |
| `email_verified` | bigint | 是 |  |
| `email_verified_at` | bigint | 是 |  |
| `created_at` | bigint | 是 |  |
| `email_norm` | varchar(320) | 是 | UNI |

### `admin_oidc_identities`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `username` | varchar(191) | 否 | MUL |
| `issuer` | varchar(255) | 否 | MUL |
| `sub` | varchar(191) | 否 |  |
| `email` | varchar(320) | 是 |  |
| `created_at` | bigint | 是 |  |
| `last_login_at` | bigint | 是 |  |

### `mfa_backup_codes`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `username` | varchar(191) | 否 | MUL |
| `code` | longtext | 否 |  |
| `used` | bigint | 是 | MUL |
| `created_at` | bigint | 是 |  |
| `used_at` | bigint | 是 |  |

### `api_keys`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `key_name` | longtext | 否 |  |
| `api_key` | varchar(255) | 否 | UNI |
| `secret_key` | longtext | 是 |  |
| `enabled` | bigint | 否 | MUL |
| `permissions` | varchar(191) | 是 |  |
| `key_hint` | longtext | 是 |  |
| `expires_at` | bigint | 是 |  |
| `created_by` | longtext | 是 |  |
| `created_at` | bigint | 是 | MUL |
| `updated_at` | bigint | 是 |  |
| `last_used_at` | bigint | 是 |  |
| `usage_count` | bigint | 是 |  |

### `client_auth_codes`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `code` | varchar(96) | 否 | PRI |
| `status` | varchar(16) | 否 |  |
| `token` | text | 否 |  |
| `email` | varchar(191) | 否 |  |
| `client_name` | varchar(128) | 否 |  |
| `created_at` | bigint | 否 |  |
| `expires_at` | bigint | 否 | MUL |
| `approved_at` | bigint | 否 |  |

## 服务器与 Agent

### `servers`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `name` | varchar(191) | 否 | UNI |
| `host` | varchar(255) | 否 |  |
| `port` | bigint | 是 |  |
| `username` | varchar(191) | 否 |  |
| `auth_type` | varchar(32) | 是 |  |
| `secret_enc` | varchar(255) | 是 |  |
| `secret_hint` | longtext | 是 |  |
| `host_key_fingerprint` | longtext | 是 |  |
| `tags` | longtext | 是 |  |
| `remark` | longtext | 是 |  |
| `enabled` | bigint | 是 | MUL |
| `last_status` | varchar(191) | 是 |  |
| `last_latency_ms` | bigint | 是 |  |
| `last_error` | longtext | 是 |  |
| `last_check_at` | bigint | 是 |  |
| `last_metrics` | longtext | 是 |  |
| `os_info` | longtext | 是 |  |
| `singbox_template_id` | bigint | 是 |  |
| `singbox_domain` | longtext | 是 |  |
| `singbox_warp` | bigint | 是 |  |
| `singbox_auto_deploy` | bigint | 是 |  |
| `created_at` | bigint | 是 |  |
| `updated_at` | bigint | 是 |  |
| `agent_token_hash` | varchar(255) | 是 |  |
| `agent_token_hint` | longtext | 是 |  |
| `agent_token_enc` | varchar(255) | 是 |  |
| `agent_version` | longtext | 是 |  |
| `agent_last_seen` | bigint | 是 |  |
| `agent_last_poll_at` | bigint | 是 |  |
| `agent_next_poll_sec` | bigint | 是 |  |
| `agent_poll_count` | bigint | 是 |  |
| `metrics_every` | bigint | 是 |  |
| `allow_exec` | bigint | 是 |  |
| `last_ip` | varchar(45) | 是 |  |
| `last_ip4` | varchar(45) | 是 |  |
| `last_ip6` | varchar(45) | 是 |  |
| `last_ip_private` | varchar(64) | 否 |  |
| `last_mesh_ip4` | varchar(64) | 否 |  |
| `last_mesh_ip6` | varchar(64) | 否 |  |
| `skip_hostkey_verify` | bigint | 是 |  |
| `agent_key_created_at` | bigint | 是 |  |
| `agent_key_used_at` | bigint | 是 |  |
| `agent_platform` | longtext | 是 |  |
| `last_notify_at` | bigint | 是 |  |
| `ip_country` | longtext | 是 |  |
| `ip_region` | longtext | 是 |  |
| `ip_lat` | double | 是 |  |
| `ip_lon` | double | 是 |  |
| `link_mode` | varchar(32) | 是 |  |
| `traffic_state` | enum('normal','warning','exceeded') | 否 |  |
| `ssh_guard` | tinyint | 否 |  |

### `agent_tasks`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `server_id` | bigint | 否 | MUL |
| `config_id` | bigint | 是 |  |
| `type` | varchar(32) | 是 |  |
| `payload` | longtext | 是 |  |
| `payload_hash` | longtext | 是 |  |
| `status` | varchar(32) | 是 |  |
| `attempts` | bigint | 是 |  |
| `exit_code` | bigint | 是 |  |
| `output` | longtext | 是 |  |
| `actor` | varchar(191) | 是 |  |
| `created_at` | bigint | 是 | MUL |
| `sent_at` | bigint | 是 |  |
| `finished_at` | bigint | 是 |  |

### `server_metrics`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `server_id` | bigint | 否 | MUL |
| `status` | varchar(32) | 是 |  |
| `latency_ms` | bigint | 是 |  |
| `load1` | double | 是 |  |
| `load5` | double | 是 |  |
| `load15` | double | 是 |  |
| `cpu_percent` | double | 是 |  |
| `mem_percent` | double | 是 |  |
| `mem_used_mb` | bigint | 是 |  |
| `mem_total_mb` | bigint | 是 |  |
| `disk_percent` | double | 是 |  |
| `uptime_sec` | bigint | 是 |  |
| `services` | longtext | 是 |  |
| `raw` | longtext | 是 |  |
| `created_at` | bigint | 是 |  |
| `net_rx_bytes` | bigint | 是 |  |
| `net_tx_bytes` | bigint | 是 |  |
| `net_rx_rate` | bigint | 是 |  |
| `net_tx_rate` | bigint | 是 |  |
| `disk_read_bytes` | bigint | 是 |  |
| `disk_write_bytes` | bigint | 是 |  |
| `disk_read_rate` | bigint | 是 |  |
| `disk_write_rate` | bigint | 是 |  |
| `conn_total` | bigint | 是 |  |
| `conn_established` | bigint | 是 |  |
| `conn_timewait` | bigint | 是 |  |
| `proc_total` | bigint | 是 |  |

### `server_traffic_limits`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `server_id` | bigint | 否 | PRI |
| `enabled` | tinyint | 否 |  |
| `mode` | enum('rx','tx','both') | 否 |  |
| `limit_bytes` | bigint | 否 |  |
| `reset_day` | tinyint | 否 |  |
| `reset_hour` | tinyint | 否 |  |
| `action` | enum('notify','suspend') | 否 |  |
| `warn_percent` | tinyint | 否 |  |
| `updated_at` | bigint | 否 |  |

### `server_traffic_usage`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `server_id` | bigint | 否 | PRI |
| `period_start` | bigint | 否 | PRI |
| `rx_bytes` | bigint | 否 |  |
| `tx_bytes` | bigint | 否 |  |
| `last_rx_total` | bigint | 否 |  |
| `last_tx_total` | bigint | 否 |  |
| `last_sample_at` | bigint | 否 |  |
| `exceeded_at` | bigint | 否 |  |

### `server_unlock_checks`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `server_id` | bigint unsigned | 否 | PRI |
| `results` | longtext | 否 |  |
| `checked_at` | bigint | 否 |  |
| `task_id` | bigint | 否 |  |
| `updated_at` | bigint | 否 |  |

### `shell_sessions`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `server_id` | bigint | 否 | PRI |
| `session` | longtext | 否 |  |
| `created_at` | bigint | 否 |  |
| `status` | varchar(32) | 是 |  |

### `local_server`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | tinyint unsigned | 否 | PRI |
| `name` | varchar(191) | 否 |  |
| `host` | varchar(255) | 否 |  |
| `status` | varchar(32) | 否 |  |
| `ssh_guard` | tinyint | 否 |  |
| `os_info` | text | 是 |  |
| `metrics` | longtext | 是 |  |
| `ip` | varchar(64) | 否 |  |
| `ip4` | varchar(64) | 否 |  |
| `ip6` | varchar(191) | 否 |  |
| `checked_at` | bigint | 否 |  |
| `updated_at` | bigint | 否 |  |

### `local_port_rules`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint | 否 | PRI |
| `port` | int | 否 | MUL |
| `port_end` | int | 否 |  |
| `protocol` | varchar(8) | 否 |  |
| `action` | varchar(8) | 否 |  |
| `note` | varchar(128) | 否 |  |
| `src_cidr` | varchar(64) | 否 |  |
| `created_at` | bigint | 否 |  |

## 配置与模板

### `configs`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `uuid` | varchar(36) | 否 | MUL |
| `config_data` | mediumtext | 否 |  |
| `protocol` | varchar(32) | 否 | MUL |
| `remark` | longtext | 是 |  |
| `domain_hosting` | varchar(191) | 否 | MUL |
| `created_at` | bigint | 否 |  |
| `updated_at` | bigint | 否 |  |
| `ip` | varchar(45) | 是 |  |
| `domain` | varchar(255) | 是 |  |
| `port` | bigint | 是 |  |
| `meta` | longtext | 是 |  |
| `singbox_node_id` | bigint | 是 | MUL |

### `config_templates`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `name` | varchar(191) | 否 | UNI |
| `url` | varchar(255) | 否 |  |
| `description` | longtext | 是 |  |
| `is_default` | bigint | 是 | MUL |
| `created_at` | bigint | 是 | MUL |
| `updated_at` | bigint | 是 |  |

### `node_configs`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `name` | varchar(191) | 否 | UNI |
| `server_id` | bigint | 否 | MUL |
| `engine` | varchar(32) | 是 |  |
| `target_path` | varchar(255) | 否 |  |
| `service_name` | varchar(255) | 是 |  |
| `restart_cmd` | longtext | 是 |  |
| `check_cmd` | longtext | 是 |  |
| `content` | longtext | 是 |  |
| `backup` | bigint | 是 |  |
| `enabled` | bigint | 是 |  |
| `auto_push` | bigint | 是 |  |
| `push_interval_min` | bigint | 是 |  |
| `last_push_at` | bigint | 是 |  |
| `last_push_status` | longtext | 是 |  |
| `last_push_message` | longtext | 是 |  |
| `created_at` | bigint | 是 |  |
| `updated_at` | bigint | 是 |  |
| `version` | bigint | 是 |  |
| `content_hash` | longtext | 是 |  |
| `applied_version` | bigint | 是 |  |
| `applied_at` | bigint | 是 |  |
| `template_id` | bigint | 是 |  |
| `uuid` | varchar(36) | 是 |  |
| `install_versions` | longtext | 是 |  |

### `node_templates`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `name` | varchar(191) | 否 | UNI |
| `engine` | varchar(32) | 是 |  |
| `target_path` | varchar(255) | 否 |  |
| `service_name` | varchar(255) | 是 |  |
| `restart_cmd` | longtext | 是 |  |
| `check_cmd` | longtext | 是 |  |
| `content` | longtext | 是 |  |
| `backup` | bigint | 是 |  |
| `remark` | longtext | 是 |  |
| `enabled` | bigint | 是 |  |
| `created_at` | bigint | 是 |  |
| `updated_at` | bigint | 是 |  |

### `mihomo_templates`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `name` | varchar(191) | 否 | UNI |
| `remark` | longtext | 是 |  |
| `content` | longtext | 是 |  |
| `created_at` | bigint | 是 |  |
| `updated_at` | bigint | 是 |  |
| `enabled` | bigint | 是 |  |
| `protocols` | longtext | 是 |  |
| `mode` | varchar(16) | 是 |  |
| `server_json` | longtext | 是 |  |

### `mihomo_template_versions`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `template_id` | bigint unsigned | 否 | MUL |
| `name` | varchar(191) | 否 |  |
| `remark` | longtext | 是 |  |
| `content` | longtext | 是 |  |
| `actor` | varchar(191) | 是 |  |
| `created_at` | bigint | 是 |  |

### `mtproto_templates`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `name` | varchar(191) | 否 | UNI |
| `remark` | longtext | 是 |  |
| `domain` | varchar(255) | 是 |  |
| `port` | bigint | 是 |  |
| `api_port` | bigint | 是 |  |
| `secret_mode` | varchar(32) | 是 |  |
| `concurrency` | bigint | 是 |  |
| `prefer_ip` | varchar(32) | 是 |  |
| `public_ipv4` | longtext | 是 |  |
| `blocklist` | bigint | 是 |  |
| `throttle_max` | bigint | 是 |  |
| `config_path` | varchar(255) | 是 |  |
| `service_name` | varchar(255) | 是 |  |
| `enabled` | bigint | 是 |  |
| `created_at` | bigint | 是 |  |
| `updated_at` | bigint | 是 |  |

### `uuid_port_map`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `server_id` | bigint | 否 | PRI |
| `uuid` | varchar(36) | 否 | PRI |
| `port` | int | 否 |  |
| `protocol` | varchar(32) | 否 | PRI |
| `created_at` | bigint | 否 |  |
| `updated_at` | bigint | 否 |  |

## 节点

### `singbox_nodes`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `server_id` | bigint | 否 | UNI |
| `template_id` | bigint | 否 |  |
| `name` | varchar(191) | 否 | UNI |
| `domain` | varchar(255) | 是 |  |
| `port_base` | bigint | 是 |  |
| `warp_base` | bigint | 是 |  |
| `sb_path` | varchar(255) | 是 |  |
| `caddy_path` | varchar(255) | 是 |  |
| `xray_path` | varchar(255) | 是 |  |
| `service_sb` | varchar(255) | 是 |  |
| `service_caddy` | varchar(255) | 是 |  |
| `service_xray` | varchar(255) | 是 |  |
| `uuid_list` | longtext | 是 |  |
| `warp_enabled` | bigint | 是 |  |
| `enabled` | bigint | 是 |  |
| `last_apply_at` | bigint | 是 |  |
| `last_render_hash` | longtext | 是 |  |
| `remark` | longtext | 是 |  |
| `created_at` | bigint | 是 |  |
| `updated_at` | bigint | 是 |  |
| `last_link_hash` | longtext | 是 |  |
| `cdn_domain` | longtext | 是 |  |
| `stats_enabled` | bigint | 是 |  |
| `stats_secret_enc` | varchar(255) | 是 |  |

### `mihomo_nodes`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `name` | varchar(191) | 否 | UNI |
| `server_id` | bigint | 否 | UNI |
| `template_id` | bigint | 否 |  |
| `uuid_list` | longtext | 是 |  |
| `enabled` | bigint | 是 |  |
| `remark` | longtext | 是 |  |
| `created_at` | bigint | 是 |  |
| `updated_at` | bigint | 是 |  |
| `protocols` | longtext | 是 |  |
| `last_preview_at` | bigint | 是 |  |
| `advanced_json` | longtext | 是 |  |
| `port_base` | bigint | 是 |  |
| `domain` | varchar(255) | 是 |  |
| `config_path` | varchar(255) | 是 |  |
| `service` | varchar(64) | 是 |  |
| `last_apply_at` | bigint | 是 |  |
| `last_render_hash` | varchar(64) | 是 |  |
| `report_token` | varchar(64) | 是 |  |

### `mtproto_nodes`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `server_id` | bigint | 否 | UNI |
| `template_id` | bigint | 是 | MUL |
| `name` | varchar(191) | 否 | UNI |
| `domain` | varchar(255) | 是 |  |
| `port` | bigint | 是 |  |
| `api_port` | bigint | 是 |  |
| `secret_mode` | varchar(32) | 是 |  |
| `engine` | varchar(32) | 是 |  |
| `config_path` | varchar(255) | 是 |  |
| `service_name` | varchar(255) | 是 |  |
| `concurrency` | bigint | 是 |  |
| `prefer_ip` | varchar(32) | 是 |  |
| `public_ipv4` | longtext | 是 |  |
| `blocklist` | bigint | 是 |  |
| `throttle_max` | bigint | 是 |  |
| `bootstrap_secret` | varchar(255) | 是 |  |
| `enabled` | bigint | 是 |  |
| `last_apply_at` | bigint | 是 |  |
| `last_render_hash` | longtext | 是 |  |
| `remark` | longtext | 是 |  |
| `created_at` | bigint | 是 |  |
| `updated_at` | bigint | 是 |  |
| `host` | varchar(255) | 是 |  |

### `mtproto_clients`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `node_id` | bigint | 否 | MUL |
| `uuid` | varchar(36) | 是 | MUL |
| `email` | varchar(320) | 是 |  |
| `label` | varchar(191) | 是 |  |
| `secret` | varchar(255) | 是 |  |
| `status` | varchar(32) | 是 |  |
| `delivered_at` | bigint | 是 |  |
| `deliver_count` | bigint | 是 |  |
| `last_mail_at` | bigint | 是 |  |
| `mail_count_today` | bigint | 是 |  |
| `mail_day` | varchar(191) | 是 |  |
| `revoked_at` | bigint | 是 |  |
| `remark` | longtext | 是 |  |
| `created_at` | bigint | 是 |  |
| `updated_at` | bigint | 是 |  |

### `node_push_logs`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `config_id` | bigint | 是 |  |
| `server_id` | bigint | 是 | MUL |
| `action` | varchar(32) | 是 |  |
| `status` | varchar(32) | 是 |  |
| `exit_code` | bigint | 是 |  |
| `message` | longtext | 是 |  |
| `output` | longtext | 是 |  |
| `duration_ms` | bigint | 是 |  |
| `actor` | varchar(191) | 是 |  |
| `created_at` | bigint | 是 | MUL |

### `node_stats_snapshot`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `node_id` | bigint | 否 | PRI |
| `sampled_at` | bigint | 是 |  |
| `conn_count` | bigint | 是 |  |
| `up_total` | bigint | 是 |  |
| `down_total` | bigint | 是 |  |
| `up_rate` | bigint | 是 |  |
| `down_rate` | bigint | 是 |  |
| `err_text` | longtext | 是 |  |

### `node_connections`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `node_id` | bigint | 否 | PRI |
| `conn_id` | varchar(64) | 否 | PRI |
| `source_ip` | varchar(45) | 是 |  |
| `source_port` | bigint | 是 |  |
| `host` | varchar(255) | 是 |  |
| `network` | varchar(16) | 是 |  |
| `inbound` | varchar(64) | 是 |  |
| `upload` | bigint | 是 |  |
| `download` | bigint | 是 |  |
| `started_at` | bigint | 是 |  |
| `seen_at` | bigint | 是 | MUL |

### `node_user_counter`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `node_id` | bigint | 否 | PRI |
| `uuid` | varchar(36) | 否 | PRI |
| `up_total` | bigint | 是 |  |
| `down_total` | bigint | 是 |  |
| `sampled_at` | bigint | 是 |  |

### `user_node_recent`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `uuid` | varchar(36) | 否 | PRI |
| `node_id` | bigint | 否 | PRI |
| `last_ts` | bigint | 否 | MUL |
| `up` | bigint | 否 |  |
| `down` | bigint | 否 |  |

## 流量与访问

### `user_traffic`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `uuid` | varchar(36) | 否 | PRI |
| `up_total` | bigint | 是 |  |
| `down_total` | bigint | 是 |  |
| `last_active_at` | bigint | 是 |  |
| `updated_at` | bigint | 是 |  |

### `user_traffic_hourly`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `uuid` | varchar(36) | 否 | PRI |
| `node_id` | bigint | 否 | PRI |
| `hour_ts` | bigint | 否 | PRI |
| `up` | bigint | 是 |  |
| `down` | bigint | 是 |  |

### `mihomo_traffic`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `uuid` | varchar(36) | 否 | PRI |
| `up_total` | bigint | 是 |  |
| `down_total` | bigint | 是 |  |
| `last_active_at` | bigint | 是 |  |
| `updated_at` | bigint | 是 |  |

### `mihomo_traffic_hourly`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `uuid` | varchar(36) | 否 | PRI |
| `node_id` | bigint | 否 | PRI |
| `hour_ts` | bigint | 否 | PRI |
| `up` | bigint | 是 |  |
| `down` | bigint | 是 |  |

### `config_access_logs`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `uuid` | varchar(36) | 否 | MUL |
| `query_type` | varchar(32) | 否 | MUL |
| `client_ip` | varchar(191) | 是 | MUL |
| `user_agent` | longtext | 是 |  |
| `created_at` | varchar(191) | 是 | MUL |

### `ip_geo`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `ip` | varchar(45) | 否 | PRI |
| `country` | longtext | 是 |  |
| `region` | longtext | 是 |  |
| `lat` | double | 是 |  |
| `lon` | double | 是 |  |
| `updated_at` | bigint | 是 |  |

### `cdn_preferred_addresses`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `group_name` | varchar(64) | 否 | MUL |
| `address` | varchar(255) | 否 |  |
| `port` | int | 否 |  |
| `remark` | varchar(191) | 是 |  |
| `enabled` | tinyint | 否 |  |
| `sort` | int | 否 |  |
| `last_check_at` | bigint | 是 |  |
| `last_check_ok` | tinyint | 是 |  |
| `last_latency_ms` | int | 是 |  |
| `fail_count` | int | 否 |  |
| `created_at` | bigint | 是 |  |
| `updated_at` | bigint | 是 |  |

## VPN

### `vpn_nodes`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `server_id` | bigint unsigned | 否 | UNI |
| `name` | varchar(128) | 否 |  |
| `domain` | varchar(255) | 否 |  |
| `region` | varchar(64) | 否 |  |
| `endpoint` | varchar(255) | 否 |  |
| `mesh_ip4` | varchar(64) | 否 | MUL |
| `client_cidr4` | varchar(64) | 否 |  |
| `node_pubkey` | varchar(128) | 否 |  |
| `state` | varchar(16) | 否 | MUL |
| `listen_port` | int | 否 |  |
| `capacity` | int | 否 |  |
| `cert_mode` | varchar(16) | 否 |  |
| `version` | varchar(64) | 否 |  |
| `last_seen` | bigint | 否 |  |
| `created_at` | bigint | 否 |  |
| `updated_at` | bigint | 否 |  |

### `vpn_node_grants`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `node_id` | bigint unsigned | 否 | MUL |
| `subject_type` | varchar(16) | 否 |  |
| `subject_id` | varchar(64) | 否 | MUL |
| `enabled` | tinyint | 否 |  |
| `expires_at` | bigint | 否 |  |
| `max_devices` | int | 否 |  |
| `quota_bytes` | bigint | 否 |  |
| `used_bytes` | bigint | 否 |  |
| `created_by` | varchar(64) | 否 |  |
| `created_at` | bigint | 否 |  |
| `revoked_at` | bigint | 否 |  |

### `vpn_node_links`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `node_id` | bigint unsigned | 否 | UNI |
| `transport` | varchar(16) | 否 |  |
| `state` | varchar(16) | 否 |  |
| `rtt_ms` | int | 否 |  |
| `last_seen` | bigint | 否 |  |

### `vpn_devices`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `uuid` | varchar(64) | 否 | MUL |
| `name` | varchar(128) | 否 |  |
| `platform` | varchar(32) | 否 |  |
| `device_pubkey` | varchar(128) | 否 |  |
| `ip4` | varchar(64) | 否 | UNI |
| `ip6` | varchar(128) | 否 |  |
| `state` | varchar(16) | 否 |  |
| `last_seen` | bigint | 否 |  |
| `created_at` | bigint | 否 |  |
| `revoked_at` | bigint | 否 |  |

### `vpn_device_codes`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `device_code` | varchar(64) | 否 | PRI |
| `user_code` | varchar(16) | 否 | UNI |
| `uuid` | varchar(64) | 否 |  |
| `approved` | tinyint | 否 |  |
| `expires_at` | bigint | 否 | MUL |
| `created_at` | bigint | 否 |  |

### `vpn_sessions`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `device_id` | bigint unsigned | 否 | MUL |
| `node_id` | bigint unsigned | 否 | MUL |
| `grant_id` | bigint unsigned | 否 |  |
| `ip4` | varchar(64) | 否 |  |
| `ip6` | varchar(128) | 否 |  |
| `client_ip` | varchar(64) | 否 |  |
| `started_at` | bigint | 否 |  |
| `ended_at` | bigint | 否 |  |
| `bytes_tx` | bigint | 否 |  |
| `bytes_rx` | bigint | 否 |  |
| `packets` | bigint | 否 |  |
| `close_reason` | varchar(32) | 否 |  |

### `vpn_settings`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | tinyint unsigned | 否 | PRI |
| `enabled` | tinyint | 否 |  |
| `management_transport` | varchar(16) | 否 |  |
| `masque_fallback` | tinyint | 否 |  |
| `server_mesh_ip4` | varchar(64) | 否 |  |
| `server_mesh_domain` | varchar(255) | 否 |  |
| `mesh_cidr4` | varchar(64) | 否 |  |
| `client_cidr4` | varchar(64) | 否 |  |
| `mtu` | int | 否 |  |
| `token_ttl_seconds` | int | 否 |  |
| `updated_at` | bigint | 否 |  |

### `vpn_signing_keys`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `kid` | varchar(32) | 否 | PRI |
| `public_key` | varchar(128) | 否 |  |
| `private_key_enc` | text | 否 |  |
| `active` | tinyint | 否 | MUL |
| `created_at` | bigint | 否 |  |

### `vpn_user_access`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `uuid` | varchar(64) | 否 | PRI |
| `enabled` | tinyint | 否 |  |
| `expires_at` | bigint | 否 |  |
| `max_devices` | int | 否 |  |
| `quota_bytes` | bigint | 否 |  |
| `used_bytes` | bigint | 否 |  |
| `note` | varchar(255) | 否 |  |
| `created_by` | varchar(64) | 否 |  |
| `created_at` | bigint | 否 |  |
| `updated_at` | bigint | 否 |  |

## 证书

### `vpn_ca`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | tinyint unsigned | 否 | PRI |
| `cert_pem` | text | 否 |  |
| `key_enc` | text | 否 |  |
| `created_at` | bigint | 否 |  |

### `vpn_certificates`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `domains` | varchar(512) | 否 |  |
| `source` | varchar(32) | 否 |  |
| `provider` | varchar(32) | 否 |  |
| `cert_pem` | longtext | 否 |  |
| `key_pem` | longtext | 否 |  |
| `chain_pem` | longtext | 否 |  |
| `fingerprint` | varchar(128) | 否 |  |
| `not_before` | bigint | 否 |  |
| `not_after` | bigint | 否 |  |
| `status` | varchar(32) | 否 |  |
| `created_at` | bigint | 否 |  |
| `updated_at` | bigint | 否 |  |

### `vpn_certs`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `node_id` | bigint unsigned | 否 | MUL |
| `kind` | varchar(16) | 否 |  |
| `mode` | varchar(16) | 否 |  |
| `serial` | varchar(128) | 否 |  |
| `fingerprint_sha256` | varchar(128) | 否 |  |
| `san` | text | 是 |  |
| `not_before` | bigint | 否 |  |
| `not_after` | bigint | 否 |  |
| `cert_pem` | mediumtext | 是 |  |
| `chain_pem` | mediumtext | 是 |  |
| `status` | varchar(16) | 否 | MUL |
| `issued_at` | bigint | 否 |  |
| `renewed_at` | bigint | 否 |  |

## 组网

### `vpn_mesh_nodes`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `server_id` | bigint unsigned | 否 | PRI |
| `mesh_ip4` | varchar(64) | 否 |  |
| `domain` | varchar(255) | 否 |  |
| `state` | varchar(16) | 否 |  |
| `transport` | varchar(16) | 否 |  |
| `rtt_ms` | int | 否 |  |
| `last_seen` | bigint | 否 |  |
| `updated_at` | bigint | 否 |  |

## WARP

### `masque_warp_profiles`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `uuid` | varchar(36) | 否 | UNI |
| `device_id` | varchar(128) | 否 |  |
| `token_enc` | text | 否 |  |
| `private_key_enc` | text | 否 |  |
| `public_key` | varchar(64) | 否 |  |
| `peer_public_key` | varchar(64) | 否 |  |
| `client_ipv4` | varchar(64) | 否 |  |
| `client_ipv6` | varchar(128) | 否 |  |
| `status` | varchar(16) | 否 | MUL |
| `last_error` | varchar(500) | 否 |  |
| `authorized_by` | varchar(64) | 否 |  |
| `created_at` | bigint | 否 |  |
| `last_refresh_at` | bigint | 否 |  |
| `revoked_at` | bigint | 否 |  |

### `masque_warp_settings`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | tinyint unsigned | 否 | PRI |
| `enabled` | tinyint | 否 |  |
| `sni` | varchar(255) | 否 |  |
| `server_ips` | longtext | 否 |  |
| `ports` | longtext | 否 |  |
| `mtu` | int | 否 |  |
| `dns` | longtext | 否 |  |
| `updated_at` | bigint | 否 |  |

## 系统与其他

### `app_settings`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `key` | varchar(191) | 否 | PRI |
| `value` | longtext | 是 |  |
| `updated_at` | bigint | 是 |  |

### `batch_jobs`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `kind` | varchar(32) | 否 |  |
| `title` | varchar(191) | 是 |  |
| `params` | longtext | 是 |  |
| `status` | varchar(32) | 是 | MUL |
| `total` | bigint | 是 |  |
| `done` | bigint | 是 |  |
| `failed` | bigint | 是 |  |
| `cursor` | bigint | 是 |  |
| `runner_id` | longtext | 是 |  |
| `locked_at` | bigint | 是 |  |
| `actor` | varchar(191) | 是 |  |
| `error` | longtext | 是 |  |
| `summary` | longtext | 是 |  |
| `created_at` | bigint | 是 | MUL |
| `started_at` | bigint | 是 |  |
| `finished_at` | bigint | 是 |  |
| `updated_at` | bigint | 是 |  |
| `dedup_key` | varchar(191) | 是 | UNI |

### `batch_job_items`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `job_id` | bigint | 否 | MUL |
| `seq` | bigint | 否 |  |
| `target` | longtext | 是 |  |
| `target_id` | bigint | 是 |  |
| `label` | varchar(191) | 是 |  |
| `status` | varchar(32) | 是 |  |
| `message` | longtext | 是 |  |
| `detail` | longtext | 是 |  |
| `attempts` | bigint | 是 |  |
| `started_at` | bigint | 是 |  |
| `finished_at` | bigint | 是 |  |

### `schema_migrations`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `version` | varchar(191) | 否 | PRI |
| `applied_at` | bigint | 否 |  |

### `singbox_templates`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `name` | varchar(191) | 否 | UNI |
| `remark` | longtext | 是 |  |
| `users_json` | longtext | 是 |  |
| `ss_method` | varchar(191) | 是 |  |
| `ss_server_key` | varchar(255) | 是 |  |
| `reality_private_key` | varchar(255) | 是 |  |
| `reality_public_key` | varchar(255) | 是 |  |
| `reality_short_id` | varchar(255) | 是 |  |
| `reality_dest` | varchar(191) | 是 |  |
| `reality_sni` | varchar(191) | 是 |  |
| `ws_sni` | varchar(191) | 是 |  |
| `disguise_url` | varchar(191) | 是 |  |
| `cert_path` | varchar(255) | 是 |  |
| `key_path` | varchar(255) | 是 |  |
| `xhttp_path` | varchar(191) | 是 |  |
| `port_base` | bigint | 是 |  |
| `warp_base` | bigint | 是 |  |
| `enable_ss` | bigint | 是 |  |
| `enable_anytls` | bigint | 是 |  |
| `enable_hy2` | bigint | 是 |  |
| `enable_reality` | bigint | 是 |  |
| `enable_vlws` | bigint | 是 |  |
| `enable_vmess` | bigint | 是 |  |
| `enable_xhttp` | bigint | 是 |  |
| `warp_enabled` | bigint | 是 |  |
| `warp_private_key` | varchar(255) | 是 |  |
| `warp_address` | longtext | 是 |  |
| `warp_peer_public_key` | varchar(255) | 是 |  |
| `enabled` | bigint | 是 |  |
| `created_at` | bigint | 是 |  |
| `updated_at` | bigint | 是 |  |
| `enable_singbox` | bigint | 是 |  |
| `enable_xray` | bigint | 是 |  |
| `warp_engines` | varchar(191) | 是 |  |
| `enable_masque` | bigint | 是 |  |
| `masque_json` | longtext | 是 |  |
| `masque_path` | varchar(191) | 是 |  |
| `service_masque` | varchar(191) | 是 |  |
| `protocol_options` | longtext | 是 |  |
| `singbox_version` | varchar(64) | 是 |  |
| `xray_version` | varchar(64) | 是 |  |

### `uuid_info`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `uuid` | varchar(36) | 否 | UNI |
| `owner` | varchar(191) | 否 | MUL |
| `email` | varchar(320) | 否 | MUL |
| `contact_info` | longtext | 是 |  |
| `status` | varchar(32) | 是 | MUL |
| `remark` | longtext | 是 |  |
| `last_accessed` | bigint | 是 | MUL |
| `access_count` | bigint | 是 |  |
| `created_at` | bigint | 是 | MUL |
| `updated_at` | bigint | 是 |  |
| `email_norm` | varchar(320) | 是 | UNI |

### `uuid_sub_sources`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `uuid` | varchar(36) | 否 | MUL |
| `url` | varchar(255) | 否 |  |
| `remark` | longtext | 是 |  |
| `enabled` | bigint | 是 | MUL |
| `created_at` | bigint | 是 |  |
| `updated_at` | bigint | 是 |  |

### `vpn_audit`

| 字段 | 类型 | 可空 | 键 |
|---|---|---|---|
| `id` | bigint unsigned | 否 | PRI |
| `actor` | varchar(64) | 否 |  |
| `action` | varchar(64) | 否 |  |
| `target` | varchar(128) | 否 |  |
| `detail` | text | 是 |  |
| `created_at` | bigint | 否 | MUL |

## 关系速查

```
servers 1 ──< agent_tasks            任务队列（server_id）
servers 1 ──< server_metrics         指标采样（server_id）
servers 1 ──1 vpn_nodes              一个服务器一个 VPN 节点（server_id）
vpn_nodes 1 ──< vpn_node_grants      按 UUID 授权（node_id + subject_id）
vpn_nodes 1 ──< vpn_sessions         连接会话（node_id）
vpn_devices 1 ──< vpn_sessions       设备会话（device_id）
configs   N ──1 singbox_nodes        节点配置（singbox_node_id）
configs   1 ──< config_access_logs   访问日志（按 uuid 聚合）
```

## 常用查询

```sql
-- 在线节点
SELECT id, name, agent_version, FROM_UNIXTIME(last_check_at/1000) AS last
FROM servers WHERE enabled=1 AND last_status='online' ORDER BY id;

-- 最近失败任务
SELECT id, server_id, type, LEFT(output,200), created_at
FROM agent_tasks WHERE status='failed' ORDER BY id DESC LIMIT 20;

-- VPN 授权情况
SELECT n.id, n.name, COUNT(g.id) AS grants
FROM vpn_nodes n LEFT JOIN vpn_node_grants g ON g.node_id=n.id AND g.enabled=1
GROUP BY n.id, n.name;

-- 用户流量 Top10
SELECT uuid, up_total, down_total, FROM_UNIXTIME(last_active_at/1000) AS last
FROM user_traffic ORDER BY up_total+down_total DESC LIMIT 10;
```
