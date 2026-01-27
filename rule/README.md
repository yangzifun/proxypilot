# Clash/Mihomo 多场景配置集

本仓库提供多种 Clash/Mihomo 配置文件，针对不同的使用场景进行优化。所有配置都基于相同的核心架构，但分流策略和服务分类有所不同。

## 📁 配置文件概览

| 配置文件 | 适用场景 | 特点说明 | 规则源 |
|----------|----------|----------|--------|
| config.yaml | **通用全能配置** | 最完整的配置文件，包含所有服务分类和地区自动选择 | MetaCubeX |
| full_config.yaml | **海外用户专用** | 与通用配置相同，适用于海外用户访问国内服务 | MetaCubeX |
| global_config.yaml | **全球直连配置** | 所有国内流量直连，专注于海外服务访问 | MetaCubeX |
| lite_config.yaml | **精简轻量配置** | 简化版配置，核心服务分类，资源占用少 | Loyalsoldier/clash-rules |
| cn_config.yaml | **回国加速配置** | 专门为海外用户访问国内服务优化 | MetaCubeX |
| geoip_config.yaml | **精准IP分流** | 基于GEOIP的IP精准分流，按目标IP国家定向代理 | MetaCubeX |
| load-balance_config.yaml | **负载均衡配置** | 负载均衡策略，支持一致性哈希和轮询，适合多节点加速 | MetaCubeX |
| stream_config.yaml | **影视流媒体优化** | 针对Netflix、YouTube等影视平台的专门优化配置 | MetaCubeX |

## 🚀 快速开始

### 1. 使用 Clash/Mihomo 客户端

1. **下载配置文件**: 选择适合你使用场景的配置文件
2. **导入到客户端**:
   - Clash for Windows: 点击 Profiles → 拖拽配置文件到窗口
   - ClashX (macOS): 将配置文件拖到状态栏图标
   - Mihomo: 将配置文件放入配置目录
3. **启动代理**: 选择配置文件并启动服务

### 2. 命令行使用 (curl)

```bash
# 下载配置文件到本地
curl -L https://raw.githubusercontent.com/yangzifun/proxypilot/main/rule/configs/config.yaml -o clash_config.yaml

# 或使用特定配置文件
curl -L https://raw.githubusercontent.com/yangzifun/proxypilot/main/rule/configs/lite_config.yaml -o clash_lite.yaml
```

### 3. 直接使用 URL

在支持远程配置的客户端中，直接使用配置文件的原始 URL:
```
https://raw.githubusercontent.com/yangzifun/proxypilot/main/rule/configs/config.yaml
```

## 🔧 核心配置特性

### 共同基础配置
所有配置文件共享以下基础设置：
- **端口设置**: 7890 (HTTP)、7891 (SOCKS5)、7892 (Redir)、7893 (Mixed)、7894 (TProxy)
- **TUN 模式**: 启用，支持全局透明代理，自动路由和重定向
- **DNS 配置**: 使用 Fake-IP 模式，支持 DoH，监听端口 1053
- **嗅探功能**: 自动识别 HTTP/TLS/QUIC 流量，强制嗅探特定域名
- **广告拦截**: 内置广告域名拦截
- **全局指纹**: Chrome 浏览器指纹模拟
- **连接保持**: keep-alive 空闲 600 秒，间隔 15 秒
- **日志级别**: warning，平衡信息量与性能

### 代理策略架构
所有配置都采用分层策略设计：
1. **入站层**: TUN 设备接收所有流量
2. **规则匹配层**: 基于域名/IP进行精确匹配
3. **策略组层**: 流量分配到不同代理策略组
4. **节点选择层**: 地区自动选择最快节点

## 📊 各配置文件详细说明

### 1. `config.yaml` - 通用全能配置
**适用场景**: 中国大陆用户日常使用

**核心特性**:
- **完整服务分类**: AI、社交、流媒体、游戏、国内服务等
- **智能地区选择**: 香港、日本、美国、新加坡、台湾、德国、中国自动选择
- **国内服务优化**: 微博、微信、支付宝等国内服务使用回国节点
- **游戏平台分流**: Steam、Ubisoft、Epic、EA、PlayStation 单独优化

**分流策略**:
- AI 服务 (OpenAI、Gemini、Claude) → 美国/日本/新加坡/台湾/德国
- 社交办公 (Telegram、GitHub、Discord) → 自动选择
- 流媒体 (YouTube、Netflix、Disney+) → 自动选择
- 国内服务 (微博、微信、支付宝) → 回国核心节点
- 游戏平台 → 直连 + 代理混合策略

