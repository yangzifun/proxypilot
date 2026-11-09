//go:build windows

package main

import (
	"fmt"
	"log"
	"strings"

	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows"
)

// logWin 输出调试日志。
func logWin(format string, args ...any) { log.Printf(format, args...) }

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

	// 地址：先清掉旧配置（dhcp），再用 add address 叠加当前地址。
	// 注意：不能用 set address ... static <gw>，它会把默认网关写成「永久路由」，
	// 客户端异常退出后会残留在系统里，导致所有流量被黑洞。
	gw := s.gateway()
	res.Gateway = gw
	mask := strings.Split(res.IP, "/")[1]
	runWinSilent("netsh", "interface", "ipv4", "set", "address", "name="+dev.Name(), "source=dhcp")
	if out, err := runWin("netsh", "interface", "ipv4", "add", "address", dev.Name(), s.IP, mask); err != nil {
		return res, fmt.Errorf("配置网卡地址失败：%v %s", err, out)
	}
	// 网关只写进「活动」路由表（ipconfig 可见），不持久化
	if gw != "" {
		runWinSilent("netsh", "interface", "ipv4", "add", "route", "0.0.0.0/0", dev.Name(), gw, "metric=60", "store=active")
	}

	// MTU
	_, _ = runWin("netsh", "interface", "ipv4", "set", "subinterface", dev.Name(),
		fmt.Sprintf("mtu=%d", res.MTU), "store=active")

	// 关键：先给 VPN 服务器自身加主机路由，绕开即将下发的 /1 全局路由。
	// 否则 QUIC 发往节点的包会被送进隧道，形成环路（表现为 wsasendto 不可达 / 隧道中断）。
	if res.Mode == TunModeFull {
		realGW := defaultGatewayWin()
		if realGW != "" {
			for _, ip := range s.endpointIPs() {
				_, _ = runWin("route", "delete", ip)
				if out, err := runWin("route", "add", ip, "mask", "255.255.255.255", realGW, "metric", "1"); err != nil {
					logWin("给服务器 %s 加主机路由失败：%v %s", ip, err, out)
					continue
				}
				res.Routes = append(res.Routes, ip+"/32(via "+realGW+")")
				logWin("已为 VPN 服务器 %s 添加直连主机路由（经 %s）", ip, realGW)
			}
		} else {
			logWin("未能获取真实默认网关，跳过服务器主机路由（可能导致环路）")
		}
	}

	for _, r := range planRoutes(s) {
		// 先删掉旧版本可能残留的同前缀路由（否则 add 会因已存在而失败）
		_, _ = runWin("netsh", "interface", "ipv4", "delete", "route", r, dev.Name())
		// 直接挂到网卡：下一跳 0.0.0.0（on-link），避免对虚拟网关做 ARP 解析
		if out, err := runWin("netsh", "interface", "ipv4", "add", "route",
			r, dev.Name(), "0.0.0.0", "metric=5", "store=active"); err != nil {
			return res, fmt.Errorf("下发路由 %s 失败：%v %s", r, err, out)
		}
		res.Routes = append(res.Routes, r)
	}

	// DNS：默认 1.1.1.1 + 8.8.8.8
	dns := s.dnsList()
	applied := []string{}
	for i, d := range dns {
		if !isIPv4(d) {
			continue
		}
		var out string
		var err error
		if i == 0 {
			out, err = runWin("netsh", "interface", "ipv4", "set", "dnsservers",
				"name="+dev.Name(), "static", d, "primary")
		} else {
			out, err = runWin("netsh", "interface", "ipv4", "add", "dnsservers",
				"name="+dev.Name(), d, fmt.Sprintf("index=%d", i+1))
		}
		if err != nil {
			logWin("设置 DNS %s 失败：%v %s", d, err, out)
			continue
		}
		applied = append(applied, d)
	}
	res.DNS = strings.Join(applied, ", ")

	// DNS 泄露防护：NRPT 强制走隧道 DNS + 封禁物理网卡的明文 DNS 出站。
	// Windows 会并行向所有网卡的 DNS 发查询，只配虚拟网卡会漏到运营商 DNS。
	res.DNSGuard = applyDNSGuard(dns)
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
		if strings.Contains(ip, "/") {
			_, _ = runWin("netsh", "interface", "ipv4", "delete", "route", ip, res.Adapter, "store=active")
		}
	}
	// 先撤销 DNS 守卫，再清地址（顺序重要：先恢复解析，避免断开后短暂无 DNS）
	revertDNSGuard()
	// 只清地址与 DNS，不禁用适配器（保持下次连接可直接复用）
	if res.Adapter != "" {
		_, _ = runWin("netsh", "interface", "ipv4", "set", "address", "name="+res.Adapter, "source=dhcp")
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