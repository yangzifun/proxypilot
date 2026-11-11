package node

import "testing"

// DNS 必须走可靠流的判定，是「DATAGRAM 81% 丢包」这个实测结论的固化。
// 判定错了会直接让 nslookup 恢复成连续超时，所以这里锁住边界。
func TestIsDNSPacket(t *testing.T) {
	// 造一个 IPv4/UDP 报文，指定源/目的端口
	mk := func(sport, dport uint16) []byte {
		p := make([]byte, 28)
		p[0] = 0x45
		p[9] = 17
		p[20] = byte(sport >> 8)
		p[21] = byte(sport)
		p[22] = byte(dport >> 8)
		p[23] = byte(dport)
		return p
	}

	cases := []struct {
		name string
		pkt  []byte
		want bool
	}{
		{"查询：dst=53", mk(50000, 53), true},
		{"应答：src=53", mk(53, 50000), true},
		{"普通 UDP", mk(50000, 443), false},
		{"同端口号但非 53", mk(5353, 9999), false},
		{"TCP 报文", func() []byte {
			p := mk(50000, 53)
			p[9] = 6 // TCP
			return p
		}(), false},
		{"过短", make([]byte, 10), false},
	}

	for _, c := range cases {
		if got := isDNSPacket(c.pkt); got != c.want {
			t.Fatalf("%s: isDNSPacket=%v，期望 %v", c.name, got, c.want)
		}
	}
}
