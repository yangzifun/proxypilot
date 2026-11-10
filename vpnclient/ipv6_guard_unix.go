//go:build !windows

package main

// 非 Windows 平台的 IPv6 防护。
//
// Linux 可用 sysctl net.ipv6.conf.<if>.disable_ipv6，macOS 用 networksetup -setv6off，
// 但两者都会影响系统网络配置，需要各自做恢复标记，留到后续版本实现。
// 当前在 Linux/macOS 上不做处理（IPv6 泄露仍存在，客户端界面会显示「未启用」）。

func disablePhysicalIPv6() []string { return nil }

func restoreIPv6(adapters []string) {}

func restoreIPv6FromMarker() {}

func physicalIPv6Addrs() []string { return nil }