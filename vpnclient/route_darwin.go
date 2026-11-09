//go:build darwin

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// applyTun 在 macOS 上配置地址、MTU、启用网卡，并按模式下发路由。
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

	if s.IP != "" {
		if out, err := run("ifconfig", dev.Name(), s.IP, s.IP, "mtu", fmt.Sprint(res.MTU), "up"); err != nil {
			return res, fmt.Errorf("配置网卡失败：%v %s", err, out)
		}
	}

	gw := defaultGateway()
	if res.Mode == TunModeFull && gw != "" {
		if host := s.endpointHost(); host != "" {
			_, _ = run("route", "-n", "add", "-host", host, gw)
		}
	}
	for _, r := range planRoutes(s) {
		if out, err := run("route", "-n", "add", "-net", r, "-interface", dev.Name()); err != nil {
			// macOS 用 -net 传 CIDR 也接受，失败时再试一次去掉掩码的写法
			if out2, err2 := run("route", "-n", "add", r, dev.Name()); err2 != nil {
				return res, fmt.Errorf("下发路由 %s 失败：%v %s / %v %s", r, err, out, err2, out2)
			}
		}
		res.Routes = append(res.Routes, r)
	}
	return res, nil
}

func defaultGateway() string {
	out, err := exec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "gateway:") {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "gateway:"))
		}
	}
	return ""
}

func cleanupTun(res TunResult) {
	run := func(args ...string) {
		_ = exec.Command(args[0], args[1:]...).Run()
	}
	for _, r := range res.Routes {
		run("route", "-n", "delete", r)
	}
	if res.Adapter != "" {
		run("ifconfig", res.Adapter, "down")
	}
}