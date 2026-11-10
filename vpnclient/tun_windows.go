//go:build windows

package main

import (
	"fmt"
	"strings"

	wgtun "golang.zx2c4.com/wireguard/tun"
)

// defaultTunName 各平台默认网卡名。
func defaultTunName() string {
	return "ProxyPilot VPN"
}

// wintunDevice 把 wireguard/tun 的批量读写适配成单包 PacketDevice。
type wintunDevice struct {
	dev  wgtun.Device
	name string
	bufs [][]byte
	szs  []int
}

func (d *wintunDevice) Name() string { return d.name }

func (d *wintunDevice) Read(p []byte) (int, error) {
	if cap(p) < 2048 {
		return 0, fmt.Errorf("读缓冲太小：%d", cap(p))
	}
	d.bufs[0] = p[:cap(p)]
	n, err := d.dev.Read(d.bufs, d.szs, 0)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	return d.szs[0], nil
}

func (d *wintunDevice) Write(p []byte) (int, error) {
	d.bufs[0] = p
	n, err := d.dev.Write(d.bufs, 0)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, fmt.Errorf("写入网卡失败")
	}
	return len(p), nil
}

func (d *wintunDevice) Close() error { return d.dev.Close() }

// openTUN 在 Windows 上用 Wintun 创建虚拟网卡（需要管理员权限）。
func openTUN(name string) (PacketDevice, error) {
	if err := ensureWintun(); err != nil {
		return nil, err
	}
	dev, err := wgtun.CreateTUN(name, 1420)
	if err != nil {
		return nil, fmt.Errorf("创建 Wintun 网卡失败（需要管理员权限，且 wintun.dll 必须存在）：%w", err)
	}
	realName, nameErr := dev.Name()
	if nameErr != nil || strings.TrimSpace(realName) == "" {
		realName = name
	}
	// 关掉网卡的发送卸载：开了 LSO 之后 Windows 会把「大段」交给网卡去切分，
	// 交上来的报文 IP 头声明长度远大于实际字节数（实测差值 9/12/205 不等），
	// 我们把它当普通报文发出去，对端就会判为坏包丢弃 —— 表现为 25% 丢包。
	disableTunOffload(realName)

	return &wintunDevice{dev: dev, name: realName, bufs: make([][]byte, 1), szs: make([]int, 1)}, nil
}