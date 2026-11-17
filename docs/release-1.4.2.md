# ProxyPilot 1.4.2 发布说明（MTProto 修复）

日期：2026-11-18
版本：面板 V1.4.2 / Agent 1.4.1 / VPN 客户端 1.4.1

## 修复

1. **MTProto 链接改用配置域名**
   - 链接主机优先级：`eff.Host`（连接地址，必须为域名）→ `eff.Domain`（节点/模板域名）→ `LastIP4/LastIP` → `srv.Host`；
   - 不再让服务器探测到的 IP 覆盖已配置域名；
   - 管理端/用户端/邮件/节点配置所有链接入口统一复用 `mtprotoClientLink`，`tg://` 与 `t.me` 同时生效；
   - 单测覆盖 Domain/Host/IP/非法 Domain 优先级与链接解析；golden 期望更新为域名。

2. **MTProto 校验命令兼容 mtg-multi v1.13.0**
   - 面板下发与本地版本探测从 `mtg-multi version` 改为 `mtg-multi --version`（v1.13.0 已移除 `version` 子命令）；
   - 修复“应用 MTProto 后 check 失败 → 回滚配置 → 服务未启动 → 端口不监听”的链路；
   - 香港2 现有节点配置已改为新命令并重新下发成功（mtg active，13709/13719 监听）。

## 现场验证

- 面板 healthz：`V1.4.2`；15/15 节点在线。
- MTProto 链接示例：
  - 洛杉矶（node 2）：`tg://proxy?server=lax.yangzifun.org&port=13709...`
  - 香港2（node 11）：`server=hk2.yangzifun.org`
  - 香港3（node 4）：保持配置的 `hk1.pandajun.xyz`（Host 为连接域名，Domain 为 FakeTLS 伪装域名）
- 香港2：`mtg.service active`，`*:13709` 与 `127.0.0.1:13719` 监听，面板与洛杉矶节点探测 13709 OPEN，doctor/Telegram DC 通。

## 注意

- 其它节点的旧 `node_configs.check_cmd` 会在下次“应用/下发”时由新代码自动纠正；短期无需逐个改库。
- 香港2 的 4 个 MTProto 客户端当前共用同一 secret（链接相同），建议后续轮换。
- 节点日志中 fronting domain `hk2.yangzifun.org:443` 连接被拒，但实际代理走 IPv4:13709 正常；如需 FakeTLS/域名前置再单独处理。
- 真实 Telegram 客户端端到端未在本机执行，建议用新链接实测。