//go:build !windows

package main

// 非 Windows 平台暂未实现断线保护（Linux 需 nftables/iptables 规则，macOS 需 pf）。
// 设置项仍然可见，但开启后不生效，界面会显示「未启用」。

func enableKillSwitch(endpoints []string, tunAlias string) ([]string, error) { return nil, nil }

func disableKillSwitch() {}

func restoreKillSwitchFromMarker() {}