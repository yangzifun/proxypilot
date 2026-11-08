//go:build linux

package main

import "github.com/songgao/water"

// openTUN 在 Linux 上创建 TUN 网卡。
func openTUN(name string) (*water.Interface, error) {
	return water.New(water.Config{
		DeviceType:             water.TUN,
		PlatformSpecificParams: water.PlatformSpecificParams{Name: name},
	})
}
