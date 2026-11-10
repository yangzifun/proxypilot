//go:build windows

package main

// installFirewallRules 为客户端程序添加 Windows 防火墙放行规则（出/入站 + ICMP）。
// Wintun 注入的报文会被防火墙按该网卡处理，放行后能避免部分环境下丢包。
func installFirewallRules(exe string) error {
	name := "ProxyPilot VPN"
	// 三条规则各有名字，必须逐个删干净再建。
	// 以前只删了 name 本身，"(out)" 与 "ICMP" 从来不清，于是每次安装/升级都
	// 多堆一组 —— 实测一台机器上堆了 23 组重复规则。
	rules := [][]string{
		{"netsh", "advfirewall", "firewall", "delete", "rule", "name=" + name},
		{"netsh", "advfirewall", "firewall", "delete", "rule", "name=" + name + " (out)"},
		{"netsh", "advfirewall", "firewall", "delete", "rule", "name=" + name + " ICMP"},
		{"netsh", "advfirewall", "firewall", "add", "rule", "name=" + name,
			"dir=in", "action=allow", "program=" + exe, "enable=yes", "profile=any"},
		{"netsh", "advfirewall", "firewall", "add", "rule", "name=" + name + " (out)",
			"dir=out", "action=allow", "program=" + exe, "enable=yes", "profile=any"},
		{"netsh", "advfirewall", "firewall", "add", "rule", "name=" + name + " ICMP",
			"dir=in", "action=allow", "protocol=icmpv4:8,any", "profile=any"},
	}
	for _, r := range rules {
		_, _ = runWin(append([]string{}, r...)...)
	}
	return nil
}