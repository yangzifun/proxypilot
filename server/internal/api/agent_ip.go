package api

import (
	"net"
	"strings"
)

// 面板「IP / 主机」列的落库过滤（task-15）。
//
// 背景：Agent 1.1 起，采集侧会跳过 WARP/组网网卡；「只有 WARP IPv6」的机器上报空串
// 表示「没有真实 IPv6」。但落库 SQL 原来是 COALESCE(NULLIF(?, ''), last_ip6)，
// 空串永远不覆盖旧值，库里历史 WARP 地址（2606:4700:cf1:...）就一直挂在列表上。
//
// 这里把「该写什么 / 该不该清空」的判定抽成纯函数，方便单测覆盖：
//   - last_ip6 只接受真实网卡的全球单播 IPv6；WARP 段一律拒写
//   - 区分「字段缺失」（老脚本 Agent，保留旧值）与「字段存在」（本次上报即权威，空串就清空）
//   - last_ip4 不接受 CGNAT 100.64/10（WARP 组网地址只进 last_mesh_ip4）

// warpIPv6CIDRs 与 Agent 侧采集口径保持一致（agent/internal/metrics/net.go）。
//
// 只把 2606:4700::/32 当 WARP 段。2602:fa4f::/32 曾在现场被误判为 WARP，
// 2026-11-12 核实为东京2/东京3 物理网卡 eth0 上的真实公网 IPv6（默认路由
// via 2602:fa4f:a30::1），不能过滤，否则会把真实 IPv6 隐藏掉。
var warpIPv6CIDRs = []string{
	"2606:4700::/32", // Cloudflare WARP 设备地址/网关主段
}

var warpIPv6Nets = func() []*net.IPNet {
	out := make([]*net.IPNet, 0, len(warpIPv6CIDRs))
	for _, c := range warpIPv6CIDRs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}()