### 2. `full_config.yaml` - 海外用户专用
**适用场景**: 海外用户访问国内外服务

**与 config.yaml 的区别**:
- 内容与 `config.yaml` 完全相同
- 适用于海外网络环境
- 国内服务使用代理访问，避免网络限制

### 3. `global_config.yaml` - 全球直连配置
**适用场景**: 专注于海外服务，国内流量直连

**核心特性**:
- **国内流量全直连**: 所有中国大陆域名/IP 直接连接
- **精简代理组**: 移除中国大陆相关分组
- **海外服务优化**: 专注于 AI、社交、流媒体等海外服务
- **地区自动选择**: 香港、日本、美国、新加坡、台湾、德国自动选择

**分流策略**:
- 中国大陆服务 (哔哩哔哩、爱奇艺、微博等) → DIRECT
- AI 服务 → 美国/日本/新加坡/台湾/德国
- 海外流媒体 → 自动选择
- 社交办公 → 自动选择

### 4. `lite_config.yaml` - 精简轻量配置
**适用场景**: 资源有限的设备或简单使用场景

**核心特性**:
- **策略组精简**: 合并相似服务，减少策略组数量
- **核心服务保留**: AI、社交、流媒体、游戏、国内服务五大类
- **简化的自动选择**: 基础地区自动选择
- **资源占用低**: 配置简单，启动快速
- **使用 Loyalsoldier 规则源**: 更稳定可靠的规则更新

**服务分类**:
- 🤖 AI 服务: OpenAI、Gemini、Claude 等所有 AI 服务
- 📲 社交办公: Telegram、GitHub、Discord、Microsoft 等
- 🎬 流媒体: YouTube、Netflix、Disney+、Spotify 等
- 🎮 游戏平台: Steam、Ubisoft、Epic、EA、Sony 等
- 🇨🇳 大陆服务: 所有中国大陆服务

### 5. `cn_config.yaml` - 回国加速配置
**适用场景**: 海外用户专门访问国内服务

**核心特性**:
- **回国核心优化**: 专门为访问国内服务设计
- **地区自动选择**: 上海、北京、深圳、香港自动选择
- **国内服务分类**: 影音、社交、游戏、支付等详细分类
- **海外流量直连**: Google、YouTube 等海外服务直连

**分流策略**:
- 影音娱乐 (哔哩哔哩、爱奇艺、腾讯视频) → 大陆影音节点
- 社交支付 (微信、支付宝、微博) → 回国核心节点
- 国服游戏 → 游戏加速节点
- 海外服务 (Google、YouTube) → DIRECT

### 6. `geoip_config.yaml` - 精准IP分流配置
**适用场景**: 需要按目标IP国家进行精确分流的用户

**核心特性**:
- **GEOIP精准分流**: 基于目标IP地理位置进行精准分流
- **国家IP匹配**: 香港、日本、美国、新加坡、韩国、台湾、德国IP独立匹配
- **域名优先匹配**: 域名规则优先于IP规则，提高匹配速度
- **智能兜底策略**: 未匹配的境外IP流量使用默认代理

**分流策略**:
- 特定服务 (OpenAI、Gemini、YouTube等) → 指定服务策略组
- 中国大陆IP → 直连
- 香港IP → 香港IP策略组
- 日本IP → 日本IP策略组
- 美国IP → 美国IP策略组
- 其他国家IP → 默认代理

### 7. `load-balance_config.yaml` - 负载均衡配置
**适用场景**: 拥有多个节点需要负载均衡的用户

**核心特性**:
- **一致性哈希负载均衡**: 相同域名走相同节点，防止IP跳变
- **轮询下载策略**: 适合大文件下载，叠加所有节点带宽
- **地区均衡组**: 香港、日本、美国、新加坡独立均衡组
- **流媒体优化**: YouTube使用负载均衡，Netflix使用自动选择

**策略组类型**:
- **⚖️ 全球均衡**: 一致性哈希，适合日常使用
- **📥 极速下载**: 轮询策略，适合下载工具
- **🇭🇰 香港均衡**: 香港节点负载均衡
- **🇺🇸 美国均衡**: 美国节点负载均衡

