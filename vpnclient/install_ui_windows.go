//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// psQuote 生成 PowerShell 单引号字符串字面量（内部单引号写两次）。
// 单引号字符串不解析反斜杠，路径可以原样写入。
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// shortcutPaths 返回期望生成的快捷方式路径。
func shortcutPaths() []string {
	desktop := filepath.Join(os.Getenv("USERPROFILE"), "Desktop", "ProxyPilot.lnk")
	startMenu := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "ProxyPilot", "ProxyPilot.lnk")
	return []string{desktop, startMenu}
}

// installCreateShortcuts 创建桌面与开始菜单图标，并逐个校验文件存在。
//
// 注意：脚本必须写成 UTF-8 BOM，否则 Windows PowerShell 5.1 会按 ANSI 解析，
// 中文字符被破坏后导致 "字符串缺少终止符" 的语法错误。
func installCreateShortcuts(exe string) error {
	dir := filepath.Dir(exe)
	// 脚本保持纯 ASCII，避免任何编码问题
	script := strings.Join([]string{
		"$ErrorActionPreference = 'Stop'",
		"$shell = New-Object -ComObject WScript.Shell",
		"function New-Lnk([string]$path) {",
		"  $parent = Split-Path -Parent $path",
		"  if (-not (Test-Path $parent)) { New-Item -ItemType Directory -Force -Path $parent | Out-Null }",
		"  $l = $shell.CreateShortcut($path)",
		"  $l.TargetPath = " + psQuote(exe),
		"  $l.WorkingDirectory = " + psQuote(dir),
		"  $l.IconLocation = " + psQuote(exe+",0"),
		"  $l.Description = 'ProxyPilot VPN Client'",
		"  $l.Save()",
		"  if (-not (Test-Path $path)) { throw ('shortcut not created: ' + $path) }",
		"  Write-Output ('ok ' + $path)",
		"}",
		"",
	}, "\r\n")

	var calls []string
	for _, p := range shortcutPaths() {
		calls = append(calls, "New-Lnk "+psQuote(p))
	}
	script += strings.Join(calls, "\r\n") + "\r\n"

	ps := filepath.Join(os.TempDir(), "pp-shortcut.ps1")
	// UTF-8 BOM
	content := append([]byte{0xEF, 0xBB, 0xBF}, []byte(script)...)
	if err := os.WriteFile(ps, content, 0o644); err != nil {
		return err
	}
	defer os.Remove(ps)

	out, err := runWin("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", ps)
	if err != nil {
		return fmt.Errorf("创建快捷方式失败：%v %s", err, strings.TrimSpace(out))
	}

	// Go 侧再校验一次，避免依赖脚本输出
	missing := []string{}
	for _, p := range shortcutPaths() {
		if _, serr := os.Stat(p); serr != nil {
			missing = append(missing, p)
		}
	}
	if len(missing) == len(shortcutPaths()) {
		return fmt.Errorf("快捷方式未生成：%v", missing)
	}
	return nil
}

// launchProgram 启动指定程序（安装完成后拉起已安装版本）。
func launchProgram(exe, args string) error {
	return launchDetached(exe, args)
}

// platformName 返回平台标识，供前端展示。
func platformName() string { return "windows" }