// isWARPIPv6 判断地址是否落在 Cloudflare WARP 的 IPv6 段。
func isWARPIPv6(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range warpIPv6Nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// isCGNATIPv4 判断是否 100.64.0.0/10（CGNAT，WARP 组网 IPv4 常用此段）。
func isCGNATIPv4(v4 net.IP) bool {
	return len(v4) == net.IPv4len && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

// usablePublicIPv4 返回可写入 last_ip4 的公网 IPv4。
//
// 私网 / 回环 / 链路本地 / 未指定 / 组播 / CGNAT(100.64/10) 一律返回空串。
// 不合法或非公网时返回 ""，调用方据此决定「保留旧值」还是「清空」。
func usablePublicIPv4(s string) string {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return ""
	}
	v4 := ip.To4()
	if v4 == nil {
		return ""
	}
	if isPrivateIP(v4.String()) || isCGNATIPv4(v4) || v4.IsUnspecified() || v4.IsMulticast() {
		return ""
	}
	if v4[0] == 0 { // 0.0.0.0/8：非法来源地址
		return ""
	}
	return v4.String()
}

// usableGlobalIPv6 返回可写入 last_ip6 的全球单播 IPv6。
//
// 只接受 2000::/3（全球单播）；WARP 段、私网/ULA、回环、链路本地、IPv4 映射地址
// 一律返回空串，宁可让列空着也不显示 WARP 地址。
func usableGlobalIPv6(s string) string {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil || ip.To4() != nil {
		return ""
	}
	ip6 := ip.To16()
	if ip6 == nil || ip6.IsUnspecified() || ip6.IsLoopback() ||
		ip6.IsLinkLocalUnicast() || ip6.IsLinkLocalMulticast() {
		return ""
	}
	if isPrivateIP(ip6.String()) { // 含 ULA fc00::/7
		return ""
	}
	if isWARPIPv6(ip6) {
		return ""
	}
	if ip6[0]&0xe0 != 0x20 { // 只接受 2000::/3 全球单播
		return ""
	}
	return ip6.String()
}

// displayIPv6 是列表/详情展示 last_ip6 前的兜底过滤：
// 历史脏数据里的 WARP 段、非全球单播、私网地址一律不展示（返回空串）。
func displayIPv6(s string) string {
	return usableGlobalIPv6(s)
}

// agentIPResolution 是一次 poll 解析出的各 IP 列落库意图。
type agentIPResolution struct {
	IP        string // last_ip：连接来源地址（语义不变）
	IP4       string // last_ip4
	IP6       string // last_ip6
	PrivateIP string // last_ip_private
	MeshIP4   string // last_mesh_ip4
	MeshIP6   string // last_mesh_ip6
	// ClearIP4/ClearIP6 为真表示「本次明确要把它置空」，落库时不能再用 COALESCE 保留旧值。
	// 典型场景：Agent 1.1 上报 ipv6="" 表示没有真实 IPv6；旧值是 CGNAT/WARP 脏数据。
	ClearIP4 bool
	ClearIP6 bool
}

// resolveAgentIPs 根据 poll 请求体与连接来源解析各 IP 列的落库值。
//
// connIP 是面板看到的连接来源（clientIP）；body 是 Agent 的 poll 请求体。
// 纯函数（只依赖入参与旧快照），便于单测覆盖 task-15 的三类场景。
func resolveAgentIPs(body map[string]any, connIP string, srv *agentServer) agentIPResolution {
	out := agentIPResolution{}

	// last_ip：连接来源；来源缺失/非法时回落到 Agent 上报的 ip（语义与旧实现一致）
	ip := strings.TrimSpace(connIP)
	if ip == "" || ip == "unknown" {
		if mine := strings.TrimSpace(jsonStr(body["ip"])); regexpIP.MatchString(mine) {
			ip = truncateRunes(mine, 64)
		}
	}
	out.IP = ip

	// Agent 主动上报的出口公网 IP：走内网通道时，连接来源是 100.64/10 等地址，
	// 必须靠这个字段才能拿到真正的公网 IP。
	agentPublicIP := strings.TrimSpace(jsonStr(body["public_ip"]))
	agentPrivateIP := strings.TrimSpace(jsonStr(body["private_ip"]))
	if agentPrivateIP == "" {
		agentPrivateIP = strings.TrimSpace(jsonStr(body["ip"]))
	}

	// 组网地址（Cloudflare WARP）：Agent 直接从组网网卡读取，v4/v6 各一
	if v4 := strings.TrimSpace(jsonStr(body["mesh_ip4"])); regexpIPv4.MatchString(v4) {
		out.MeshIP4 = truncateRunes(v4, 64)
	}
	if v6 := strings.TrimSpace(jsonStr(body["mesh_ip6"])); strings.Contains(v6, ":") && regexpIP.MatchString(v6) {
		out.MeshIP6 = truncateRunes(v6, 64)
	}

	// 内网地址：优先 Agent 上报的本机内网 IP，其次连接来源（WARP / 内网）
	if agentPrivateIP != "" && isPrivateIP(agentPrivateIP) && regexpIP.MatchString(agentPrivateIP) {
		out.PrivateIP = truncateRunes(agentPrivateIP, 64)
	} else if ip != "" && isPrivateIP(ip) && regexpIP.MatchString(ip) {
		out.PrivateIP = truncateRunes(ip, 64)
	}

	// 公网 IPv4：Agent 探测 > 连接来源 > 上报的 ipv4/ip；CGNAT / 私网不算公网 IPv4
	for _, cand := range []string{agentPublicIP, ip} {
		if v := usablePublicIPv4(cand); v != "" {
			out.IP4 = v
			break
		}
	}
	if out.IP4 == "" {
		mine := strings.TrimSpace(jsonStr(body["ipv4"]))
		if mine == "" {
			mine = strings.TrimSpace(jsonStr(body["ip"]))
		}
		out.IP4 = usablePublicIPv4(mine)
	}
	if out.IP4 == "" {
		// 没有新的合法公网 IPv4：旧值合法才保留；旧值是 CGNAT/私网等脏数据则明确清空
		// （不能让 COALESCE 继续把它留在 last_ip4 里）
		if v := usablePublicIPv4(srv.LastIP4); v != "" {
			out.IP4 = v
		} else if strings.TrimSpace(srv.LastIP4) != "" {
			out.ClearIP4 = true
		}
	}

	// IPv6：区分「字段缺失」与「字段存在」。
	//   - 字段存在：本次上报即权威。合法全球单播才写；空串/WARP 段/私网 → 明确清空旧值。
	//     （Agent 1.1 对「只有 WARP IPv6」的机器正是上报空串，必须能覆盖历史 WARP 值）
	//   - 字段缺失（老脚本 Agent）：保持旧值，但旧值本身也必须合法（WARP 段照样丢弃）。
	raw6, reported6 := body["ipv6"]
	if reported6 {
		out.IP6 = usableGlobalIPv6(jsonStr(raw6))
		out.ClearIP6 = true
	} else {
		if v := usableGlobalIPv6(agentPublicIP); v != "" {
			out.IP6 = v // 老 Agent 没有 ipv6 字段：退回探测到的公网 IPv6
		} else if v := usableGlobalIPv6(srv.LastIP6); v != "" {
			out.IP6 = v
		} else if strings.TrimSpace(srv.LastIP6) != "" {
			// 旧值本身是 WARP/非全球单播脏数据：即使本次没上报也要清掉
			out.ClearIP6 = true
		}
	}

	if out.PrivateIP == "" {
		out.PrivateIP = strings.TrimSpace(srv.LastIPPrivate)
	}
	if out.MeshIP4 == "" {
		out.MeshIP4 = strings.TrimSpace(srv.LastMeshIP4)
	}
	if out.MeshIP6 == "" {
		out.MeshIP6 = strings.TrimSpace(srv.LastMeshIP6)
	}
	return out
}
