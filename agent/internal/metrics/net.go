package metrics

import (
	"net"
	"strconv"
	"strings"
)

// NetStats 网卡累计流量（/proc/net/dev 汇总）。
// 排除回环与常见虚拟接口（veth / br- / docker / virbr / cni / flannel / kube-），
// 避免容器与网桥把同一份流量重复计入；其余接口（含 wg* 等真实隧道）如实统计。
type NetStats struct {
	RxBytes   int64 `json:"rx_bytes"`
	TxBytes   int64 `json:"tx_bytes"`
	RxPackets int64 `json:"rx_packets"`
	TxPackets int64 `json:"tx_packets"`
	RxErrs    int64 `json:"rx_errs"`
	TxErrs    int64 `json:"tx_errs"`
	Ifaces    int   `json:"ifaces"`
}

// isVirtualIface 判断是否应排除的接口（虚拟 / 网桥 / 回环）。
func isVirtualIface(name string) bool {
	if name == "" || name == "lo" {
		return true
	}
	for _, prefix := range []string{"veth", "br-", "docker", "virbr", "cni", "flannel", "kube-"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// ParseProcNetDev 解析 /proc/net/dev（前两行为表头）。
// 每行形如：
//
//	eth0: 1234567 8901 0 0 0 0 0 0 7654321 6789 0 0 0 0 0 0
//	       rx_bytes rx_packets rx_errs rx_drop ... tx_bytes tx_packets tx_errs tx_drop ...
func ParseProcNetDev(text string) NetStats {
	out := NetStats{}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i < 2 { // 两行表头
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		if isVirtualIface(name) {
			continue
		}
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 16 {
			continue
		}
		// 接收：bytes packets errs drop fifo frame compressed multicast
		// 发送：bytes packets errs drop fifo colls carrier compressed
		out.RxBytes += parseCounter(fields[0])
		out.RxPackets += parseCounter(fields[1])
		out.RxErrs += parseCounter(fields[2])
		out.TxBytes += parseCounter(fields[8])
		out.TxPackets += parseCounter(fields[9])
		out.TxErrs += parseCounter(fields[10])
		out.Ifaces++
	}
	return out
}

// parseCounter 解析无符号计数器（非法值按 0 计）。
func parseCounter(v string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// sumNet 合并多个 NetStats（多来源统计用，如 tcp/tcp6）。
func sumNet(list ...NetStats) NetStats {
	out := NetStats{}
	for _, n := range list {
		out.RxBytes += n.RxBytes
		out.TxBytes += n.TxBytes
		out.RxPackets += n.RxPackets
		out.TxPackets += n.TxPackets
		out.RxErrs += n.RxErrs
		out.TxErrs += n.TxErrs
		out.Ifaces += n.Ifaces
	}
	return out
}

// PrimaryIP 返回本机默认路由出口网卡上的全局单播地址（IPv4 优先）。
// 用途：Agent 走直连/非 Cloudflare 链路时，Worker 拿不到 cf-connecting-ip，
// 由 Agent 自己上报一个可用地址，后台监控就不会显示「未上报 IP」。
//
// 这不是「公网出口 IP」的探测，只读本机网卡，所以必须排除 WARP/组网网卡：
// 否则节点装上 Cloudflare WARP 后，这里会把组网地址（IPv4 100.64/10 CGNAT、
// IPv6 2606:4700::/32）当成主机 IP 上报，面板「IP / 主机」列就会显示 WARP 地址。
func PrimaryIP() string {
	return pickPrimaryIP(snapshotIfaces())
}

// PrimaryIPv6 返回本机真实网卡上的 IPv6（V11.25）。
// 跳过 loopback / 链路本地 / 未指定 / IPv4 映射地址，并排除 WARP/组网网卡
// 及其 2606:4700::/32 等 WARP 地址段；公网 2000::/3 优先，
// 没有公网时回落到真实网卡上的 ULA（fc00::/7）；都没有则返回空串。
//
// 绝不回落到 WARP 地址：那会让面板把组网 IPv6 显示成「主机 IP」。
func PrimaryIPv6() string {
	return pickPrimaryIPv6(snapshotIfaces())
}

// ifaceView 是网卡枚举的可测试快照。
// net.Interface.Addrs 只能在真实系统上调用，单测无法伪造，所以抽成这个视图。
type ifaceView struct {
	name  string
	flags net.Flags
	addrs []net.IP
}

// snapshotIfaces 枚举本机网卡与地址。
func snapshotIfaces() []ifaceView {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]ifaceView, 0, len(ifaces))
	for _, it := range ifaces {
		v := ifaceView{name: it.Name, flags: it.Flags}
		if addrs, err := it.Addrs(); err == nil {
			for _, a := range addrs {
				switch x := a.(type) {
				case *net.IPNet:
					v.addrs = append(v.addrs, x.IP)
				case *net.IPAddr:
					v.addrs = append(v.addrs, x.IP)
				}
			}
		}
		out = append(out, v)
	}
	return out
}

// meshIfaceNameHints 是 WARP / 组网隧道网卡的名称关键字（小写包含匹配）。
// 与 agent 包 netinfo.go 的 meshIfaceHints 保持同一口径（两个包各有用途，故各留一份）。
var meshIfaceNameHints = []string{"cloudflarewarp", "warp", "wgcf", "tailscale", "wireguard"}

// isMeshIfaceName 判断网卡是否为 WARP/组网隧道网卡。
// 除关键字外，还兼容 wireguard 的默认命名 wg0 / wg1 等。
func isMeshIfaceName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	for _, hint := range meshIfaceNameHints {
		if strings.Contains(n, hint) {
			return true
		}
	}
	if len(n) >= 3 && strings.HasPrefix(n, "wg") && n[2] >= '0' && n[2] <= '9' {
		return true
	}
	return false
}

