//go:build linux

package main

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// applyTun 在 Linux 上配置地址、MTU、启用网卡，并按模式下发路由。
func applyTun(s TunSpec, dev PacketDevice) (TunResult, error) {
	res := TunResult{
		Adapter: dev.Name(),
		IP:      fmt.Sprintf("%s/%d", s.IP, s.prefixLen()),
		MTU:     s.mtu(),
		Mode:    s.Mode,
		Routes:  []string{},
	}
	if res.Mode == "" {
		res.Mode = TunModeMesh
	}
	run := func(args ...string) (string, error) {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	gw := s.gateway()
	res.Gateway = gw

	if out, err := run("ip", "link", "set", "dev", dev.Name(), "mtu", fmt.Sprint(res.MTU)); err != nil {
		return res, fmt.Errorf("设置 MTU 失败：%v %s", err, out)
	}
	if out, err := run("ip", "addr", "replace", res.IP, "dev", dev.Name()); err != nil {
		return res, fmt.Errorf("设置地址失败：%v %s", err, out)
	}
	if out, err := run("ip", "link", "set", "dev", dev.Name(), "up"); err != nil {
		return res, fmt.Errorf("启用网卡失败：%v %s", err, out)
	}

	// full 模式：先给 VPN 服务器加主机路由，避免隧道流量回环
	if res.Mode == TunModeFull {
		if gw := defaultGateway(); gw != "" {
			for _, ip := range s.endpointIPs() {
				if out, err := run("ip", "route", "replace", ip+"/32", "via", gw); err != nil {
					log.Printf("给服务器 %s 加主机路由失败：%v %s", ip, err, out)
					continue
				}
				res.Routes = append(res.Routes, ip+"/32(via "+gw+")")
			}
		}
	}

	for _, r := range planRoutes(s) {
		args := []string{"ip", "route", "replace", r}
		if gw != "" {
			args = append(args, "via", gw)
		}
		args = append(args, "dev", dev.Name())
		if out, err := run(args...); err != nil {
			return res, fmt.Errorf("下发路由 %s 失败：%v %s", r, err, out)
		}
		res.Routes = append(res.Routes, r)
	}

	// DNS：优先用 systemd-resolved 下发到该网卡
	dns := s.dnsList()
	if len(dns) > 0 {
		if _, lerr := exec.LookPath("resolvectl"); lerr == nil {
			args := append([]string{"resolvectl", "dns", dev.Name()}, dns...)
			if out, err := run(args...); err == nil {
				_, _ = run("resolvectl", "domain", dev.Name(), "~.")
				res.DNS = strings.Join(dns, ", ")
				_ = out
			}
		}
	}
	return res, nil
}

// defaultGateway 取当前默认网关。
func defaultGateway() string {
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "via" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

// cleanupTun 删除本进程加过的路由与网卡地址（网卡本身由 Close 回收）。
func cleanupTun(res TunResult) {
	run := func(args ...string) {
		_ = exec.Command(args[0], args[1:]...).Run()
	}
	for _, r := range res.Routes {
		run("ip", "route", "del", r)
	}
	if res.Adapter != "" {
		run("ip", "link", "set", "dev", res.Adapter, "down")
	}
}