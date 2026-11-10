//go:build !windows

package main

// cleanStaleTunnel 非 Windows 平台由系统路由表自然回收；
// 但 DNS/IPv6 守卫可能是上次崩溃遗留，需要还原。
func cleanStaleTunnel() {
	restoreDNSGuardFromMarker()
}