//go:build windows

package main

// installFirewallRules 为客户端程序添加 Windows 防火墙放行规则（出/入站 + ICMP）。
// Wintun 注入的报文会被防火墙按该网卡处理，放行后能避免部分环境下丢包。
func installFirewallRules(exe string) error {
	name := "ProxyPilot VPN"
	rules := [][]string{
		{"netsh", "advfirewall", "firewall", "delete", "rule", "name=" + name},
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