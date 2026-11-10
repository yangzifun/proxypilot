//go:build windows

package main

import (
	"fmt"

	"strings"
)

// addBypassHostRoute 给某个 IP 加一条 /32 直连路由，走物理默认网关。
//
// 用途：面板域名（Cloudflare）会轮换 IP，连接时只给解析到的那个加了直连，
// 换到另一个 IP 时面板请求就会被塞进隧道 —— 隧道一抖，面板请求就
// TLS handshake timeout。这个函数供「连接期间定期补路由」使用。
func addBypassHostRoute(ip string) error {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return fmt.Errorf("IP 为空")
	}
	gw := defaultGatewayWin()
	if gw == "" {
		return fmt.Errorf("未取到物理默认网关")
	}
	// 先删旧条目，避免路由表里堆重复项
	_, _ = runWin("route", "delete", ip)
	out, err := runWin("route", "add", ip, "mask", "255.255.255.255", gw, "metric", "1")
	if err != nil {
		return fmt.Errorf("%v %s", err, strings.TrimSpace(out))
	}
	return nil
}