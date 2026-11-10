//go:build darwin

package main

import (
	"fmt"
	"strings"
)

// addBypassHostRoute 给某个 IP 加一条 /32 直连路由，走物理默认网关。
// 用途见 bypass_route_windows.go。
func addBypassHostRoute(ip string) error {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return fmt.Errorf("IP 为空")
	}
	gw := defaultGatewayDarwin()
	if gw == "" {
		return fmt.Errorf("未取到物理默认网关")
	}
	_, _ = runMac("route", "-n", "delete", "-host", ip)
	if out, err := runMac("route", "-n", "add", "-host", ip, gw); err != nil {
		return fmt.Errorf("%v %s", err, strings.TrimSpace(out))
	}
	return nil
}

// defaultGatewayDarwin 从 `route -n get default` 里取网关。
func defaultGatewayDarwin() string {
	out, err := runMac("route", "-n", "get", "default")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "gateway:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "gateway:"))
		}
	}
	return ""
}