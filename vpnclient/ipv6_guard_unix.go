//go:build !windows && !linux

package main

// 非 Windows、非 Linux 平台（macOS）的 IPv6 防护占位。
//
// macOS 的 IPv6 由 dns_guard_darwin.go 里的 networksetup -setv6off 处理，
// 这里的 disablePhysicalIPv6 保持空实现（状态行由 DNS/IPv6 守卫汇总展示）。

func disablePhysicalIPv6() []string { return nil }

func restoreIPv6(adapters []string) {}

func restoreIPv6FromMarker() {}

func physicalIPv6Addrs() []string { return nil }