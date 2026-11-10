//go:build darwin

package main

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// macOS 的 DNS 与 IPv6 泄露防护。
//
// 现象（与 Windows 同源）：
//  1. route_darwin.go 只下发路由，从未改 DNS —— 系统仍用路由器/运营商 DNS，
//     域名解析全程暴露在本地网络；
//  2. TUN 只有 IPv4，物理网卡的公网 IPv6（240e::/2409:: 等）会绕过隧道直连。
//
// 处理：连接期间
//   - networksetup -setdnsservers "<服务>" 1.1.1.1 8.8.8.8
//   - networksetup -setv6off "<服务>"
// 断开时按标记文件还原（服务名 + 原 DNS + 原 IPv6 状态）。

func macMarkerPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "ppvpn", "mac-dns.json")
}

type macGuardState struct {
	Service string   `json:"service"`
	DNS     []string `json:"dns"`
	IPv6Off bool     `json:"ipv6_off"`
}

func runMac(args ...string) (string, error) {
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// activeMacService 返回承载默认路由的网络服务名（如 "Wi-Fi"）。
func activeMacService() string {
	// route -n get default → interface: en0
	out, err := runMac("route", "-n", "get", "default")
	if err != nil {
		return ""
	}
	iface := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "interface:") {
			iface = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "interface:"))
			break
		}
	}
	if iface == "" {
		return ""
	}
	// networksetup -listnetworkserviceorder 形如：
	//   (1) Wi-Fi
	//   (Hardware Port: Wi-Fi, Device: en0)
	order, err := runMac("networksetup", "-listnetworkserviceorder")
	if err != nil {
		return ""
	}
	lines := strings.Split(order, "\n")
	for i, line := range lines {
		if strings.Contains(line, "Device: "+iface+")") && i > 0 {
			prev := strings.TrimSpace(lines[i-1])
			if idx := strings.Index(prev, ")"); idx >= 0 {
				return strings.TrimSpace(prev[idx+1:])
			}
		}
	}
	return ""
}

// applyMacDNSGuard 设置 DNS 并关闭 IPv6。返回已生效的项。
func applyDNSGuard(dns []string) []string {
	service := activeMacService()
	if service == "" {
		log.Printf("DNS 防护（macOS）：找不到活动的网络服务，跳过")
		return nil
	}
	var applied []string

	// 记录原有配置（只在没有标记时记录，避免覆盖原始值）
	st := readMacGuardState()
	if st == nil {
		st = &macGuardState{Service: service}
		if out, err := runMac("networksetup", "-getdnsservers", service); err == nil {
			for _, line := range strings.Split(out, "\n") {
				v := strings.TrimSpace(line)
				if v == "" || strings.Contains(v, "aren't any") {
					continue
				}
				st.DNS = append(st.DNS, v)
			}
		}
		// 记录 IPv6 当前状态：networksetup -getinfo 里 "IPv6: Automatic"
		if out, err := runMac("networksetup", "-getinfo", service); err == nil {
			st.IPv6Off = strings.Contains(out, "IPv6: Off")
		}
		writeMacGuardState(st)
	}

	servers := make([]string, 0, len(dns))
	for _, d := range dns {
		if isIPv4(d) {
			servers = append(servers, d)
		}
	}
	if len(servers) == 0 {
		servers = []string{"1.1.1.1", "8.8.8.8"}
	}
	if out, err := runMac(append([]string{"networksetup", "-setdnsservers", service}, servers...)...); err != nil {
		log.Printf("DNS 防护（macOS）：设置 DNS 失败：%v %s", err, out)
	} else {
		applied = append(applied, "DNS:"+service)
		log.Printf("DNS 防护（macOS）：已将 %s 的 DNS 改为 %s", service, strings.Join(servers, ", "))
	}

	// IPv6 泄露：关闭该服务的 IPv6
	if out, err := runMac("networksetup", "-setv6off", service); err != nil {
		log.Printf("IPv6 防护（macOS）：关闭 IPv6 失败：%v %s", err, out)
	} else {
		applied = append(applied, "IPv6Off:"+service)
		log.Printf("IPv6 防护（macOS）：已关闭 %s 的 IPv6", service)
	}
	return applied
}

// revertMacDNSGuard 还原 DNS 与 IPv6。
func revertDNSGuard() {
	st := readMacGuardState()
	if st == nil {
		return
	}
	if st.Service != "" {
		if len(st.DNS) > 0 {
			args := append([]string{"networksetup", "-setdnsservers", st.Service}, st.DNS...)
			if out, err := runMac(args...); err != nil {
				log.Printf("DNS 防护（macOS）：恢复 DNS 失败：%v %s", err, out)
			}
		} else {
			if out, err := runMac("networksetup", "-setdnsservers", st.Service, "Empty"); err != nil {
				log.Printf("DNS 防护（macOS）：恢复 DNS（空）失败：%v %s", err, out)
			}
		}
		if !st.IPv6Off {
			if out, err := runMac("networksetup", "-setv6automatic", st.Service); err != nil {
				log.Printf("IPv6 防护（macOS）：恢复 IPv6 失败：%v %s", err, out)
			}
		}
		log.Printf("DNS/IPv6 防护（macOS）：已恢复 %s 的配置", st.Service)
	}
	_ = os.Remove(macMarkerPath())
}

// restoreMacGuardFromMarker 启动时恢复上次未还原的配置。
func restoreDNSGuardFromMarker() {
	if _, err := os.Stat(macMarkerPath()); err != nil {
		return
	}
	log.Printf("DNS 防护（macOS）：检测到上次未恢复的配置，正在还原")
	revertDNSGuard()
}

func readMacGuardState() *macGuardState {
	data, err := os.ReadFile(macMarkerPath())
	if err != nil {
		return nil
	}
	var st macGuardState
	if json.Unmarshal(data, &st) != nil {
		return nil
	}
	return &st
}

func writeMacGuardState(st *macGuardState) {
	path := macMarkerPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	raw, _ := json.MarshalIndent(st, "", "  ")
	_ = os.WriteFile(path, raw, 0o600)
}

