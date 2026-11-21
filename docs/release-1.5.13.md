# ProxyPilot 1.5.13 发布说明（去除重复的组网中心域名）

日期：2026-11-21
版本：面板 V1.5.13 / Agent 1.4.2 / VPN 客户端 1.4.1

## 变更

- 系统设置中「组网中心域名」重复出现两次：
  - 保留「Agent 内网通信（自建组网）」下的 `agent_mesh_center` 作为唯一输入；
  - 删除「自建组网」下重复的 `server_mesh_domain` 输入框；
  - 保存时把同一个值同步写入：
    - 通用设置 `agent_mesh_center`；
    - `/api/admin/vpn/settings` 的 `server_mesh_domain`；
  - 加载以 `agent_mesh_center` 为准，旧数据为空时回退 `server_mesh_domain`；
  - 中心组网 IP、组网网段、数据同步地址等字段保留。
- 文案说明该域名同时用于 Agent 组网与中心状态，只需填写一次。

## 验证

- jsdom：页面只出现 1 个「组网中心域名」输入；
- 修改保存后 `PUT /api/settings` 的 `agent_mesh_center` 与 `PUT /api/admin/vpn/settings` 的 `server_mesh_domain` 一致；
- 旧数据回退正常，其它组网字段不受影响；
- web-antd 构建成功；
- 面板 healthz V1.5.13，15/15 节点在线；
- 全量发布 dist-release 更新 V1.5.13。

## 注意

- 本次仅前端去重与同步保存，未改后端接口/DB；
- 如果旧配置两个值不一致，页面显示 `agent_mesh_center`，保存后统一为同一值。