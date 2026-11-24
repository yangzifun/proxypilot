# ProxyPilot 1.6.25 发布说明（Hiddify 兼容与订阅客户端指引）

日期：2026-11-24
版本：面板 V1.6.25 / Agent 1.4.2 / VPN 客户端 1.4.1

## 1. Hiddify UA 兼容

Hiddify 5.x 的 “Not secure” 来自 Core 的 `insecure_reason`，部分解析器对缺失 `insecure` 参数会按不安全处理。

现已支持按 User-Agent 区分订阅输出：

- 当 UA 包含 `hiddify`（大小写不敏感）时：
  - AnyTLS：追加/修正 `insecure=0`；
  - Hysteria2：追加/修正 `insecure=0`；
  - TUIC：追加/修正 `allow_insecure=0`；
  - 保留 `sni`、`alpn`、`path`、`host`、fragment 等参数；
- 普通 UA 输出保持标准 URI，不追加 `insecure` 参数；
- 仅影响 `/sub/{uuid}` 与 `/batch-configs/{uuid}` 的链接后处理；
- 外部订阅源、Clash/Mihomo 输出不变。

实测：

- `Hiddify/5.9.11` UA 下：
  - anytls 带 `insecure=0`；
  - hy2 带 `insecure=0`；
  - tuic 带 `allow_insecure=0`；
- `v2rayN/7.0` UA 下保持原始 URI，无 insecure 参数。

## 2. 订阅生成客户端指引

桌面端与移动端订阅生成页增加客户端适用说明：

- 原始订阅：
  - Hiddify、v2rayN、Shadowrocket、Stash、Karing、NekoBox、sing-box；
  - 明确提示「Hiddify 请选择原始订阅」；
- Clash 订阅：
  - Clash Verge、ClashX、Clash for Windows、Mihomo、FlClash；
- Mihomo 订阅：
  - mihomo / Clash Meta 系；
- 批量配置：
  - 脚本、一次性导入、二次加工。

格式切换处显示当前格式的适用客户端，原有生成、复制、二维码、下载功能不变。

## 3. 其他修复

- 本机未安装 `proxypilot-agent` 时，本机 VPN 证书下发项不再显示失败；
- 证书状态回采、离线重试与自动续期继续生效；
- 全部 15 台节点证书已 synced，指纹一致：
  - `34486A183FDF7144FAF0378A75D2694BAE814D5BE1EE63C4DAD23F9B3A307DA7`
- 节点域名拆分：
  - `lax1-node.yzfn.top` 对应洛杉矶1；
  - `lax2-node.yzfn.top` 对应洛杉矶2。

## 验证

- server 构建、测试全绿；
- web-antd 构建成功，render-check 38/0；
- Hiddify/普通 UA 订阅解码验证通过；
- 15/15 节点在线且证书 synced；
- 面板 healthz V1.6.25；
- 全量发布 dist-release 更新 V1.6.25。

## 说明

- Hiddify 若仍显示 Not secure，请先删除旧配置并重新导入最新订阅，避免旧缓存中的 `insecure=1` 继续生效；
- 普通 UA 的链接保持标准格式，不强制加 `insecure=0`。