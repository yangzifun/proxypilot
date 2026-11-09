//go:build windows

package main

import "log"

// cleanStaleTunnel 清理上次异常退出（崩溃/强杀/旧版本）残留的网卡地址与路由。
//
// 旧版本会把「默认网关 10.88.1.1」写进永久路由，一旦客户端没起来，
// 系统所有流量都会被送到一个不存在的隧道网关 → 全网上不去。
// 每次启动都先把我们自己的适配器恢复干净，再重新接管。
func cleanStaleTunnel() {
	// 先撤销上次崩溃可能残留的 DNS 守卫（NRPT / 防火墙规则），
	// 否则用户会表现为「能上网但所有域名解析失败」。
	revertDNSGuard()

	name := defaultTunName()
	if name == "" {
		return
	}
	if _, ok := adapterStatus(); !ok {
		return
	}
	// 1) 地址恢复 DHCP（同时移除持久化的网关与 0.0.0.0/0 永久路由）
	runWinSilent("netsh", "interface", "ipv4", "set", "address", "name="+name, "source=dhcp")

	// 2) 删除我们可能残留的路由（按适配器名匹配，不影响其他网卡）
	for _, cidr := range []string{"0.0.0.0/1", "128.0.0.0/1", "10.99.0.0/16", "10.0.0.0/8"} {
		runWinSilent("netsh", "interface", "ipv4", "delete", "route", cidr, name)
	}
	log.Printf("已清理隧道适配器残留配置：%s", name)
}

// runWinSilent 执行命令并忽略结果。
func runWinSilent(args ...string) {
	_, _ = runWin(append([]string{}, args...)...)
}