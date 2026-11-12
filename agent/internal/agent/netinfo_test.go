package agent

import "testing"

// WARP/组网网卡识别：这些网卡上的地址不能当作主机地址上报。
func TestIsMeshIface(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"eth0", false},
		{"ens5", false},
		{"wlan0", false},
		{"lo", false},
		{"CloudflareWARP", true},
		{"warp0", true},
		{"wgcf", true},
		{"tailscale0", true},
		{"wireguard0", true},
		{"wg0", true},
		{"wg2", true},
		{"", false},
	}
	for _, c := range cases {
		if got := isMeshIface(c.name); got != c.want {
			t.Fatalf("isMeshIface(%q)=%v，期望 %v", c.name, got, c.want)
		}
	}
}
