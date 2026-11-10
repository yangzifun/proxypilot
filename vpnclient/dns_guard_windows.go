//go:build windows

package main

import (
	"fmt"
	"log"
	"strings"
)

// Windows DNS 泄露防护。
//
// 现象：只给虚拟网卡配 DNS（1.1.1.1 / 8.8.8.8）时，Windows 的
// 「智能多宿主名称解析」会并行向所有网卡（含物理网卡）的 DNS 发查询，
// 物理网卡的运营商 DNS（如 116.236.159.x 中国电信）先回包，
// 于是出现「出口在美国、DNS 却在国内」的泄露。
//
// 两层防护：
//  1. NRPT（名称解析策略表）：把所有域名强制走隧道 DNS，系统级生效；
//  2. 物理网卡出站 53 封禁：即使用户绕过 NRPT 的应用也发不出去。
// 断开时按名字精确清理，不影响系统原有 DNS 配置。

const dnsGuardTag = "ProxyPilot"

// applyDNSGuard 下发 NRPT 规则并封禁物理网卡的 DNS 出站。
func applyDNSGuard(dns []string) []string {
	servers := make([]string, 0, len(dns))
	for _, d := range dns {
		if isIPv4(d) {
			servers = append(servers, d)
		}
	}
	if len(servers) == 0 {
		servers = []string{"1.1.1.1", "8.8.8.8"}
	}
	applied := []string{}

	// 先清掉可能残留的规则，避免重复叠加
	revertDNSGuard()

	// 1) NRPT：命名空间 "." 表示所有域名
	ps := fmt.Sprintf(
		`Add-DnsClientNrptRule -Namespace '.' -NameServers %s -DisplayName '%s-VPN' -Comment '%s DNS guard' -ErrorAction Stop`,
		strings.Join(servers, ","), dnsGuardTag, dnsGuardTag)
	if out, err := runWin("powershell", "-NoProfile", "-NonInteractive", "-Command", ps); err != nil {
		log.Printf("DNS 守卫：下发 NRPT 规则失败：%v %s", err, strings.TrimSpace(out))
	} else {
		applied = append(applied, "NRPT")
		log.Printf("DNS 守卫：已强制所有域名走 %s", strings.Join(servers, ", "))
	}

	// 2) 物理网卡出站 53 封禁（UDP + TCP），防止应用绕过 NRPT
	adapters := physicalAdapters()
	for _, alias := range adapters {
		ok := true
		for _, proto := range []string{"UDP", "TCP"} {
			psAdd := fmt.Sprintf(
				`New-NetFirewallRule -DisplayName '%s-DNS-%s-%s' -Direction Outbound -Action Block -Protocol %s -RemotePort 53 -InterfaceAlias '%s' -Profile Any -ErrorAction Stop | Out-Null`,
				dnsGuardTag, alias, proto, proto, alias)
			if out, err := runWin("powershell", "-NoProfile", "-NonInteractive", "-Command", psAdd); err != nil {
				log.Printf("DNS 守卫：%s 网卡 %s 封禁失败：%v %s", alias, proto, err, strings.TrimSpace(out))
				ok = false
			}
		}
		if ok {
			applied = append(applied, "FW:"+alias)
			log.Printf("DNS 守卫：已封禁网卡 %s 的明文 DNS 出站", alias)
		}
	}
	return applied
}

// revertDNSGuard 撤销本客户端下发的 NRPT 规则与防火墙规则。
func revertDNSGuard() {
	psNrpt := fmt.Sprintf(
		`Get-DnsClientNrptRule -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -like '%s*' } | Remove-DnsClientNrptRule -Force -ErrorAction SilentlyContinue`,
		dnsGuardTag)
	if _, err := runWin("powershell", "-NoProfile", "-NonInteractive", "-Command", psNrpt); err != nil {
		log.Printf("DNS 守卫：清理 NRPT 失败：%v", err)
	}
	psFw := fmt.Sprintf(
		`Get-NetFirewallRule -DisplayName '%s-DNS-*' -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue`,
		dnsGuardTag)
	if _, err := runWin("powershell", "-NoProfile", "-NonInteractive", "-Command", psFw); err != nil {
		log.Printf("DNS 守卫：清理防火墙规则失败：%v", err)
	}
}

// physicalAdapters 返回处于 Up 状态的物理网卡别名（排除虚拟网卡）。
func physicalAdapters() []string {
	out, err := runWin("powershell", "-NoProfile", "-NonInteractive", "-Command",
		`Get-NetAdapter -Physical -ErrorAction SilentlyContinue | Where-Object { $_.Status -eq 'Up' } | Select-Object -ExpandProperty Name`)
	if err != nil {
		return nil
	}
	var list []string
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(strings.ToLower(name), "proxypilot") {
			continue
		}
		list = append(list, name)
	}
	return list
}
// restoreDNSGuardFromMarker 非 darwin 平台无需恢复（Windows 由 cleanStaleTunnel 处理）。
func restoreDNSGuardFromMarker() {}
