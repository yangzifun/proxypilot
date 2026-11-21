# ProxyPilot 1.5.20 发布说明（用户流量统计恢复）

日期：2026-11-21
版本：面板 V1.5.20 / Agent 1.4.2 / VPN 客户端 1.4.1

## 问题

选择/固定 sing-box 1.14.4 后，用户流量页面再次出现统计异常、在线用户变少：

- 此前 Agent 无法解析自建 sing-box 版本，上报 `unknown`，面板按“组件基址已配置”兜底启用 `experimental.v2ray_api`；
- 版本显示修复后 Agent 上报明确版本 `1.14.4`；
- 原兜底逻辑只在 `unknown/空` 时才认组件基址，明确版本 1.14.4 被判定为官方构建，重新生成的配置不再写 `v2ray_api`；
- Agent 只能采到 clash_api，用户流量/在线状态再次停更。

## 修复

`singboxUserStatsCapableFor` 改为：

- 只要显式配置了组件分发基址（面板只允许 S3 自建构建），就视为带统计能力的自建 sing-box；
- 不再要求版本号必须是 `unknown`；
- 无组件基址的官方构建仍不写 `v2ray_api`，保持安全行为。

同时重新下发全部 sing-box 节点，恢复 `experimental.v2ray_api`。

## 验证

- 15 台节点配置全部重新下发；
- 用户流量接口 active 恢复，当前 3 个活跃用户；
- `user_node_counter`/`user_node_recent`/`user_traffic` 时间戳持续刷新；
- 面板 healthz V1.5.20，15/15 节点在线；
- server 测试全绿；
- 全量发布 dist-release 更新 V1.5.20。

## 注意

- 节点仍使用 S3 自建 1.14.4，版本显示正常；
- 若以后切换到官方 sing-box，必须清空组件基址或使用官方能力判定，不能复用本兜底；
- 版本切换/重新下发会短暂重启 sing-box。