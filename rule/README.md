# Clash/Mihomo 规则模板说明

`rule/configs` 目录提供多场景配置模板（当前 6 个）。本文档按当前模板实际内容维护，避免“描述与文件不一致”。

## 配置总览

| 配置文件 | 适用场景 | 核心特性 | 推荐内核 |
|---|---|---|---|
| `global_full_config.yaml` | 全球全量（按业务自动选路） | 20 个策略组全部 `url-test` + `include-all`/`exclude-filter` 自动选路（无地区组）；启用 TUN + 嗅探；75 个规则源；韩国服务经 `korea` 规则源送入 `🚀 默认代理`；中国大陆流量也走代理 | Mihomo / Clash Meta |
| `global_config.yaml` | 全球访问（大陆直连） | 与 `global_full_config.yaml` 逐字同构（同 20 组、同 75 个规则源）；唯一差异是中国大陆域名/IP 走 `DIRECT` | Mihomo / Clash Meta |
| `lite_config.yaml` | 轻量通用 | 16 个策略组、16 个规则源；启用 TUN（`stack: mixed`）+ Fake-IP；不配置嗅探 | Mihomo / Clash Meta |
| `cn_config.yaml` | 海外回国加速 | 回国核心 / 大陆影音 / 国服游戏 + 上海·北京·深圳·香港自动测速组（11 组）；启用 TUN + 嗅探 | Mihomo / Clash Meta |
| `stream_config.yaml` | 影视流媒体优化 | 流媒体服务组 + 地区流媒体组 + 地区自动组（31 组）；强制嗅探并重定向流媒体 CDN；不启用 TUN | Mihomo / Clash Meta |
| `ios_config.yaml` | iOS 专用 | 21 个策略组、合并规则源；`tun` 使用 `stack: system`；显式关闭嗅探（面向 50MB 内存限制） | Mihomo / Clash Meta |

## 与 `global_full_config.yaml` 的差异对比

`global_full_config.yaml` 可视为“全球全量基线模板”。下表描述其余模板相对它的主要差异：

| 模板 | 相对基线的主要差异 | 典型取舍 |
|---|---|---|
| `global_config.yaml` | 仅一处差异：中国大陆域名/IP 规则改为 `DIRECT`；组体系与 75 个规则源与基线逐字同构 | 国内外分流更省心、国内访问延迟更低，但无法手动指定地区出口 |
| `lite_config.yaml` | 策略组 20→16、规则源 75→16，移除复杂业务分组；不配置嗅探 | 资源占用更低，但精细控制下降 |
| `cn_config.yaml` | 目标从“出海”变为“回国”：换成回国核心/大陆影音/国服游戏与上海·北京·深圳·香港自动组（11 组）；不使用 `exclude-filter` | 海外访问国内更稳，海外业务处理更简单 |
| `stream_config.yaml` | 新增流媒体服务组与地区流媒体组（31 组）、强制嗅探并重定向流媒体 CDN；不启用 TUN | 观影体验更好，入站与通用能力下降 |
| `ios_config.yaml` | 合并规则源、显式关闭嗅探、`tun` 改为 `stack: system`；面向 50MB 内存限制 | iOS 稳定性更好，协议识别能力相对克制 |

## 重要兼容性说明

- 本目录全部模板（`global_full_config.yaml`、`global_config.yaml`、`lite_config.yaml`、`cn_config.yaml`、`stream_config.yaml`、`ios_config.yaml`）共同依赖以下能力：
  - `rule-providers` + `mrs`
  - `include-all`
  - `rule-anchor` 锚点（`ip`/`domain`/`class`）
- 入站与嗅探差异：`global_full_config.yaml`、`global_config.yaml`、`cn_config.yaml` 启用 `tun` + `sniffer`；`lite_config.yaml` 启用 `tun` 但未配置 `sniffer`；`ios_config.yaml` 启用 `tun`（`stack: system`）且显式关闭嗅探；`stream_config.yaml` 启用 `sniffer` 但不启用 `tun`。
- 旧内核对 `include-all`/`rule-providers`/`mrs` 支持不佳时会报 `unknown field` 或规则源加载失败。本仓库已不再提供旧内核兜底模板（历史 `legacy_config.yaml` 已移除），请升级到 Mihomo / Clash Meta；若必须使用旧内核，请自行编写不依赖上述特性的传统 `rules` 配置。

