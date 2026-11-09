//go:build windows

package main

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows"
)

// runWin 执行命令并合并输出（不弹窗）。
func runWin(args ...string) (string, error) {
	cmd := execCommand(args[0], args[1:]...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// applyTun 在 Windows 上配置地址、MTU、启用网卡，并按模式下发路由。
//
// 地址与 MTU 用 netsh，路由用 route.exe（兼容 Win10/11，无需 PowerShell）。
func applyTun(s TunSpec, dev PacketDevice) (TunResult, error) {
	res := TunResult{
		Adapter: dev.Name(),
		IP:      fmt.Sprintf("%s/255.255.0.0", s.IP),
		MTU:     s.mtu(),
		Mode:    s.Mode,
		Routes:  []string{},
	}
	if res.Mode == "" {
		res.Mode = TunModeMesh
	}
	if ones := s.prefixLen(); ones != 16 {
		if mask := prefixToMask(ones); mask != "" {
			res.IP = fmt.Sprintf("%s/%s", s.IP, mask)
		}
	}

	// 地址：netsh interface ipv4 set address name="<适配器>" static <ip> <mask>
	if _, err := runWin("netsh", "interface", "ipv4", "set", "address",
		"name="+dev.Name(), "static", s.IP, strings.Split(res.IP, "/")[1]); err != nil {
		// 某些系统上名字匹配失败，退回按索引
		if idx, ierr := interfaceIndex(dev.Name()); ierr == nil {
			if _, err2 := runWin("netsh", "interface", "ipv4", "set", "address",
				fmt.Sprint(idx), "static", s.IP, strings.Split(res.IP, "/")[1]); err2 != nil {
				return res, fmt.Errorf("配置网卡地址失败：%v", err2)
			}
		} else {
			return res, fmt.Errorf("配置网卡地址失败：%v", err)
		}
	}

	// MTU
	_, _ = runWin("netsh", "interface", "ipv4", "set", "subinterface", dev.Name(),
		fmt.Sprintf("mtu=%d", res.MTU), "store=persistent")

	// full 模式：给 VPN 服务器加主机路由，避免隧道流量回绕
	if res.Mode == TunModeFull {
		if host := s.endpointHost(); host != "" && isIPv4(host) {
			if gw := defaultGatewayWin(); gw != "" {
				_, _ = runWin("route", "add", host, "mask", "255.255.255.255", gw, "metric", "1")
				res.Routes = append(res.Routes, host+"/32(via "+gw+")")
			}
		}
	}

	for _, r := range planRoutes(s) {
		ip, mask, err := cidrToNetsh(r)
		if err != nil {
			return res, err
		}
		// 以网卡名作为网关参数即可把流量压到该适配器
		if out, err := runWin("route", "add", ip, "mask", mask, s.IP, "metric", "5"); err != nil {
			return res, fmt.Errorf("下发路由 %s 失败：%v %s", r, err, out)
		}
		res.Routes = append(res.Routes, r)
	}

	// 可选 DNS
	if dns := strings.TrimSpace(s.DNS); dns != "" && isIPv4(dns) {
		if _, err := runWin("netsh", "interface", "ipv4", "set", "dnsservers",
			"name="+dev.Name(), "static", dns, "primary"); err == nil {
			res.DNS = dns
		}
	}
	return res, nil
}

// cleanupTun 删除本进程加过的路由并关掉适配器地址。
func cleanupTun(res TunResult) {
	for _, r := range res.Routes {
		ip := r
		if i := strings.Index(ip, "("); i > 0 {
			ip = strings.TrimSpace(ip[:i])
		}
		if ip == "" {
			continue
		}
		if strings.Contains(ip, "/32") {
			ip = strings.TrimSuffix(ip, "/32")
			_, _ = runWin("route", "delete", ip)
			continue
		}
		if a, b, err := cidrToNetsh(ip); err == nil {
			_, _ = runWin("route", "delete", a, "mask", b)
		}
	}
	if res.Adapter != "" {
		_, _ = runWin("netsh", "interface", "set", "interface", res.Adapter, "admin=disabled")
	}
}

// defaultGatewayWin 取默认网关（IPv4）。
func defaultGatewayWin() string {
	out, err := runWin("route", "print", "-4", "0.0.0.0")
	if err != nil {
		return ""
	}
	best := ""
	bestMetric := 1 << 30
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		// 目标 掩码 网关 接口 跃点
		if len(f) < 5 || f[0] != "0.0.0.0" || f[1] != "0.0.0.0" {
			continue
		}
		if f[2] == "On-link" || f[2] == "在链路上" {
			continue
		}
		metric := 0
		fmt.Sscanf(f[4], "%d", &metric)
		if metric > 0 && metric < bestMetric {
			bestMetric = metric
			best = f[2]
		}
	}
	return best
}

// interfaceIndex 通过适配器名查接口索引（用注册表兜底，避免依赖 PowerShell）。
func interfaceIndex(name string) (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Network\{4D36E972-E325-11CE-BFC1-08002BE10318}`, registry.READ)
	if err != nil {
		return "", err
	}
	defer k.Close()
	subs, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return "", err
	}
	for _, guid := range subs {
		ck, cerr := registry.OpenKey(k, guid+`\Connection`, registry.READ)
		if cerr != nil {
			continue
		}
		n, _, nerr := ck.GetStringValue("Name")
		ck.Close()
		if nerr == nil && strings.EqualFold(n, name) {
			return guid, nil
		}
	}
	return "", fmt.Errorf("未找到适配器 %s", name)
}

func prefixToMask(ones int) string {
	if ones <= 0 || ones > 32 {
		return ""
	}
	var m uint32 = 0xFFFFFFFF << (32 - ones)
	return fmt.Sprintf("%d.%d.%d.%d", byte(m>>24), byte(m>>16), byte(m>>8), byte(m))
}

// cidrToNetsh 把 CIDR 转成 route.exe 需要的 (ip, mask)。
func cidrToNetsh(cidr string) (string, string, error) {
	parts := strings.SplitN(cidr, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("路由格式非法：%s", cidr)
	}
	var ones int
	fmt.Sscanf(parts[1], "%d", &ones)
	mask := prefixToMask(ones)
	if mask == "" {
		return "", "", fmt.Errorf("路由前缀非法：%s", cidr)
	}
	return parts[0], mask, nil
}

func isIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		n := 0
		if _, err := fmt.Sscanf(p, "%d", &n); err != nil || n < 0 || n > 255 {
			return false
		}
	}
	return true
}

var _ = windows.Handle(0)