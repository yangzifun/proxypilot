//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// IPv6 泄露防护。
//
// 现象：TUN 只配了 IPv4，接管路由也只有 0.0.0.0/1 与 128.0.0.0/1。
// 一旦物理网卡有公网 IPv6（如电信 240e::/20）与默认路由 ::/0，
// 浏览器访问支持 IPv6 的站点时会优先走 v6 —— 真实 IPv6 直接暴露，
// 隧道形同虚设（实测 curl -6 ipv6.icanhazip.com 返回家宽 v6）。
//
// 处理：连接期间禁用物理网卡的 ms_tcpip6 绑定（fail-closed），断开时恢复。
// 崩溃兜底：禁用前把网卡列表写入标记文件，下次启动先恢复再运行。

func ipv6MarkerPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "ppvpn", "ipv6-disabled.json")
}

// disablePhysicalIPv6 关闭所有物理网卡的 IPv6 绑定，返回被关闭的网卡名。
func disablePhysicalIPv6() []string {
	adapters := physicalAdapters()
	if len(adapters) == 0 {
		return nil
	}
	// 先写标记，保证崩溃后能恢复
	if err := writeIPv6Marker(adapters); err != nil {
		log.Printf("IPv6 防护：写标记失败（仍继续）：%v", err)
	}

	var disabled []string
	for _, alias := range adapters {
		ps := fmt.Sprintf(
			`Disable-NetAdapterBinding -Name '%s' -ComponentID ms_tcpip6 -ErrorAction Stop`,
			alias)
		if out, err := runWin("powershell", "-NoProfile", "-NonInteractive", "-Command", ps); err != nil {
			log.Printf("IPv6 防护：禁用网卡 %s 的 IPv6 失败：%v %s",
				alias, err, strings.TrimSpace(out))
			continue
		}
		disabled = append(disabled, alias)
		log.Printf("IPv6 防护：已禁用网卡 %s 的 IPv6（防止直连泄露）", alias)
	}

	// 校验：仍能解析到公网 v6 说明没关干净
	if left := physicalIPv6Addrs(); len(left) > 0 {
		log.Printf("IPv6 防护：仍有公网 IPv6 地址残留：%v", left)
	}
	if len(disabled) > 0 {
		writeIPv6Marker(disabled)
	}
	return disabled
}

// restoreIPv6 恢复被我们关闭的网卡 IPv6。
func restoreIPv6(adapters []string) {
	for _, alias := range adapters {
		ps := fmt.Sprintf(
			`Enable-NetAdapterBinding -Name '%s' -ComponentID ms_tcpip6 -ErrorAction SilentlyContinue`,
			alias)
		if out, err := runWin("powershell", "-NoProfile", "-NonInteractive", "-Command", ps); err != nil {
			log.Printf("IPv6 防护：恢复网卡 %s 失败：%v %s", alias, err, strings.TrimSpace(out))
			continue
		}
		log.Printf("IPv6 防护：已恢复网卡 %s 的 IPv6", alias)
	}
	removeIPv6Marker()
}

// restoreIPv6FromMarker 启动时调用：上次异常退出可能把 IPv6 关了，先恢复。
func restoreIPv6FromMarker() {
	data, err := os.ReadFile(ipv6MarkerPath())
	if err != nil {
		return
	}
	var adapters []string
	if err := json.Unmarshal(data, &adapters); err != nil || len(adapters) == 0 {
		removeIPv6Marker()
		return
	}
	log.Printf("IPv6 防护：检测到上次未恢复的网卡 %v，正在恢复", adapters)
	restoreIPv6(adapters)
}

func writeIPv6Marker(adapters []string) error {
	path := ipv6MarkerPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, _ := json.Marshal(adapters)
	return os.WriteFile(path, raw, 0o600)
}

func removeIPv6Marker() {
	_ = os.Remove(ipv6MarkerPath())
}

// physicalIPv6Addrs 列出物理网卡上的公网 IPv6 地址（排除本地链路）。
func physicalIPv6Addrs() []string {
	out, err := runWin("powershell", "-NoProfile", "-NonInteractive", "-Command",
		`Get-NetIPAddress -AddressFamily IPv6 -ErrorAction SilentlyContinue |
		 Where-Object { $_.PrefixOrigin -ne 'WellKnown' -and $_.IPAddress -notlike 'fe80*' -and $_.IPAddress -ne '::1' } |
		 Select-Object -ExpandProperty IPAddress`)
	if err != nil {
		return nil
	}
	var list []string
	for _, line := range strings.Split(out, "\n") {
		if ip := strings.TrimSpace(line); ip != "" {
			list = append(list, ip)
		}
	}
	return list
}