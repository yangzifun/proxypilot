//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	appDisplayName = "ProxyPilot VPN"
	appPublisher   = "ProxyPilot"
	uninstallKey   = `Software\Microsoft\Windows\CurrentVersion\Uninstall\ProxyPilot`
)

// installResult 描述安装结果，供安装脚本读取。
type installResult struct {
	OK         bool   `json:"ok"`
	InstallDir string `json:"install_dir"`
	ExePath    string `json:"exe_path"`
	Adapter    string `json:"adapter"`
	DriverPath string `json:"driver_path"`
	Version    string `json:"version"`
	Error      string `json:"error,omitempty"`
}

// RunInstall 执行安装：释放驱动、注册虚拟网卡、写卸载信息。
//
// 关键点：Wintun 的适配器是「持久设备」——创建一次后即使句柄关闭也会留在系统里，
// 后续连接时用同名 CreateTUN 会直接复用，所以安装阶段注册网卡是安全且必要的。
func RunInstall(installDir string) (installResult, error) {
	res := installResult{InstallDir: installDir, Version: Version}

	exe, err := os.Executable()
	if err != nil {
		return res, fmt.Errorf("获取程序路径失败：%w", err)
	}
	res.ExePath = exe
	if installDir == "" {
		installDir = filepath.Dir(exe)
		res.InstallDir = installDir
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return res, fmt.Errorf("创建安装目录失败：%w", err)
	}

	// 1) 把驱动释放到安装目录（运行时 LoadLibraryEx 只认 exe 同目录/System32）
	driver, err := releaseDriverTo(installDir)
	if err != nil {
		return res, err
	}
	res.DriverPath = driver

	// 2) 注册虚拟网卡（首次会安装 Wintun 驱动，之后复用）
	spec := TunSpec{Name: defaultTunName(), MTU: 1420, IP: "", CIDR: ""}
	dev, err := openTUN(spec.name())
	if err != nil {
		return res, fmt.Errorf("注册虚拟网卡失败：%w", err)
	}
	res.Adapter = dev.Name()
	_ = dev.Close()

	// 3) 卸载信息（设置 → 应用 里可见）
	if err := writeUninstallEntry(installDir, exe); err != nil {
		// 非致命：不影响使用
		fmt.Fprintf(os.Stderr, "[install] 写卸载信息失败：%v\n", err)
	}

	res.OK = true
	return res, nil
}

// RunUninstall 删除虚拟网卡与注册表项（文件由安装脚本删除）。
func RunUninstall() error {
	// 删除适配器：打开后调用 wintun 的删除接口不可用，这里用 netsh 移除
	if name := defaultTunName(); name != "" {
		_, _ = runWin("netsh", "interface", "set", "interface", name, "admin=disabled")
	}
	_ = registry.DeleteKey(registry.LOCAL_MACHINE, uninstallKey)
	return nil
}

// writeUninstallEntry 在「设置 → 应用」里登记本程序。
func writeUninstallEntry(installDir, exe string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, uninstallKey, registry.SET_VALUE|registry.WRITE)
	if err != nil {
		return err
	}
	defer k.Close()

	values := map[string]string{
		"DisplayName":     appDisplayName,
		"DisplayVersion":  strings.TrimPrefix(Version, "V"),
		"Publisher":       appPublisher,
		"InstallLocation": installDir,
		"DisplayIcon":     exe,
		"UninstallString": fmt.Sprintf(`"%s" -uninstall`, exe),
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

// printInstallResult 输出 JSON，方便安装脚本解析（GUI 子系统下控制台不可见也无妨）。
func printInstallResult(res installResult) {
	b, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(b))
	// 同时写到临时文件，安装脚本可靠读取
	if res.InstallDir != "" {
		_ = os.WriteFile(filepath.Join(os.TempDir(), "proxypilot-install.json"), b, 0o644)
	}
}