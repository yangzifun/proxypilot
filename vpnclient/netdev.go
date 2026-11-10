package main

import (
	"fmt"
	"net"
	"strings"
)

// PacketDevice 抽象各平台的虚拟网卡（Linux/macOS: /dev/net/tun；Windows: Wintun）。
type PacketDevice interface {
	// Name 返回系统里看到的网卡名（Windows 为适配器名称）。
	Name() string
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() error
}

// TunMode 是接管范围。
type TunMode string

const (
	// TunModeMesh 只把组网网段指到虚拟网卡，其余流量照旧（默认）。
	TunModeMesh TunMode = "mesh"
	// TunModeFull 接管全部 IPv4 流量（用 0.0.0.0/1 + 128.0.0.0/1 覆盖默认路由，
	// 保留原默认路由以便回滚，并给 VPN 服务器本身加主机路由避免环路）。
	TunModeFull TunMode = "full"
)

// TunSpec 描述一次网卡与路由的配置。
type TunSpec struct {
	Name     string // 网卡名，空则用默认
	IP       string // 分配给网卡的地址，例如 10.99.0.11
	CIDR     string // 组网网段，例如 10.99.0.0/16
	MTU      int    // 0 则用 1420
	// PrefixLen 是网卡地址的掩码位数（来自节点客户端子网）；0 则按 CIDR 推断
	PrefixLen int
	Mode     TunMode
	Endpoint string // VPN 服务器地址（host:port），full 模式下加主机路由防止流量回环
	// Gateway 是隧道对端（节点侧）地址，会写进网卡的默认网关。
	Gateway string
	// EndpointIPs 是 VPN 服务器已解析出的 IPv4 地址，必须绕过隧道直连。
	EndpointIPs []string
	// ControlIPs 是面板/控制面的 IPv4 地址，同样必须直连。
	// 客户端自己拉节点、续期会话、上报状态都走面板；若这些流量被塞进隧道，
	// 一旦隧道出问题客户端就再也连不上面板，只能手动断开重来。
	ControlIPs []string
	// DNS 是要写入该网卡的 DNS 服务器列表（默认 1.1.1.1 与 8.8.8.8）。
	DNS []string
	// KillSwitch 开启断线保护（默认关闭，需用户在设置页开启）。
	KillSwitch bool
}

// isIPv4 判断字符串是否是点分十进制 IPv4。
func isIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		n := 0
		if _, err := fmt.Sscanf(p, "%d", &n); err != nil || n < 0 || n > 255 {
			return false
		}
	}
	return true
}

func (s TunSpec) mtu() int {
	if s.MTU <= 0 {
		return 1420
	}
	if s.MTU < 576 {
		return 576
	}
	return s.MTU
}

func (s TunSpec) name() string {
	if strings.TrimSpace(s.Name) == "" {
		return defaultTunName()
	}
	return strings.TrimSpace(s.Name)
}

// prefixLen 返回网卡掩码位数：优先显式 PrefixLen，其次按 CIDR 推断。
func (s TunSpec) prefixLen() int {
	if s.PrefixLen > 0 && s.PrefixLen <= 32 {
		return s.PrefixLen
	}
	if _, n, err := net.ParseCIDR(strings.TrimSpace(s.CIDR)); err == nil {
		if ones, _ := n.Mask.Size(); ones > 0 {
			return ones
		}
	}
	return 24
}

// meshNetwork 规范化组网网段；解析失败返回空串。
func (s TunSpec) meshNetwork() string {
	if _, n, err := net.ParseCIDR(strings.TrimSpace(s.CIDR)); err == nil {
		return n.String()
	}
	return ""
}

// endpointHost 取端点里的主机部分（去掉端口）。
func (s TunSpec) endpointHost() string {
	host := strings.TrimSpace(s.Endpoint)
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// 默认下发到网卡的 DNS 服务器。
var defaultDNS = []string{"1.1.1.1", "8.8.8.8"}

// dnsList 返回要写入网卡的 DNS（未指定则用默认值）。
func (s TunSpec) dnsList() []string {
	if len(s.DNS) > 0 {
		return s.DNS
	}
	return defaultDNS
}

// gateway 返回网卡网关：优先显式指定，否则按网段取 .1。
func (s TunSpec) gateway() string {
	if gw := strings.TrimSpace(s.Gateway); gw != "" {
		return gw
	}
	if _, network, err := net.ParseCIDR(strings.TrimSpace(s.CIDR)); err == nil {
		ip := network.IP.To4()
		if ip != nil {
			gw := net.IPv4(ip[0], ip[1], ip[2], 1)
			return gw.String()
		}
	}
	return ""
}

// 接管类路由（full 模式）。
var fullModeRoutes = []string{"0.0.0.0/1", "128.0.0.0/1"}

// planRoutes 计算需要下发的路由列表（不含主机路由）。
//
// 全局模式（客户端默认）只接管公网：下发 0.0.0.0/1 与 128.0.0.0/1，
// 不再附带组网网段 —— 客户端不显示也不访问组网网络。
func planRoutes(s TunSpec) []string {
	if s.Mode == TunModeMesh {
		if net := s.meshNetwork(); net != "" {
			return []string{net}
		}
		return nil
	}
	return append([]string{}, fullModeRoutes...)
}

// TunResult 是一次接管的结果，供界面展示与断开时清理。
type TunResult struct {
	Adapter string   // 系统网卡名
	IP      string   // 已配置地址
	MTU     int      // 已配置 MTU
	Mode    TunMode  // 接管模式
	Routes  []string // 已下发路由
	Gateway string   // 已配置网关
	DNS     string   // 已写入的 DNS（空=未改）
	// DNSGuard 记录 DNS 泄露防护已生效的项（NRPT / 防火墙规则）
	DNSGuard []string
	// IPv6Disabled 记录被临时关闭 IPv6 的物理网卡（断开时恢复）
	IPv6Disabled []string
	// KillSwitchRules 记录断线保护下发过的防火墙规则（断开时删除）
	KillSwitchRules []string
}

func (r TunResult) Summary() string {
	if r.Adapter == "" {
		return "未接管"
	}
	return fmt.Sprintf("%s（%s · 网关 %s · %s · MTU %d · 路由 %d 条）", r.Adapter, r.IP, r.Gateway, r.Mode, r.MTU, len(r.Routes))
}
// endpointIPs 返回需要直连的服务器 IPv4 地址：优先用调用方解析好的列表，
// 否则从 Endpoint 里取（仅当它是 IP）。
func (s TunSpec) endpointIPs() []string {
	out := []string{}
	seen := map[string]bool{}
	// 节点端点 + 面板控制面，两者都要直连
	for _, ip := range append(append([]string{}, s.EndpointIPs...), s.ControlIPs...) {
		ip = strings.TrimSpace(ip)
		if v4 := net.ParseIP(ip); v4 != nil && v4.To4() != nil && !seen[ip] {
			seen[ip] = true
			out = append(out, ip)
		}
	}
	if len(out) == 0 {
		if h := s.endpointHost(); net.ParseIP(h) != nil && net.ParseIP(h).To4() != nil {
			out = append(out, h)
		}
	}
	return out
}
// resolveEndpointIPs 解析 VPN 服务器地址为 IPv4 列表（host 也可直接是 IP）。
func resolveEndpointIPs(endpoint string) []string {
	host := strings.TrimSpace(endpoint)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return []string{v4.String()}
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil
	}
	out := []string{}
	seen := map[string]bool{}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil && !seen[v4.String()] {
			seen[v4.String()] = true
			out = append(out, v4.String())
		}
	}
	return out
}