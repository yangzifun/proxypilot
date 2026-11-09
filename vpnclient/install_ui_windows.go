//go:build windows

package main

import (
	"fmt"
	"strings"
	"os"
	"path/filepath"
)

// installCopySelf 复制当前可执行文件到目标路径。
func installCopySelf(dst string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if src, _ := filepath.Abs(exe); src == dst {
		return nil // 已在目标位置
	}
	return copySelf(exe, dst)
}

// installWriteRegistry 写入卸载登记项。
func installWriteRegistry(dir, exe string) error {
	return writeUninstallEntry(dir, exe)
}

// installSetAutostart 写入/删除开机自启。
func installSetAutostart(exe string, enable bool) error {
	return setAutostart(exe, enable)
}

// installCreateShortcuts 创建开始菜单（含文件夹）与桌面快捷方式，并校验 .lnk 确实生成。
func installCreateShortcuts(exe string) error {
	desktop := filepath.Join(os.Getenv("USERPROFILE"), "Desktop")
	if _, err := os.Stat(desktop); err != nil {
		// OneDrive 重定向时回落到注册表里的桌面路径
		if out, qerr := runWin("powershell", "-NoProfile", "-Command",
			"[Environment]::GetFolderPath('Desktop')"); qerr == nil {
			if p := strings.TrimSpace(out); p != "" {
				desktop = p
			}
		}
	}
	startMenu := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs")

	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$shell = New-Object -ComObject WScript.Shell
function New-Lnk($path) {
  $dir = Split-Path -Parent $path
  if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }
  $l = $shell.CreateShortcut($path)
  $l.TargetPath = %q
  $l.WorkingDirectory = %q
  $l.IconLocation = '%s,0'
  $l.Description = 'ProxyPilot VPN 客户端'
  $l.Save()
  if (-not (Test-Path $path)) { throw "未生成 $path" }
  Write-Output ("ok " + $path)
}
New-Lnk %q
New-Lnk %q
`, exe, filepath.Dir(exe), exe,
		filepath.Join(desktop, "ProxyPilot.lnk"),
		filepath.Join(startMenu, "ProxyPilot", "ProxyPilot.lnk"))

	ps := filepath.Join(os.TempDir(), "pp-shortcut.ps1")
	if err := os.WriteFile(ps, []byte(script), 0o644); err != nil {
		return err
	}
	defer os.Remove(ps)

	out, err := runWin("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", ps)
	if err != nil {
		return fmt.Errorf("创建快捷方式失败：%v %s", err, strings.TrimSpace(out))
	}
	return nil
}

// shortcutPaths 返回期望生成的快捷方式路径，供安装结果展示与校验。
func shortcutPaths() []string {
	desktop := filepath.Join(os.Getenv("USERPROFILE"), "Desktop", "ProxyPilot.lnk")
	startMenu := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "ProxyPilot", "ProxyPilot.lnk")
	return []string{desktop, startMenu}
}

// launchProgram 启动指定程序（安装完成后拉起已安装版本）。
func launchProgram(exe, args string) error {
	return launchDetached(exe, args)
}

// platformName 返回平台标识，供前端展示。
func platformName() string { return "windows" }