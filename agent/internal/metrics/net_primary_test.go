package metrics

import (
	"net"
	"testing"
)

// 造一个网卡快照：name + 是否 up + 地址列表。
func view(name string, up bool, addrs ...string) ifaceView {
	flags := net.Flags(0)
	if up {
		flags = net.FlagUp
	}
	if name == "lo" {
		flags |= net.FlagLoopback
	}
	v := ifaceView{name: name, flags: flags}
	for _, s := range addrs {
		if ip := net.ParseIP(s); ip != nil {
			v.addrs = append(v.addrs, ip)
		}
	}
	return v
}

func TestIsMeshIfaceName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"eth0", false},
		{"ens5", false},
		{"lo", false},
		{"wlan0", false},
		{"CloudflareWARP", true},
		{"warp0", true},
		{"wgcf", true},
		{"tailscale0", true},
		{"wireguard0", true},
		{"wg0", true},
		{"wg1", true},
		{"", false},
	}
	for _, c := range cases {
		if got := isMeshIfaceName(c.name); got != c.want {
			t.Fatalf("isMeshIfaceName(%q)=%v，期望 %v", c.name, got, c.want)
		}
	}
}

// P1 回归：面板「IP / 主机」列的 IPv6 来自 poll 的 ipv6 字段，即 PrimaryIPv6。
// 装了 Cloudflare WARP 的机器上，WARP 网卡会分配 2606:4700:cf1:... 之类的组网
// IPv6；修复前按网卡枚举顺序返回第一个全球 IPv6，于是面板把 WARP 地址当主机 IP。
func TestPickPrimaryIPv6SkipsWARPNIC(t *testing.T) {
	cases := []struct {
		name  string
		views []ifaceView
		want  string
	}{
		{
			name: "真实网卡 + WARP 网卡",
			views: []ifaceView{
				view("eth0", true, "2401:d800:1:2::10"),
				view("warp0", true, "2606:4700:cf1:2000::1"),
			},
			want: "2401:d800:1:2::10",
		},
		{
			name: "WARP 网卡枚举在前",
			views: []ifaceView{
				view("CloudflareWARP", true, "2606:4700:cf1:2000::1", "172.16.0.2"),
				view("ens5", true, "2404:6800:4001::20"),
			},
			want: "2404:6800:4001::20",
		},
		{
			name: "只有 WARP 时必须返回空（不能回落成 WARP 地址）",
			views: []ifaceView{
				view("warp0", true, "2606:4700:cf1:2000::1"),
			},
			want: "",
		},
		{
			name: "网卡名不像 WARP，但地址落在 2606:4700::/32",
			views: []ifaceView{
				view("ppp0", true, "2606:4700:cf1:2000::1"),
			},
			want: "",
		},
		{
			name: "网卡名不像 WARP，但地址落在 2602:fa4f::/32",
			views: []ifaceView{
				view("ppp0", true, "2602:fa4f:1:2::9"),
			},
			want: "",
		},
		{
			name: "真实网卡只有链路本地，WARP 有全球地址 → 空",
			views: []ifaceView{
				view("eth0", true, "fe80::1"),
				view("wgcf", true, "2606:4700:cf1:2000::1"),
			},
			want: "",
		},
		{
			name: "真实网卡只有 ULA 时保留原有回落",
			views: []ifaceView{
				view("eth0", true, "fd12:3456::1"),
				view("warp0", true, "2606:4700:cf1:2000::1"),
			},
			want: "fd12:3456::1",
		},
		{
			name: "down 掉的网卡与回环不参与",
			views: []ifaceView{
				view("eth1", false, "2401:d800:1:2::99"),
				view("lo", true, "::1"),
			},
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickPrimaryIPv6(c.views); got != c.want {
				t.Fatalf("PrimaryIPv6=%q，期望 %q", got, c.want)
			}
		})
	}
}

// IPv4 同类问题：WARP 会分配 100.64/10（wgcf）或 172.16.0.2（MASQUE）组网地址，
// PrimaryIP 修复前可能把它当主机 IP 上报，服务端 publicIPv4 又不把 CGNAT 当私有，
// 于是 last_ip4 也会显示 WARP 地址。
func TestPickPrimaryIPSkipsWARPAndCGNAT(t *testing.T) {
	cases := []struct {
		name  string
		views []ifaceView
		want  string
	}{
		{
			name: "WARP 网卡在前，真实网卡在后",
			views: []ifaceView{
				view("warp0", true, "100.96.0.2", "2606:4700:cf1:2000::1"),
				view("eth0", true, "203.0.113.10"),
			},
			want: "203.0.113.10",
		},
		{
			name: "只有 WARP 时返回空",
			views: []ifaceView{
				view("CloudflareWARP", true, "100.96.0.2", "2606:4700:cf1:2000::1"),
			},
			want: "",
		},
		{
			name: "非 mesh 网卡上的 CGNAT 也不当作主机 IPv4",
			views: []ifaceView{
				view("eth0", true, "100.96.1.1"),
			},
			want: "",
		},
		{
			name: "真实网卡上的 RFC1918 保持原行为（内网 VPS 仍可上报）",
			views: []ifaceView{
				view("eth0", true, "172.16.3.4"),
			},
			want: "172.16.3.4",
		},
		{
			name: "没有 IPv4 时回落到真实网卡 IPv6，且不回落 WARP",
			views: []ifaceView{
				view("warp0", true, "2606:4700:cf1:2000::1"),
				view("eth0", true, "2401:d800:1:2::10"),
			},
			want: "2401:d800:1:2::10",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickPrimaryIP(c.views); got != c.want {
				t.Fatalf("PrimaryIP=%q，期望 %q", got, c.want)
			}
		})
	}
}

// pickIPv6 是公开语义的纯函数：WARP 地址段即使传进来也必须被丢弃。
func TestPickIPv6DropsWARPAddresses(t *testing.T) {
	cands := []net.IP{
		net.ParseIP("2606:4700:cf1:2000::1"),
		net.ParseIP("2602:fa4f:1:2::9"),
		net.ParseIP("2401:d800:1:2::10"),
	}
	if got := pickIPv6(cands); got != "2401:d800:1:2::10" {
		t.Fatalf("应跳过 WARP 段，得到 %q", got)
	}
	if got := pickIPv6(cands[:2]); got != "" {
		t.Fatalf("只有 WARP 段时应返回空串，得到 %q", got)
	}
}