**使用场景**:
- 日常浏览 → 一致性哈希负载均衡
- 文件下载 → 轮询负载均衡
- 流媒体观看 → 按服务指定策略

### 8. `stream_config.yaml` - 影视流媒体优化配置
**适用场景**: 专注影视流媒体观看的用户

**核心特性**:
- **完整流媒体支持**: 支持Netflix、YouTube、Disney+、Prime Video等20+流媒体服务
- **服务独立分组**: 每个流媒体服务都有独立策略组
- **流媒体专用节点**: 香港、台湾、日本、新加坡、美国流媒体专用节点池
- **嗅探优化**: 强制嗅探流媒体域名，确保CDN正确识别

**支持的流媒体服务**:
- 🎥 Netflix、🎬 YouTube、🎞️ Disney+、📺 Prime Video
- 🍎 Apple TV+、🟢 Hulu、🟠 Max、🔵 Paramount+
- 🦚 Peacock、💡 Discovery+、🌸 AbemaTV、🟣 Viu
- 📺 TVB、🎮 Bahamut、🎵 Spotify

**优化特性**:
- 流媒体节点池容忍度设为50ms，防止播放中频繁跳节点
- YouTube使用负载均衡策略，叠加带宽实现秒开
- 强制嗅探流媒体域名，优化CDN识别

## 🎯 策略组说明

### 通用策略组 (所有配置)
- **🚀 默认代理**: 通用代理选择，包含除"直连"外的所有节点
- **⚡ 自动选择**: 全局自动选择最快节点 (默认延迟20ms)
- **🚫 广告拦截**: 广告域名拦截 (REJECT/DIRECT)
- **🎯 直连**: 直连策略组
- **🐟 漏网之鱼**: 未匹配流量的默认处理 (DIRECT + 自动选择)

### 地区自动选择组
- **🇭🇰 香港自动**: 匹配 "香港|HK|Hong Kong" 的节点
- **🇯🇵 日本自动**: 匹配 "日本|JP|Japan|东京" 的节点
- **🇺🇸 美国自动**: 匹配 "美国|US|USA|洛杉矶|弗吉尼亚" 的节点
- **🇸🇬 新加坡自动**: 匹配 "新加坡|SG|Singapore" 的节点
- **🇹🇼 台湾自动**: 匹配 "台湾|TW|Taiwan|台北" 的节点
- **🇩🇪 德国自动**: 匹配 "德国|DE|Germany" 的节点
- **🇨🇳 中国自动**: 匹配 "上海|北京|广州|深圳|中国|CN" 的节点

## 📋 规则匹配优先级

所有配置文件都遵循相同的匹配优先级：

1. **🚫 广告拦截**: 首先拦截广告域名
2. **🎯 私有网络/LAN**: 本地网络直连
3. **🔍 特定服务**: 按服务类别精确匹配 (AI、社交、流媒体等)
4. **🌍 地区IP**: 根据目标IP地理位置分流
5. **🚀 默认代理**: GFW域名和代理列表
6. **🐟 漏网之鱼**: 未匹配的流量默认处理

## 🔄 配置文件选择指南

### 中国大陆用户
- **日常使用**: `config.yaml` - 完整功能，最佳体验
- **轻量需求**: `lite_config.yaml` - 资源占用少，核心功能 (使用 Loyalsoldier 规则源，更稳定)
- **专注海外**: `global_config.yaml` - 国内服务直连，专注海外
- **影视流媒体**: `stream_config.yaml` - 完整流媒体支持，最优观看体验
- **多节点负载均衡**: `load-balance_config.yaml` - 多个节点负载均衡，最大化带宽利用

### 海外用户
- **访问国内外**: `full_config.yaml` - 完整功能，国内外兼顾
- **专注国内**: `cn_config.yaml` - 专门优化国内服务访问
- **专注海外**: `global_config.yaml` - 国内直连，专注海外服务
- **精准IP分流**: `geoip_config.yaml` - 按目标国家IP精准分流
- **影视流媒体**: `stream_config.yaml` - 完整流媒体支持，最优观看体验

### 高级用户场景
- **精准IP控制**: `geoip_config.yaml` - 需要按目标国家IP精确控制流量
- **多节点加速**: `load-balance_config.yaml` - 拥有多个节点，需要负载均衡叠加带宽
- **专业影视观看**: `stream_config.yaml` - 专注Netflix、YouTube等流媒体平台
- **下载优化**: `load-balance_config.yaml` - 大文件下载需要轮询负载均衡

