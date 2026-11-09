//go:build windows

package main

import (
	"fmt"
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

// installCreateShortcuts 创建开始菜单与桌面快捷方式（用 PowerShell 的 WScript.Shell）。
func installCreateShortcuts(exe string) error {
	script := fmt.Sprintf(`
$shell = New-Object -ComObject WScript.Shell
function New-Lnk($p) {
  $l = $shell.CreateShortcut($p)
  $l.TargetPath = %q
  $l.WorkingDirectory = %q
  $l.IconLocation = "%s,0"
  $l.Description = 'ProxyPilot VPN 客户端'
  $l.Save()
}
New-Lnk (Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\ProxyPilot.lnk')
New-Lnk (Join-Path ([Environment]::GetFolderPath('Desktop')) 'ProxyPilot.lnk')
`, exe, filepath.Dir(exe), exe)

	ps := filepath.Join(os.TempDir(), "pp-shortcut.ps1")
	if err := os.WriteFile(ps, []byte(script), 0o644); err != nil {
		return err
	}
	defer os.Remove(ps)
	_, err := runWin("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", ps)
	return err
}

// launchProgram 启动指定程序（安装完成后拉起已安装版本）。
func launchProgram(exe, args string) error {
	return launchDetached(exe, args)
}

// platformName 返回平台标识，供前端展示。
func platformName() string { return "windows" }