//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	appDisplayName = "ProxyPilot VPN"
	appPublisher   = "ProxyPilot"
	appExeName     = "ProxyPilot.exe"
	uninstallKey   = `Software\Microsoft\Windows\CurrentVersion\Uninstall\ProxyPilot`
	runKey         = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName   = "ProxyPilot"
)

// installResult 描述安装结果，供安装脚本读取。
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

// InstallOptions 安装参数。
type InstallOptions struct {
	// Dir 安装目录，空则用 %LOCALAPPDATA%\Programs\ProxyPilot
	Dir string
	// Autostart 写入开机自启（HKCU Run，带 -background）
	Autostart bool
	// ForceAutostart 明确不写自启时为 false；Autostart=true 时生效
	SkipAutostart bool
}

// DefaultInstallDir 返回默认安装目录。
func DefaultInstallDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "Programs", "ProxyPilot")
}

// InstalledExePath 返回已安装程序的预期路径。
func InstalledExePath() string {
	return filepath.Join(DefaultInstallDir(), appExeName)
}

// IsInstalled 判断是否已经安装（程序文件 + 卸载登记项都在）。
func IsInstalled() bool {
	if _, err := os.Stat(InstalledExePath()); err != nil {
		return false
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	return true
}

// RunInstall 执行安装：复制程序、释放驱动、注册虚拟网卡、写卸载信息与自启。
//
// Wintun 适配器是「持久设备」：创建一次后即使句柄关闭也留在系统里，
// 连接时用同名 CreateTUN 会直接复用，所以安装阶段注册网卡是安全且必要的。
func RunInstall(opts InstallOptions) (installResult, error) {
	dir := strings.TrimSpace(opts.Dir)
	if dir == "" {
		dir = DefaultInstallDir()
	}
	res := installResult{InstallDir: dir, Version: Version}

	exe, err := os.Executable()
	if err != nil {
		return res, fmt.Errorf("获取程序路径失败：%w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, fmt.Errorf("创建安装目录失败：%w", err)
	}

	// 1) 把自己复制进安装目录（在安装目录内运行时跳过）
	dst := filepath.Join(dir, appExeName)
	if !strings.EqualFold(filepath.Clean(exe), filepath.Clean(dst)) {
		if err := copySelf(exe, dst); err != nil {
			return res, err
		}
	}
	res.ExePath = dst

	// 2) 把驱动释放到安装目录（运行时 LoadLibraryEx 只认 exe 同目录/System32）
	driver, err := releaseDriverTo(dir)
	if err != nil {
		return res, err
	}
	res.DriverPath = driver

	// 3) 注册虚拟网卡（首次安装 Wintun 驱动，之后复用）
	spec := TunSpec{Name: defaultTunName(), MTU: 1420}
	dev, err := openTUN(spec.name())
	if err != nil {
		return res, fmt.Errorf("注册虚拟网卡失败：%w", err)
	}
	res.Adapter = dev.Name()
	_ = dev.Close()

	// 4) 卸载信息（设置 → 应用 里可见）
	if err := writeUninstallEntry(dir, dst); err != nil {
		fmt.Fprintf(os.Stderr, "[install] 写卸载信息失败：%v\n", err)
	}

	// 5) 开机自启（托盘后台运行）
	if opts.Autostart && !opts.SkipAutostart {
		if err := setAutostart(dst, true); err == nil {
			res.Autostart = true
		} else {
			fmt.Fprintf(os.Stderr, "[install] 写开机自启失败：%v\n", err)
		}
	}

	res.OK = true
	return res, nil
}

// RunUninstall 删除虚拟网卡与注册表项（文件由安装脚本删除）。
func RunUninstall() error {
	setAutostart("", false)
	if name := defaultTunName(); name != "" {
		_, _ = runWin("netsh", "interface", "set", "interface", name, "admin=disabled")
	}
	_ = registry.DeleteKey(registry.LOCAL_MACHINE, uninstallKey)
	return nil
}

// copySelf 复制当前可执行文件到目标路径（同盘用重命名优化）。
func copySelf(src, dst string) error {
	// 目标可能正被运行，先尝试直接覆盖
	if err := copyFile(src, dst); err == nil {
		return nil
	}
	// 退而求其次：先写 .new 再替换
	tmp := dst + ".new"
	if err := copyFile(src, tmp); err != nil {
		return fmt.Errorf("复制程序失败：%w", err)
	}
	_ = os.Remove(dst)
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("替换程序失败：%w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// setAutostart 写入/删除开机自启（HKCU，不需要管理员）。
func setAutostart(exePath string, enable bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !enable {
		return k.DeleteValue(runValueName)
	}
	return k.SetStringValue(runValueName, fmt.Sprintf(`"%s" -background`, exePath))
}

// writeUninstallEntry 在「设置 → 应用」里登记本程序。
func writeUninstallEntry(installDir, exe string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, uninstallKey, registry.SET_VALUE|registry.WRITE)
	if err != nil {
		return err
	}
	defer k.Close()

	values := map[string]string{
		"DisplayName":          appDisplayName,
		"DisplayVersion":       strings.TrimPrefix(Version, "V"),
		"Publisher":            appPublisher,
		"InstallLocation":      installDir,
		"DisplayIcon":          exe,
		"UninstallString":      fmt.Sprintf(`"%s" -uninstall`, exe),
		"QuietUninstallString": fmt.Sprintf(`"%s" -uninstall -quiet`, exe),
	}
	for name, v := range values {
		if err := k.SetStringValue(name, v); err != nil {
			return err
		}
	}
	_ = k.SetDWordValue("NoModify", 1)
	_ = k.SetDWordValue("NoRepair", 1)
	return nil
}

// adapterStatus 返回当前系统里是否已存在我们的虚拟网卡。
func adapterStatus() (string, bool) {
	name := defaultTunName()
	out, err := runWin("netsh", "interface", "show", "interface")
	if err != nil {
		return name, false
	}
	return name, strings.Contains(out, name)
}

// printInstallResult 输出 JSON，方便安装脚本解析。
func printInstallResult(res installResult) {
	b, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(b))
	if res.InstallDir != "" {
		_ = os.WriteFile(filepath.Join(os.TempDir(), "proxypilot-install.json"), b, 0o644)
	}
}