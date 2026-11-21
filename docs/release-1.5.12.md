# ProxyPilot 1.5.12 发布说明（统计恢复、sing-box 来源/版本、1.15 组件）

日期：2026-11-21
版本：面板 V1.5.12 / Agent 1.4.2 / VPN 客户端 1.4.1

## 修复与变更

1. **用户流量全部离线/统计停更**
   - 10:58 全量 re-apply 后自建 sing-box（version unknown）未写 `experimental.v2ray_api`；
   - Agent 只采到 clash_api，用户计数停更，活跃窗口后全部离线；
   - 面板渲染改为：自建组件（S3 component base）或版本含 proxypilot/with_v2ray_api 时启用 v2ray_api；官方明确版本仍不启用；
   - 已重新下发全部 sing-box 节点，用户计数恢复，在线用户 active 正常。

2. **禁止第三方 sing-box**
   - `component_base_url` 固定/校验为 `https://s3.proxypilot.yangzifun.org/singbox`，非法设置/环境变量 fail-closed；
   - Agent 安装脚本与组件安装器删除 GitHub 回落，只允许 S3 组件 URL + sha256；
   - 14 台节点二进制已核对为 S3 sha（曼谷1 原为官方 GitHub 构建，已备份并从 S3 重装）。

3. **服务状态显示 sing-box 版本**
   - Agent 安装/切换 sing-box 后写 `/etc/sing-box/.proxypilot-version`；
   - `sing-box version` 输出 unknown 时，Agent metrics 回退版本标记；
   - 服务器详情「服务状态」显示 `已装 v1.14.4`，无版本显示 `—`；
   - Agent 已升级到 1.4.2，15/15 节点上报 1.14.4。

4. **S3 新增 sing-box 1.15.0-alpha.13 自编译统计版**
   - linux-amd64/arm64 二进制与 tar.gz，tags `with_quic,with_utls,with_clash_api,with_v2ray_api`，版本注入正确；
   - `singbox/versions.json` 多版本索引；manifest 默认仍 1.14.4，新增 1.15 预览版本；
   - 曼谷1 实测 1.15 version/check/服务 active 后已恢复生产 1.14.4。

5. **模板版本从存储桶选择**
   - 新增 `GET /api/singbox/component-versions`，从 S3 `versions.json`/manifest 读取版本；
   - 模板编辑器版本下拉改用该接口，标注 S3/正式版/预览版，不再依赖 GitHub releases；
   - 留空仍使用桶默认（1.14.4）；选择 1.15.0-alpha.13 后按版本 tar 安装。

## 验证

- 面板 healthz V1.5.12，15/15 节点在线；
- Agent 15/15 升级到 1.4.2；
- 全部 sing-box 节点 metrics 上报 1.14.4；
- 用户流量页 active 由 0 恢复（当前 3 个活跃用户）；
- `/api/singbox/component-versions` 返回 1.14.4 + 1.15.0-alpha.13；
- 14 台节点 sing-box sha 与 S3 一致，`check` 通过、服务 active；
- 新加坡2 allow_exec=0，版本标记通过临时授权写入后已恢复。

## 注意

- 1.15.0-alpha.13 为预览版，建议先少量节点选择模板版本验证；
- 默认 `component_version` 仍为 1.14.4，不会自动升级 1.15；
- 节点重装 sing-box 会写版本标记；已有节点本次已补写；
- 客户端旧链接/错误密码仍会在日志出现 invalid request，与本修复无关。