# ProxyPilot 1.5.6 发布说明（节点域名统一 + MTProto 模板化）

日期：2026-11-21
版本：面板 V1.5.6 / Agent 1.4.1 / VPN 客户端 1.4.1

## 变更

1. 节点绑定全局「域名」改为必填：
   - 支持合法域名或 IPv4；
   - 拒绝协议、路径、端口、空格等非法输入；
   - 所有协议统一使用该域名：
     - sing-box：`domain` 使用全局域名；
     - mihomo：`domain` 使用全局域名；
     - MTProto：`host`（连接地址）使用全局域名。
2. 移除节点级协议域名/端口输入：
   - 删除 sing-box/mihomo 节点单独域名；
   - 删除 MTProto 节点伪装域名、端口输入。
3. MTProto 伪装域名与端口统一由模板设定：
   - 后端 `mtprotoEffective` 的 Domain/Port 只取模板，忽略节点 domain/port 覆盖；
   - 模板 port=0 时默认 13709；
   - 节点 `host` 仍为全局域名/IP，用于客户端连接地址；
   - 存量节点无需改 DB；已有 MTProto client secret 不会自动重写。
4. MTProto 模板编辑：
   - 伪装域名（FakeTLS SNI）必填并可编辑；
   - 端口可编辑；
   - 节点统一继承模板，不再在节点侧填写。
5. CDN 域名仍只在 sing-box 节点配置，未扩展到 mihomo/MTProto。

## 兼容性

- 旧节点的全局域名回填 `row.domain`；旧协议级覆盖不再展示，保存后统一为全局域名；
- MTProto 节点级 domain/port 仍可解析但后端忽略，模板生效；
- 不发新迁移；
- 旧 MTProto 客户端 secret 不自动更新；如模板伪装域名变化导致 SNI 不一致，需要重新生成客户端 secret。

## 验证

- 真实浏览器：节点绑定编辑显示「域名（必填，可填域名或 IP）」，无协议级域名/端口字段，MTProto 提示模板统一设定；
- jsdom：全局域名必填/非法输入拦截；sing-box/mihomo payload domain=全局；MTProto payload 只有 host=全局，无 domain/port；MTProto 模板伪装域名必填；
- server `go build/vet/test` 全绿；
- web-antd 构建成功；
- 面板 healthz V1.5.6，15/15 节点在线；
- 全量发布 dist-release 更新 V1.5.6。

## 注意

- 全局域名必须是客户端可访问的连接地址（域名或 IPv4）；CDN 域名另在 sing-box CDN 条目中维护；
- MTProto 伪装域名必须是域名，不能填 IP；
- 修改模板伪装域名/端口后需重新下发 MTProto 节点；客户端 secret 是否更新按需处理。