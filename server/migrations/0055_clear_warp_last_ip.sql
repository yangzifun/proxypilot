-- 清理 servers.last_ip6 里的 Cloudflare WARP 地址、last_ip4 里的 CGNAT 组网地址
--
-- 背景（task-15）：
--   Agent 1.1 起采集侧跳过 WARP/组网网卡，对「只有 WARP IPv6」的机器上报空串，
--   表示「没有真实 IPv6」。但旧落库 SQL 是 COALESCE(NULLIF(?, ''), last_ip6)，
--   空串不会覆盖旧值，于是历史 WARP 地址（2606:4700:cf1:...）一直留在库里、
--   显示在面板「IP / 主机」列。
--
--   代码层已改为「ipv6 字段存在即权威：合法全球单播才写，空串/WARP 段明确清空」，
--   这条迁移负责把存量脏数据一次性清掉（幂等：重复执行影响 0 行）。
--
-- 说明：
--   * 只清 Cloudflare WARP 段 2606:4700::/32；2602:fa4f::/32 经现场核实是
--     东京2/东京3 物理网卡上的真实公网 IPv6（2026-11-12），必须保留；
--   * 组网地址本身仍在 last_mesh_ip4 / last_mesh_ip6，不受本迁移影响；
--   * 只清明确的 WARP 段与 100.64/10 CGNAT，不碰真实网卡地址。

UPDATE servers
   SET last_ip6 = ''
 WHERE last_ip6 <> ''
   AND INET6_ATON(last_ip6) >= INET6_ATON('2606:4700::')
   AND INET6_ATON(last_ip6) <= INET6_ATON('2606:4700:ffff:ffff:ffff:ffff:ffff:ffff');

UPDATE servers
   SET last_ip4 = ''
 WHERE last_ip4 <> ''
   AND last_ip4 REGEXP '^100\\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\\.'