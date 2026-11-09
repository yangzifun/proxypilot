//go:build linux || darwin

package main

import (
	"fmt"
	"strings"

	"github.com/songgao/water"
)

// defaultTunName 各平台默认网卡名。
func defaultTunName() string {
	return "ppvpn0"
}

// openTUN 在 Linux/macOS 上创建 TUN 网卡（需要 root 或 CAP_NET_ADMIN）。
func openTUN(name string) (PacketDevice, error) {
	cfg := water.Config{DeviceType: water.TUN}
	if strings.TrimSpace(name) != "" {
		cfg.PlatformSpecificParams = water.PlatformSpecificParams{Name: name}
	}
	dev, err := water.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("创建 TUN 失败（需要 root/CAP_NET_ADMIN）：%w", err)
	}
	return dev, nil
}