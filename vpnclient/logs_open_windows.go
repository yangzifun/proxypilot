//go:build windows

package main

import (
	"os"
	"os/exec"
)

// openLogFolder 在资源管理器里打开日志目录。
func openLogFolder() bool {
	dir := logDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	cmd := exec.Command("explorer.exe", dir)
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return false
	}
	return true
}