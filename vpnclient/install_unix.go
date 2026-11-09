//go:build !windows

package main

import (
	"errors"
	"log"
	"os"
)

// 非 Windows 平台的安装语义不同：Linux/macOS 用系统包管理器或脚本部署，
// 不在这里注册网卡（TUN 由内核提供）。

// installResult 与 Windows 版本保持同名结构，便于共用 main.go 的调用。
type installResult struct {
	OK         bool   `json:"ok"`
	InstallDir string `json:"install_dir"`
	ExePath    string `json:"exe_path"`
	Adapter    string `json:"adapter"`
	DriverPath string `json:"driver_path"`
	Version    string `json:"version"`
	Autostart  bool   `json:"autostart"`
	Error      string `json:"error,omitempty"`
}

// InstallOptions 与 Windows 版本保持同名结构。
type InstallOptions struct {
	Dir           string
	Autostart     bool
	SkipAutostart bool
}

// DefaultInstallDir 非 Windows 平台返回可执行文件所在目录。
func DefaultInstallDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return exe
}

// InstalledExePath 非 Windows 平台即当前程序。
func InstalledExePath() string { return DefaultInstallDir() }

// IsInstalled 非 Windows 平台始终视为「已就绪」。
func IsInstalled() bool { return true }

// RunInstall 在非 Windows 平台无需安装驱动，直接返回当前可执行文件信息。
func RunInstall(opts InstallOptions) (installResult, error) {
	exe, err := os.Executable()
	if err != nil {
		return installResult{}, err
	}
	dir := opts.Dir
	if dir == "" {
		dir = exe
	}
	return installResult{OK: true, InstallDir: dir, ExePath: exe, Version: Version}, nil
}

// RunUninstall 非 Windows 平台无驱动需要移除。
func RunUninstall() error {
	return errors.New("当前平台的卸载请删除二进制并使用系统包管理器")
}

// adapterStatus 非 Windows 平台由内核提供 TUN，无需注册。
func adapterStatus() (string, bool) {
	return defaultTunName(), true
}

// printInstallResult 输出安装结果。
func printInstallResult(res installResult) {
	log.Printf("安装信息：目录=%s 程序=%s", res.InstallDir, res.ExePath)
}