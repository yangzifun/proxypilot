//go:build linux

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// addBypassHostRoute 给某个 IP 加一条 /32 直连路由，走物理默认网关。
// 用途见 bypass_route_windows.go。
func addBypassHostRoute(ip string) error {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return fmt.Errorf("IP 为空")
	}
	gw := defaultGatewayLinux()
	if gw == "" {
		return fmt.Errorf("未取到物理默认网关")
	}
	if out, err := exec.Command("ip", "route", "replace", ip+"/32", "via", gw).CombinedOutput(); err != nil {
		return fmt.Errorf("%v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// defaultGatewayLinux 从 `ip route show default` 里取第一个网关。
func defaultGatewayLinux() string {
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "via" {
				return f[i+1]
			}
		}
	}
	return ""
}