//go:build !windows

package main

import (
	"os"
	"path/filepath"
)

// 非 Windows 平台：安装向导只做引导（TUN 由内核提供，无驱动可释放）。

// isAdmin 通过 uid 判断是否 root。
func isAdmin() bool { return os.Geteuid() == 0 }

// releaseDriverTo 非 Windows 平台没有驱动文件，仅确保目录存在。
func releaseDriverTo(dir string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	return "", nil
}