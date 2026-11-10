package main

import (
	"strings"
	"testing"
)

// 探测必须同时覆盖 UDP DNS 与 TCP:443：
// 客户端自己的 DNS 守卫会封物理网卡 53，只靠 UDP 53 会出现
// 「网络其实能用，但界面显示不通」。
func TestProbeTargetsIncludeBothMethods(t *testing.T) {
	udp := probeTargets("10.88.2.1", "1.1.1.1:53")
	tcp := probeTCPTargets()

	if len(udp) < 3 {
		t.Fatalf("UDP 目标太少：%+v", udp)
	}
	if len(tcp) < 1 {
		t.Fatalf("缺少 TCP 目标：%+v", tcp)
	}
	for _, x := range tcp {
		if !strings.HasSuffix(x.Addr, ":443") {
			t.Fatalf("TCP 目标应为 :443（避开 53 端口封锁），实际 %q", x.Addr)
		}
	}
	// 网关不应排在第一位：节点上不一定有 DNS 监听，放前面只会白等一个超时
	if strings.Contains(udp[0].Name, "网关") {
		t.Fatalf("网关不应作为首个探测目标：%+v", udp)
	}
}

// 目标不能重复
func TestProbeTargetsNoDuplicate(t *testing.T) {
	for _, ts := range [][]probeTarget{probeTargets("10.88.2.1", "1.1.1.1:53"), probeTCPTargets()} {
		seen := map[string]bool{}
		for _, x := range ts {
			if seen[x.Addr] {
				t.Fatalf("目标重复：%s", x.Addr)
			}
			seen[x.Addr] = true
			if strings.TrimSpace(x.Addr) == "" {
				t.Fatal("出现空目标")
			}
		}
	}
}

// 没给网关时不应生成空地址
func TestProbeTargetsWithoutGateway(t *testing.T) {
	for _, x := range probeTargets("", "1.1.1.1:53") {
		if strings.HasPrefix(x.Addr, ":") || strings.TrimSpace(x.Addr) == "" {
			t.Fatalf("地址非法：%+v", x)
		}
	}
}