## 📁 辅助文件

- `proxy.list` - 代理补充列表，包含国外DNS、测速接口、必应等需要代理的域名/IP
- `direct.list` - 直连补充列表，包含特定需要直连的域名 (如 baidu.com, yangzifun.org 等)

### proxy.list 内容说明
- 国外DNS服务: dns.google, 1dot1dot1dot1.cloudflare-dns.com 等
- 测速接口: cp.cloudflare.com, connectivitycheck.gstatic.com 等
- 必应搜索: bing.com, cn.bing.com
- IP地址: 8.8.8.8, 1.1.1.1 等公共DNS

### direct.list 内容说明
- 百度相关服务: baidu.com
- 个人域名: yangzifun.org, yangzihome.space

## 📈 性能优化建议

### 设备兼容性
- **内存有限设备** (手机、路由器、树莓派): 使用 `lite_config.yaml`
- **高性能设备** (PC、服务器): 使用 `config.yaml` 或 `stream_config.yaml`
- **低功耗设备**: 考虑关闭TUN模式以节省资源

### 网络环境
- **海外访问国内**: 使用 `cn_config.yaml`
- **国内访问海外**: 使用 `config.yaml` 或 `global_config.yaml`
- **游戏加速**: 所有配置都包含游戏平台优化

### 特殊需求
- **多节点负载均衡**: 使用 `load-balance_config.yaml` 叠加带宽
- **影视流媒体优化**: 使用 `stream_config.yaml` 获得最佳观看体验
- **精准IP控制**: 使用 `geoip_config.yaml` 进行国家级IP分流
- **大文件下载**: 使用 `load-balance_config.yaml` 的轮询下载策略

## 🔗 规则源说明

### MetaCubeX 规则源 (默认)
大多数配置文件使用 MetaCubeX 规则源，定期自动更新：
- 域名规则: `https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/geosite/`
- IP 规则: `https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/geoip/`

### Loyalsoldier/clash-rules 规则源
`lite_config.yaml` 使用 Loyalsoldier/clash-rules 规则源：
- 规则更新更稳定，适合轻量配置
- 支持的服务类型与 MetaCubeX 略有不同
- 适合追求稳定性的用户

## ⚠️ 常见问题解答

### Q1: 配置文件中的 "生成时间: 2026-01-13" 是什么意思？
这是配置文件的生成时间戳，表示配置文件是基于这个时间点的规则生成的。实际使用时，规则会根据规则源自动更新。

### Q2: 如何更新配置文件？
配置文件中的规则源会自动更新。如果需要更新整个配置文件，请重新从本仓库下载最新版本。

### Q3: 为什么有些配置无法正常工作？
1. 检查代理节点是否正确配置
2. 确保代理服务正常运行
3. 检查网络连接是否正常
4. 尝试重启 Clash/Mihomo 客户端

### Q4: 如何自定义规则？
1. 修改对应的配置文件
2. 在 `rules` 部分添加自定义规则
3. 在 `proxy-groups` 部分添加自定义策略组
4. 保存并重新加载配置

### Q5: 配置文件太大，加载慢怎么办？
1. 使用 `lite_config.yaml` 精简配置
2. 关闭不必要的策略组
3. 减少规则数量

### Q6: 如何切换不同的配置文件？
1. 在 Clash 客户端中停止当前配置
2. 导入新的配置文件
3. 启动新配置

## 🤝 贡献与反馈

欢迎提交 Issue 或 Pull Request 来改进配置：

1. **报告问题**: 在 GitHub Issues 中报告配置问题
2. **提交改进**: 通过 Pull Request 提交配置文件改进
3. **建议新功能**: 提出新的配置场景或优化建议

### 贡献指南
1. Fork 本仓库
2. 创建新的分支 (`git checkout -b feature/your-feature`)
3. 提交更改 (`git commit -m 'Add some feature'`)
4. 推送到分支 (`git push origin feature/your-feature`)
5. 创建 Pull Request

## 📄 许可证

MIT License - 查看 [LICENSE](LICENSE) 文件了解详情。

## ⚠️ 免责声明

本项目提供的配置文件仅供学习和研究使用。使用者应对自己的行为负责，作者不承担任何责任。请遵守相关法律法规。

---

**提示**: 配置文件会定期更新以保持规则的最新性。建议定期检查本仓库获取最新版本。