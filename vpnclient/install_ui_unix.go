//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// 非 Windows 平台：没有独立的网卡注册与快捷方式步骤，安装向导只做形式上的引导。

func installCopySelf(dst string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if src, _ := filepath.Abs(exe); src == dst {
		return nil
	}
	return copySelfUnix(exe, dst)
}

func copySelfUnix(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}

func installWriteRegistry(dir, exe string) error { return nil }
func installSetAutostart(exe string, enable bool) error { return nil }
func installCreateShortcuts(exe string) error          { return nil }
func shortcutPaths() []string                         { return nil }
func launchProgram(exe, args string) error             { return errors.New("当前平台不支持自启动安装版本") }
func platformName() string                             { return runtime.GOOS }