// warpIPv6CIDRs 是 Cloudflare WARP 的 IPv6 段。
//
// 2606:4700::/32 是 WARP 设备地址/网关所在的主段；2602:fa4f::/32 是现场
// 面板「主机 IP」列里出现过的 WARP 段。网卡名被改得不像 WARP 时，用地址段兜底。
var warpIPv6CIDRs = []string{
	"2606:4700::/32",
	"2602:fa4f::/32",
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

// pickPrimaryIP 从网卡快照里挑 IPv4（IPv4 优先，没有时回落到 IPv6）。
func pickPrimaryIP(views []ifaceView) string {
	var v6 string
	for _, v := range views {
		if v.flags&net.FlagUp == 0 || v.flags&net.FlagLoopback != 0 {
			continue
		}
		// WARP/组网网卡上的地址不是主机地址，整条网卡跳过
		if isMeshIfaceName(v.name) {
			continue
		}
		for _, ip := range v.addrs {
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
				ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalMulticast() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				if isCGNATIPv4(ip4) { // WARP 组网地址，不是主机 IP
					continue
				}
				return ip4.String()
			}
			if v6 == "" && !isWARPIPv6(ip) {
				v6 = ip.String()
			}
		}
	}
	return v6
}

// pickPrimaryIPv6 从网卡快照里挑真实网卡的 IPv6。
func pickPrimaryIPv6(views []ifaceView) string {
	var cands []net.IP
	for _, v := range views {
		if v.flags&net.FlagUp == 0 || v.flags&net.FlagLoopback != 0 {
			continue
		}
		if isMeshIfaceName(v.name) {
			continue
		}
		cands = append(cands, v.addrs...)
	}
	return pickIPv6(cands)
}

// pickIPv6 从候选地址里挑一个：公网优先，其次 ULA，都没有返回空串。
// 抽成纯函数方便单测（不依赖真实网卡）。
// WARP 地址（2606:4700::/32 等）一律跳过，宁可返回空串也不冒充主机 IPv6。
func pickIPv6(cands []net.IP) string {
	var ula string
	for _, ip := range cands {
		if ip == nil || ip.To4() != nil {
			continue
		}
		ip6 := ip.To16()
		if ip6 == nil || ip6.IsUnspecified() || ip6.IsLoopback() || ip6.IsLinkLocalUnicast() || ip6.IsLinkLocalMulticast() {
			continue
		}
		if isWARPIPv6(ip6) {
			continue
		}
		if ip6[0]&0xfe == 0xfc { // fc00::/7 唯一本地地址，可用但不优先
			if ula == "" {
				ula = ip6.String()
			}
			continue
		}
		return ip6.String()
	}
	return ula
}
