//go:build windows

package main

import (
	"log"
	"strings"
)

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

	// 1) 删除残留的接管路由（按前缀 + 掩码精确删除，不依赖上次记录）
	runWinSilent("route", "delete", "0.0.0.0", "mask", "128.0.0.0")
	runWinSilent("route", "delete", "128.0.0.0", "mask", "128.0.0.0")
	runWinSilent("route", "delete", "10.99.0.0", "mask", "255.255.0.0")
	for _, cidr := range []string{"0.0.0.0/1", "128.0.0.0/1", "10.99.0.0/16", "10.0.0.0/8"} {
		runWinSilent("netsh", "interface", "ipv4", "delete", "route", cidr, name)
	}

	// 2) 地址与 DNS 恢复 DHCP（同时移除持久化的默认网关）
	runWinSilent("netsh", "interface", "ipv4", "set", "address", "name="+name, "source=dhcp")
	runWinSilent("netsh", "interface", "ipv4", "set", "dnsservers", "name="+name, "source=dhcp")

	// 3) 若还留着 10.88.x / 10.99.x 地址，显式删掉
	for _, ip := range adapterIPv4(name) {
		if strings.HasPrefix(ip, "10.88.") || strings.HasPrefix(ip, "10.99.") {
			log.Printf("发现隧道适配器残留地址 %s，已删除", ip)
			runWinSilent("netsh", "interface", "ipv4", "delete", "address", "name="+name, "addr="+ip)
		}
	}
	log.Printf("已清理隧道适配器残留配置：%s", name)
}

// adapterIPv4 返回指定网卡上现有的 IPv4 地址。
func adapterIPv4(alias string) []string {
	out, err := runWin("netsh", "interface", "ipv4", "show", "addresses", "name="+alias)
	if err != nil {
		return nil
	}
	var list []string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		low := strings.ToLower(trimmed)
		if !strings.HasPrefix(low, "ip address") && !strings.HasPrefix(low, "ip 地址") {
			continue
		}
		// 形如 "IP Address:                           10.88.1.12"
		if i := strings.LastIndex(trimmed, ":"); i >= 0 {
			ip := strings.TrimSpace(trimmed[i+1:])
			if isIPv4(ip) {
				list = append(list, ip)
			}
		}
	}
	return list
}

// runWinSilent 执行命令并忽略结果。
func runWinSilent(args ...string) {
	_, _ = runWin(append([]string{}, args...)...)
}