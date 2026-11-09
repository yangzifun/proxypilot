//go:build !windows

package main

// installFirewallRules 非 Windows 平台无需配置。
func installFirewallRules(exe string) error { return nil }