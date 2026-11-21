# ProxyPilot 1.5.18 发布说明（模板版本选择实际生效）

日期：2026-11-21
版本：面板 V1.5.18 / Agent 1.4.2 / VPN 客户端 1.4.1

## 问题

在 sing-box 模板里从存储桶版本下拉选择版本后，保存并下发仍安装旧版本：

- `node_configs.install_versions` 只在配置内容变化时更新；
- 模板版本变化但配置 JSON 内容不变时，拉取逻辑判定“配置未变化”，直接跳过；
- 因此 `sing-box_version=1.15.0-alpha.13` 不会进入节点下发，节点继续使用 1.14.4。

## 修复

- `singboxNodeApply` 与 `applySingboxNode`（apply-all）把 `install_versions` 纳入变化判断：
  - 比较 `content_hash/path/restart_cmd/install_versions`；
  - 只有完全一致才跳过；
  - 版本变化时更新 `install_versions` 并递增 node_config 版本，触发 Agent 重新安装；
- 保持配置内容不变时不重复写文件，仅执行组件版本切换。

## 验证

- 模板 1 设置为 `1.15.0-alpha.13`，对曼谷1执行 apply：
  - `install_versions` 变为 `{"sing-box":"1.15.0-alpha.13"}`；
  - 节点实际安装 S3 1.15 tar，sha256 `b1a893…`，`sing-box version=1.15.0-alpha.13`，服务 active；
- 恢复模板留空（默认 1.14.4）并重新 apply：
  - `install_versions` 回到 `{"sing-box":"1.14.4"}`；
  - 节点恢复 S3 1.14.4，sha256 `206e0bee…`，服务 active；
- 面板 healthz V1.5.18，15/15 节点在线；
- server 测试全绿；
- 全量发布 dist-release 更新 V1.5.18。

## 注意

- 模板版本留空时使用桶默认 1.14.4；
- 选择 1.15.0-alpha.13 属于预览版，建议先在少量节点验证；
- 版本切换会重启 sing-box，节点连接会短暂中断。