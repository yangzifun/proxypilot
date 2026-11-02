package metrics

import (
	"net"
	"testing"
)

// V11.25：/api/agent/poll 增加上报本机 IPv6，这里锁定挑选规则。

func TestPickIPv6PrefersPublic(t *testing.T) {
	cands := []net.IP{
		net.ParseIP("127.0.0.1"),
		net.ParseIP("192.168.1.5"),
		net.ParseIP("fe80::1"),           // 链路本地，跳过
		net.ParseIP("fd00::1"),           // ULA，可回退但不优先
		net.ParseIP("2408:8207:1234::1"), // 公网
	}
	if got := pickIPv6(cands); got != "2408:8207:1234::1" {
		t.Fatalf("应优先公网 IPv6，得到 %q", got)
	}
}

func TestPickIPv6FallsBackToULA(t *testing.T) {
	cands := []net.IP{net.ParseIP("fe80::1"), net.ParseIP("fd12:3456::1")}
	if got := pickIPv6(cands); got != "fd12:3456::1" {
		t.Fatalf("没有公网时应回落到 ULA，得到 %q", got)
	}
}

func TestPickIPv6EmptyWhenNothingUsable(t *testing.T) {
	cands := []net.IP{
		nil,
		net.ParseIP("10.0.0.1"),
		net.ParseIP("::ffff:1.2.3.4"), // IPv4 映射地址
		net.ParseIP("fe80::abcd"),
		net.ParseIP("::"),
	}
	if got := pickIPv6(cands); got != "" {
		t.Fatalf("只有 v4 / 链路本地 / 未指定时应返回空串，得到 %q", got)
	}
}
