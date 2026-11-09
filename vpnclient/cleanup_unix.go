//go:build !windows

package main

// cleanStaleTunnel 非 Windows 平台由系统路由表自然回收。
func cleanStaleTunnel() {}