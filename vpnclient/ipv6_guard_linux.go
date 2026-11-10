//go:build linux

package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Linux 的 IPv6 泄露防护（与 Windows/macOS 同源问题）：
// TUN 只有 IPv4，物理网卡的公网 IPv6 与默认路由 ::/0 会让流量绕过隧道直连。
//
// 处理：连接期间把物理网卡的 net.ipv6.conf.<if>.disable_ipv6 置 1，
// 记录原值，断开/启动时按标记恢复。

func linuxIPv6MarkerPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "ppvpn", "linux-ipv6.json")
}

type linuxIPv6State struct {
	Values map[string]string `json:"values"` // iface -> 原值（"0"/"1"）
}

// linuxPhysicalIfaces 列出有物理设备的网卡（排除 lo/tun/veth/docker 等）。
func linuxPhysicalIfaces() []string {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if name == "lo" {
			continue
		}
		if strings.HasPrefix(name, "tun") || strings.HasPrefix(name, "tap") ||
			strings.HasPrefix(name, "ppvpn") || strings.HasPrefix(name, "ppmesh") ||
			strings.HasPrefix(name, "wg") || strings.HasPrefix(name, "veth") ||
			strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") ||
			strings.HasPrefix(name, "virbr") || strings.HasPrefix(name, "cni") ||
			strings.HasPrefix(name, "flannel") || strings.HasPrefix(name, "kube") {
			continue
		}
		// 物理网卡通常有 device 符号链接（虚拟网卡没有）
		if _, err := os.Readlink("/sys/class/net/" + name + "/device"); err != nil {
			continue
		}
		out = append(out, name)
	}
	return out
}

func readSysctl(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func writeSysctl(path, value string) error {
	return os.WriteFile(path, []byte(value+"\n"), 0o644)
}

// disablePhysicalIPv6 关闭物理网卡的 IPv6，返回被关闭的网卡名。
func disablePhysicalIPv6() []string {
	ifaces := linuxPhysicalIfaces()
	if len(ifaces) == 0 {
		return nil
	}
	state := &linuxIPv6State{Values: map[string]string{}}
	if prev := readLinuxIPv6State(); prev != nil {
		state = prev
	}

	var disabled []string
	for _, iface := range ifaces {
		confPath := "/proc/sys/net/ipv6/conf/" + iface + "/disable_ipv6"
		cur, err := readSysctl(confPath)
		if err != nil {
			continue
		}
		if _, ok := state.Values[iface]; !ok {
			state.Values[iface] = cur
		}
		if err := writeSysctl(confPath, "1"); err != nil {
			log.Printf("IPv6 防护（Linux）：关闭 %s 失败：%v", iface, err)
			continue
		}
		disabled = append(disabled, iface)
		log.Printf("IPv6 防护（Linux）：已关闭网卡 %s 的 IPv6", iface)
	}
	if len(disabled) > 0 {
		writeLinuxIPv6State(state)
	}
	return disabled
}

// restoreIPv6 恢复被关闭的网卡。
func restoreIPv6(adapters []string) {
	state := readLinuxIPv6State()
	if state == nil {
		return
	}
	for iface, val := range state.Values {
		confPath := "/proc/sys/net/ipv6/conf/" + iface + "/disable_ipv6"
		if err := writeSysctl(confPath, val); err != nil {
			log.Printf("IPv6 防护（Linux）：恢复 %s 失败：%v", iface, err)
			continue
		}
		log.Printf("IPv6 防护（Linux）：已恢复网卡 %s 的 IPv6（disable_ipv6=%s）", iface, val)
	}
	_ = os.Remove(linuxIPv6MarkerPath())
}

// restoreIPv6FromMarker 启动时恢复上次未还原的配置。
func restoreIPv6FromMarker() {
	if _, err := os.Stat(linuxIPv6MarkerPath()); err != nil {
		return
	}
	log.Printf("IPv6 防护（Linux）：检测到上次未恢复的网卡，正在还原")
	restoreIPv6(nil)
}

// physicalIPv6Addrs 返回非回环、非链路本地的 IPv6 地址（用于校验是否还泄露）。
func physicalIPv6Addrs() []string {
	raw, err := os.ReadFile("/proc/net/if_inet6")
	if err != nil {
		return nil
	}
	var list []string
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		hexAddr, iface := f[0], f[5]
		if iface == "lo" || strings.HasPrefix(hexAddr, "fe80") {
			continue
		}
		list = append(list, iface+":"+hexAddr)
	}
	return list
}

func readLinuxIPv6State() *linuxIPv6State {
	data, err := os.ReadFile(linuxIPv6MarkerPath())
	if err != nil {
		return nil
	}
	var st linuxIPv6State
	if json.Unmarshal(data, &st) != nil {
		return nil
	}
	return &st
}

func writeLinuxIPv6State(st *linuxIPv6State) {
	path := linuxIPv6MarkerPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	raw, _ := json.MarshalIndent(st, "", "  ")
	_ = os.WriteFile(path, raw, 0o600)
}