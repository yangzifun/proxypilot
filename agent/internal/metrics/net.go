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
func PrimaryIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var v6 string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
			if v6 == "" {
				v6 = ip.String()
			}
		}
	}
	return v6
}

// PrimaryIPv6 返回本机第一个可用的全局 IPv6（V11.25）。
// 跳过 loopback / 链路本地 / 未指定 / IPv4 映射地址；公网 2000::/3 优先，
// 没有公网时回落到 ULA（fc00::/7），两者都没有则返回空串。
func PrimaryIPv6() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var cands []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil {
				cands = append(cands, ip)
			}
		}
	}
	return pickIPv6(cands)
}

// pickIPv6 从候选地址里挑一个：公网优先，其次 ULA，都没有返回空串。
// 抽成纯函数方便单测（不依赖真实网卡）。
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
