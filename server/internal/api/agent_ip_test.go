package api

import (
	"net"
	"testing"
)

func TestUsablePublicIPv4(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"122.10.114.131", "122.10.114.131"},
		{"23.141.204.54", "23.141.204.54"},
		{"100.96.0.2", ""},             // CGNAT（WARP 组网）
		{"100.127.255.254", ""},        // CGNAT 上界
		{"100.128.0.1", "100.128.0.1"}, // 100.64/10 之外，是普通公网
		{"100.63.255.255", "100.63.255.255"},
		{"10.0.0.2", ""},    // RFC1918
		{"192.168.1.1", ""}, // RFC1918
		{"127.0.0.1", ""},   // 回环
		{"169.254.1.1", ""}, // 链路本地
		{"0.0.0.0", ""},     // 未指定
		{"2401::1", ""},     // IPv6
		{"not-an-ip", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := usablePublicIPv4(c.in); got != c.want {
			t.Fatalf("usablePublicIPv4(%q)=%q，期望 %q", c.in, got, c.want)
		}
	}
}

// P1 回归（task-15）：last_ip6 只接受真实网卡的全球单播 IPv6，WARP 段一律拒写。
func TestUsableGlobalIPv6RejectsWARP(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2401:2660:1000:1572:9b8c:d1be:7271:77dc", "2401:2660:1000:1572:9b8c:d1be:7271:77dc"},
		{"2404:8c80:85:1025::65", "2404:8c80:85:1025::65"},
		{"2a0e:97c0:7d4:ce20:daf5:8f7b:ed94:db08", "2a0e:97c0:7d4:ce20:daf5:8f7b:ed94:db08"},
		{"2606:4700:cf1:2000::1", ""},                 // WARP 主段
		{"2606:4700::1", ""},                          // WARP 主段（短写）
		{"2602:fa4f:a30:6d2d:26e4:992:e7fd:7501", ""}, // 现场实测 WARP 段
		{"fd12:3456::1", ""},                          // ULA
		{"fe80::1", ""},                               // 链路本地
		{"::1", ""},                                   // 回环
		{"::ffff:1.2.3.4", ""},                        // IPv4 映射
		{"122.10.114.131", ""},                        // IPv4
		{" 2401:2660::1 ", "2401:2660::1"},            // 首尾空白
		{"", ""},
	}
	for _, c := range cases {
		if got := usableGlobalIPv6(c.in); got != c.want {
			t.Fatalf("usableGlobalIPv6(%q)=%q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestIsWARPIPv6AndCGNAT(t *testing.T) {
	if !isWARPIPv6(mustIP("2606:4700:cf1:2000::1")) || !isWARPIPv6(mustIP("2602:fa4f::1")) {
		t.Fatal("WARP 段判定失败")
	}
	if isWARPIPv6(mustIP("2401:2660::1")) || isWARPIPv6(mustIP("2602:f3f2::1")) {
		t.Fatal("真实地址被误判为 WARP")
	}
	if !isCGNATIPv4(mustIP("100.64.0.1").To4()) || !isCGNATIPv4(mustIP("100.127.1.1").To4()) {
		t.Fatal("CGNAT 判定失败")
	}
	if isCGNATIPv4(mustIP("100.128.0.1").To4()) || isCGNATIPv4(mustIP("10.0.0.1").To4()) {
		t.Fatal("非 CGNAT 被误判")
	}
}

// P1 回归（task-15）：IP 列落库意图。
//   - Agent 1.1 上报 ipv6="" 表示没有真实 IPv6 → 必须清空历史值（不能被 COALESCE 永久保留）
//   - 上报 WARP 段 → 拒写并清空
//   - 字段缺失（老 Agent）→ 保留合法旧值，但旧值是 WARP 时仍要丢弃
//   - last_ip4 不接受 100.64/10（WARP 组网地址只进 last_mesh_ip4）
func TestResolveAgentIPs(t *testing.T) {
	const (
		warp6  = "2606:4700:cf1:2000::1"
		warp62 = "2602:fa4f:a30:6d2d:26e4:992:e7fd:7501"
		real6  = "2401:2660:1000:1572:9b8c:d1be:7271:77dc"
		real4  = "122.10.114.131"
	)
	cases := []struct {
		name       string
		body       map[string]any
		connIP     string
		lastIP4    string
		lastIP6    string
		wantIP4    string
		wantClear4 bool
		wantIP6    string
		wantClear6 bool
		wantMesh4  string
		wantMesh6  string
	}{
		{
			name: "Agent1.1 上报空串：清空历史 WARP IPv6",
			body: map[string]any{"ipv6": ""}, lastIP6: warp6,
			wantClear6: true,
		},
		{
			name: "上报 WARP 段：拒写并清空",
			body: map[string]any{"ipv6": warp62}, lastIP6: warp6,
			wantClear6: true,
		},
		{
			name: "上报真实 IPv6：写入并覆盖",
			body: map[string]any{"ipv6": real6}, lastIP6: warp6,
			wantIP6: real6, wantClear6: true,
		},
		{
			name: "字段缺失（老 Agent）：保留合法旧值",
			body: map[string]any{}, lastIP6: real6,
			wantIP6: real6,
		},
		{
			name: "字段缺失且旧值是 WARP：丢弃",
			body: map[string]any{}, lastIP6: warp6,
			wantIP6: "", wantClear6: true,
		},
		{
			name: "字段缺失但有公网探测 IPv6：用探测值",
			body: map[string]any{"public_ip": "2a0e:97c0:7d4:ce20:daf5:8f7b:ed94:db08"}, lastIP6: warp6,
			wantIP6: "2a0e:97c0:7d4:ce20:daf5:8f7b:ed94:db08",
		},
		{
			name: "公网探测的是 WARP 出口 IPv6：拒写并清空旧 WARP",
			body: map[string]any{"public_ip": "2606:4700::1234"}, lastIP6: warp6,
			wantIP6: "", wantClear6: true,
		},
		{
			name:   "IPv4：WARP CGNAT 不进 last_ip4，旧 CGNAT 被清空",
			body:   map[string]any{"ip": "100.96.0.2", "public_ip": "100.96.0.2"},
			connIP: "100.96.0.2", lastIP4: "100.96.0.18",
			wantIP4: "", wantClear4: true,
		},
		{
			name:   "IPv4：合法公网写入，连接来源 CGNAT 被忽略",
			body:   map[string]any{"public_ip": real4},
			connIP: "100.96.0.2", lastIP4: "100.96.0.18",
			wantIP4: real4,
		},
		{
			name: "IPv4：无新值且旧值合法时保留",
			body: map[string]any{}, lastIP4: "23.141.204.54",
			wantIP4: "23.141.204.54",
		},
		{
			name: "组网地址只进 mesh 列，不污染主机列",
			body: map[string]any{
				"mesh_ip4": "100.96.0.2", "mesh_ip6": warp6,
				"ipv6": "", "ip": "100.96.0.2",
			},
			lastIP4: "100.96.0.18", lastIP6: warp6,
			wantIP4: "", wantClear4: true, wantIP6: "", wantClear6: true,
			wantMesh4: "100.96.0.2", wantMesh6: warp6,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := &agentServer{LastIP4: c.lastIP4, LastIP6: c.lastIP6}
			got := resolveAgentIPs(c.body, c.connIP, srv)
			if got.IP4 != c.wantIP4 || got.ClearIP4 != c.wantClear4 {
				t.Fatalf("IP4=%q clear=%v，期望 %q/%v", got.IP4, got.ClearIP4, c.wantIP4, c.wantClear4)
			}
			if got.IP6 != c.wantIP6 || got.ClearIP6 != c.wantClear6 {
				t.Fatalf("IP6=%q clear=%v，期望 %q/%v", got.IP6, got.ClearIP6, c.wantIP6, c.wantClear6)
			}
			if c.wantMesh4 != "" && got.MeshIP4 != c.wantMesh4 {
				t.Fatalf("MeshIP4=%q，期望 %q", got.MeshIP4, c.wantMesh4)
			}
			if c.wantMesh6 != "" && got.MeshIP6 != c.wantMesh6 {
				t.Fatalf("MeshIP6=%q，期望 %q", got.MeshIP6, c.wantMesh6)
			}
		})
	}
}

// 展示兜底：历史 WARP 地址即使还在库里也不展示。
func TestDisplayIPv6(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2401:2660::1", "2401:2660::1"},
		{"2606:4700:cf1:2000::1", ""},
		{"2602:fa4f:1:2::3", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := displayIPv6(c.in); got != c.want {
			t.Fatalf("displayIPv6(%q)=%q，期望 %q", c.in, got, c.want)
		}
	}
}

// 展示兜底：last_ip4 是 CGNAT/私网时用连接来源公网 IPv4 顶替，顶替不到就隐藏。
func TestPublicDisplayIPv4SkipsMesh(t *testing.T) {
	cases := []struct{ v4, conn, want string }{
		{"100.96.0.18", "122.10.114.131", "122.10.114.131"}, // WARP v4 → 连接来源
		{"100.96.0.18", "", ""},                             // 顶替不到 → 隐藏
		{"100.96.0.18", "100.96.0.2", ""},                   // 连接来源也是 CGNAT → 隐藏
		{"122.10.114.131", "100.96.0.2", "122.10.114.131"},  // 本身合法 → 原样
	}
	for _, c := range cases {
		if got := publicDisplayIPv4(c.v4, c.conn); got != c.want {
			t.Fatalf("publicDisplayIPv4(%q,%q)=%q，期望 %q", c.v4, c.conn, got, c.want)
		}
	}
}

func mustIP(s string) net.IP { return net.ParseIP(s) }
