# Clash/Mihomo 规则模板说明

`rule/configs` 目录提供多场景配置模板。本文档按当前模板实际内容维护，避免“描述与文件不一致”。

## 配置总览

| 配置文件 | 适用场景 | 核心特性 | 推荐内核 |
|---|---|---|---|
| `config.yaml` | 大陆日常全能 | 服务分类完整，地区自动分组，规则覆盖全面 | Mihomo / Clash Meta |
| `full_config.yaml` | 海外全能使用 | 区域分组最全，面向海外网络环境 | Mihomo / Clash Meta |
| `global_config.yaml` | 专注海外访问 | 大陆流量优先直连，海外服务精细分流 | Mihomo / Clash Meta |
| `lite_config.yaml` | 轻量通用 | 分组精简，资源占用更低 | Mihomo / Clash Meta |
| `global_lite_config.yaml` | 轻量全球方案 | 极简分组，海外代理+大陆直连 | Mihomo / Clash Meta |
| `cn_config.yaml` | 海外回国加速 | 面向国内影音/社交/游戏回国链路优化 | Mihomo / Clash Meta |
| `cn_full_config.yaml` | 回国全量代理 | 仅中国服务走代理，非中国流量直连 | Mihomo / Clash Meta |
| `geoip_config.yaml` | IP 精准分流 | 基于 IP 国家策略分流 | Mihomo / Clash Meta |
| `load-balance_config.yaml` | 多节点负载均衡 | 一致性哈希 + 轮询下载策略 | Mihomo / Clash Meta |
| `stream_config.yaml` | 影视流媒体优化 | 流媒体服务独立分组，观影优先 | Mihomo / Clash Meta |
| `internal_config.yaml` | 内网访问 | 含内网策略组与内网分流规则 | Mihomo / Clash Meta |
| `ios_config.yaml` | iOS 专用 | 针对 iOS 内存限制做策略优化 | Mihomo / Clash Meta |
| `no_region_config.yaml` | 无区域分组 | 不依赖固定地区组，侧重通用自动化 | Mihomo / Clash Meta |
| `legacy_config.yaml` | 旧版内核兼容 | 移除 TUN/嗅探/MRS/include-all，使用传统 rules | 传统 Clash 内核 |
| `test.yaml` | 测试模板 | 实验性全量模板，供调试验证 | Mihomo / Clash Meta |

## 重要兼容性说明

- `config.yaml`、`full_config.yaml`、`global_config.yaml` 等主模板依赖以下能力：
  - `rule-providers` + `mrs`
  - `include-all`
  - 大部分模板还启用 `tun`/`sniffer`
- 如果客户端内核较旧，出现 `unknown field` 或 `rule-providers` 相关错误，请改用 `legacy_config.yaml`。
- `legacy_config.yaml` 是旧内核兜底模板，不依赖新特性。

## 使用方式

1. 选择配置模板。
2. 导入客户端。
3. 确认客户端具备对应内核能力（见上节）。
4. 载入并启动。

示例：

```bash
# 通用模板
curl -L https://raw.githubusercontent.com/yangzifun/proxypilot/main/rule/configs/config.yaml -o config.yaml

# 旧版兼容模板
curl -L https://raw.githubusercontent.com/yangzifun/proxypilot/main/rule/configs/legacy_config.yaml -o legacy_config.yaml
```

## 选型建议

- 中国大陆日常：`config.yaml`
- 海外全能：`full_config.yaml`
- 海外专注访问国内：`cn_config.yaml` 或 `cn_full_config.yaml`
- 影视优先：`stream_config.yaml`
- 多节点提速/下载：`load-balance_config.yaml`
- 资源敏感设备：`lite_config.yaml` 或 `global_lite_config.yaml`
- 需要内网访问：`internal_config.yaml`
- iOS 设备：`ios_config.yaml`
- 旧客户端/旧内核：`legacy_config.yaml`

## 辅助规则文件

- `rule/proxy.list`：代理补充规则（DNS、测速、常见需代理域名/IP）
- `rule/direct.list`：直连补充规则

这些文件会被部分模板通过 `RULE-SET` 引用。

## 规则源说明

- 主模板默认使用 MetaCubeX 规则数据：
  - Geosite: `https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/geosite/`
  - GeoIP: `https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/geoip/`
- `legacy_config.yaml` 使用内置传统规则，不依赖远程 `rule-providers`。

## 常见问题

### 1. 导入后报错 `unknown field`

当前内核不支持模板中的新特性。请使用 `legacy_config.yaml`，或升级到 Mihomo / Clash Meta 内核。

### 2. 导入后只有直连、没有代理节点

模板本身通常只提供策略和规则，节点来自你的订阅/客户端节点池。请确认已加载有效节点订阅。

### 3. iOS 点击连接后立即断开

iOS 存在 VPN 扩展内存限制，建议使用 `ios_config.yaml`，并适当减少规则规模。

## 许可证

MIT License。详见 `rule/LICENSE`。

## 免责声明

本项目配置仅供学习研究使用。请遵守当地法律法规并自行承担使用风险。
