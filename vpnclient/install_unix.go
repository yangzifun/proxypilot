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
	Error      string `json:"error,omitempty"`
}

// RunInstall 在非 Windows 平台无需安装驱动，直接返回当前可执行文件信息。
func RunInstall(dir string) (installResult, error) {
	exe, err := os.Executable()
	if err != nil {
		return installResult{}, err
	}
	if dir == "" {
		dir = exe
	}
	return installResult{OK: true, InstallDir: dir, ExePath: exe, Version: Version}, nil
}

// RunUninstall 非 Windows 平台无驱动需要移除。
func RunUninstall() error {
	return errors.New("当前平台的卸载请使用系统包管理器或删除二进制/blob")
}

// adapterStatus 非 Windows 平台通过 /sys 或 ip 命令判断，这里简化为未注册。
func adapterStatus() (string, bool) {
	return defaultTunName(), false
}

// printInstallResult 输出安装结果。
func printInstallResult(res installResult) {
	log.Printf("安装信息：目录=%s 程序=%s", res.InstallDir, res.ExePath)
}