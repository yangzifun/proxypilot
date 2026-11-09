//go:build !windows

package main

// 非 Windows 平台：Linux 用 systemd-resolved 的 `resolvectl domain <if> ~.`
// 已经把全部域名绑定到隧道 DNS；macOS 依赖 scutil 的补充配置，这里先留空。
func applyDNSGuard(dns []string) []string { return nil }

func revertDNSGuard() {}
