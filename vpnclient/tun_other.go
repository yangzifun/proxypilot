//go:build !linux

package main

import (
	"errors"

	"github.com/songgao/water"
)

// openTUN 组网模式依赖 Linux TUN；Windows/macOS 目前只提供控制台界面。
func openTUN(name string) (*water.Interface, error) {
	return nil, errors.New("组网模式仅支持 Linux（当前平台仅提供客户端控制台）")
}
