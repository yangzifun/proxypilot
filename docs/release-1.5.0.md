# ProxyPilot 1.5.0 发布说明（多 CDN 域名 + 自定义名称）

日期：2026-11-21
版本：面板 V1.5.0 / Agent 1.4.1 / VPN 客户端 1.4.1

## 变更

1. 管理后台 sing-box 节点支持配置多个 CDN：
   - 每个 CDN 可设置名称（CF/ESA/EO 等）、域名、主 CDN、可选优选分组；
   - 节点编辑 / 节点绑定弹窗改为动态表格，最多 8 条；
   - 每个 CDN 生成一条客户端订阅链接，主 CDN 排最前，节点 tag 含 CDN 名称；
   - 未设置优选分组时直接使用域名:443，不混用其它 CDN 的优选 IP；
   - 总链接数上限 32。
2. 兼容旧数据：
   - 新增 `singbox_nodes.cdn_entries` JSON 对象数组；
   - 迁移 0059 自动把旧 `cdn_domain` 回填为单条记录；
   - 保留 `cdn_domain` 兼容列，旧前端/旧订阅继续可用；
   - API 同时返回 `cdn_entries`、`cdn_domains`、`cdn_domain`。
3. 校验：
   - 名称 1-24 字符、禁止控制字符；
   - 域名只允许域名，不接受协议/路径/端口/IP；
   - 域名去重、最多 8 条、主 CDN 唯一；
   - 非法输入后台提示具体条目序号。
4. 节点列表显示 `CDN CF +2`，Tooltip 展示全部 CDN（含主 CDN 标记）。

## 上线与验证

- 面板 healthz `V1.5.0`，15/15 节点在线；
- 迁移 `0059_singbox_cdn_entries` 已应用，旧 `cdn_domain` 已回填；
- 节点 API 已返回 `cdn_entries`/`cdn_domains`/`cdn_domain`；
- server `go build/vet/test` 全绿；web-antd 构建成功；
- 全量发布 dist-release 更新为面板 V1.5.0。

## 注意

- 每个 CDN 域名需要在对应 CDN/Tunnel 侧配置证书并回源到节点，面板只生成链接；
- CF/ESA/EO 等不同厂商不要共用同一优选 IP 列表；
- 同一 CDN 名称可以有多条域名，但域名不能重复；
- 旧订阅链接保持兼容，新增 CDN 链接会随订阅刷新出现。