## 使用方式

1. 选择配置模板。
2. 导入客户端。
3. 确认客户端具备对应内核能力（见上节）。
4. 载入并启动。

示例：

```bash
# 全球全量模板（本站地址，与模板内规则源同源）
curl -L https://s3.proxypilot.yangzifun.org/rule/configs/global_full_config.yaml -o global_full_config.yaml

# 轻量模板
curl -L https://s3.proxypilot.yangzifun.org/rule/configs/lite_config.yaml -o lite_config.yaml
```

> 以上地址即对象存储（`https://s3.proxypilot.yangzifun.org/rule/`），与模板内的规则源同源、可直接被客户端拉取；GitHub 镜像（`https://raw.githubusercontent.com/yangzifun/proxypilot/main/rule/configs/<文件名>`）仅作备用。

## 选型建议

- 海外全能 / 按业务自动选路：`global_full_config.yaml`
- 全球访问 + 大陆直连：`global_config.yaml`
- 海外回国加速：`cn_config.yaml`
- 影视流媒体优先：`stream_config.yaml`
- 低性能设备 / 手机：`lite_config.yaml`
- iOS 设备：`ios_config.yaml`

## 辅助规则文件

- `rule/proxy.list`：代理补充规则（DNS、测速、常见需代理域名/IP）
- `rule/direct.list`：直连补充规则
- `rule/se_asia.list`：东南亚地区补充规则（泰国/新马/越南/印尼等）
- `rule/korea.list`：韩国地区补充规则（Weverse/HYBE、Naver、Kakao、Melon、TVING、Wavve、Toss、韩国银行/游戏/媒体等，仅收录韩国本地服务）

`rule/proxy.list`、`rule/se_asia.list`、`rule/korea.list` 等文件会被部分模板通过 `RULE-SET` 引用（`korea` 送往 `🚀 默认代理`）。

### 挂载 `rule/korea.list`（其它模板可选）

`rule/korea.list` 已被 `global_full_config.yaml`、`global_config.yaml` 引用（规则送入 `🚀 默认代理`）；其余模板默认不引用，如需可在客户端配置里自行挂载。其行为类型与模板内 `*class` 锚点一致（`behavior: classical`、`format: text`）：

```yaml
rule-providers:
  korea: { <<: *class, url: "https://s3.proxypilot.yangzifun.org/rule/korea.list"}

rules:
  - RULE-SET,korea,🚀 默认代理   # 组名请改成你配置中实际存在的策略组
```

- 若模板已定义 `rule-anchor` 的 `class` 锚点（`{type: http, interval: 86400, behavior: classical, format: text}`），直接复用即可；没有则按该写法补上。
- 未添加 `rule-providers` 与 `RULE-SET` 时，`rule/korea.list` 不会生效。

## 规则源说明

- 主模板默认使用 MetaCubeX 规则数据：
  - Geosite: `https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/geosite/`
  - GeoIP: `https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/geoip/`
- 各模板在 MetaCubeX 规则数据之外，部分模板（`global_full_config.yaml`、`global_config.yaml`、`ios_config.yaml`）还会通过 `RULE-SET` 引用本仓库的 `rule/proxy.list`（`proxylite`）、`rule/se_asia.list`（`se_asia`）、`rule/direct.list`（`custom_direct`）；其中 `global_full_config.yaml`、`global_config.yaml` 另引用 `rule/korea.list`（`korea`）。

## 常见问题

### 1. 导入后报错 `unknown field`

当前内核不支持模板中的新特性。本仓库已不再提供旧内核兜底模板（历史 `legacy_config.yaml` 已移除），请升级到 Mihomo / Clash Meta 内核；若必须使用旧内核，请自行编写不依赖 `include-all`/`rule-providers`/`mrs` 的传统 `rules` 配置。

### 2. 导入后只有直连、没有代理节点

模板本身通常只提供策略和规则，节点来自你的订阅/客户端节点池。请确认已加载有效节点订阅。

### 3. iOS 点击连接后立即断开

iOS 存在 VPN 扩展内存限制，建议使用 `ios_config.yaml`，并适当减少规则规模。

## 许可证

MIT License。详见 `rule/LICENSE`。

## 免责声明

本项目配置仅供学习研究使用。请遵守当地法律法规并自行承担使用风险。
