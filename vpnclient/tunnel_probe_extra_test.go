package main

import (
	"strings"
	"testing"
)

// 探测目标必须覆盖网关与多个公共 DNS：单目标失败无法判断是隧道不通
// 还是那个 DNS 被拦，之前就因此误报「不通」。
func TestProbeTargetsCoverage(t *testing.T) {
	ts := probeTargets("10.88.1.1", "1.1.1.1:53")
	if len(ts) < 4 {
		t.Fatalf("目标太少（%d）：%+v", len(ts), ts)
	}
	names := map[string]string{}
	for _, x := range ts {
		names[x.Name] = x.Addr
	}
	if got := names["节点网关"]; got != "10.88.1.1:53" {
		t.Fatalf("网关目标应为 10.88.1.1:53，实际 %q", got)
	}
	for _, want := range []string{"Cloudflare", "AliDNS", "Quad9"} {
		if names[want] == "" {
			t.Fatalf("缺少目标 %s", want)
		}
	}
	// 1.1.1.1:53 与自定义 DNS 相同，不应重复
	seen := map[string]int{}
	for _, x := range ts {
		seen[x.Addr]++
	}
	for addr, n := range seen {
		if n > 1 {
			t.Fatalf("目标 %s 重复 %d 次", addr, n)
		}
	}
}

// 没给网关时不应产生空目标
func TestProbeTargetsNoGateway(t *testing.T) {
	ts := probeTargets("", "1.1.1.1:53")
	for _, x := range ts {
		if strings.TrimSpace(x.Addr) == "" {
			t.Fatalf("出现空目标：%+v", ts)
		}
		if strings.HasPrefix(x.Addr, ":") {
			t.Fatalf("目标地址缺主机：%+v", ts)
		}
	}
	for _, x := range ts {
		if x.Name == "节点网关" {
			t.Fatalf("没给网关不应生成网关目标：%+v", ts)
		}
	}
}

func TestAtoiPort(t *testing.T) {
	cases := map[string]int{
		"53":    53,
		"443":   443,
		"":      53,
		"abc":   53,
		"0":     53,
		"70000": 53,
	}
	for in, want := range cases {
		if got := atoiPort(in, 53); got != want {
			t.Fatalf("atoiPort(%q)=%d，期望 %d", in, got, want)
		}
	}
}