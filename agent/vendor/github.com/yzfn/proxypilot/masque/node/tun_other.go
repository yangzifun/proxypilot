//go:build !linux

package node

import "errors"

func openTUN(string, int) (tunDevice, error) {
	return nil, errors.New("pp-masque-node 目前仅支持 Linux")
}

func configureTUN(Config) error { return errors.New("仅 Linux 支持自动配置 TUN/NAT") }

func cleanupTUN(Config) {}
