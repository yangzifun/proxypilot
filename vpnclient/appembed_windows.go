//go:build windows && desktop && installer

package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// 安装程序内置客户端主程序：这样只需一个安装包文件，
// 安装时把主程序写到安装目录（ProxyPilot.exe），而不是复制安装程序自身。
//
// 构建顺序：先不带 installer 标签构建主程序 → 复制到 assets/ProxyPilotApp.exe
// → 再带 installer 标签构建安装程序。
//
//go:embed assets/ProxyPilotApp.exe
var embeddedApp []byte

// installAppBinary 把内置主程序写入安装目录。
//
// 旧版本客户端可能正在运行（文件被占用，rename 会 Access denied），
// 所以先尝试结束 ProxyPilot 进程，再带重试地替换。
func installAppBinary(dir string) (string, error) {
	if len(embeddedApp) < 1024 {
		return "", fmt.Errorf("安装包内未包含客户端主程序（构建顺序有误）")
	}
	dst := filepath.Join(dir, embeddedAppName)
	tmp := dst + ".new"
	if err := os.WriteFile(tmp, embeddedApp, 0o755); err != nil {
		return "", fmt.Errorf("写入主程序失败：%w", err)
	}

	// 结束正在运行的旧客户端，释放文件占用
	stopRunningClient()
	_ = os.Chmod(dst, 0o666)

	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		_ = os.Remove(dst)
		if err := os.Rename(tmp, dst); err == nil {
			return dst, nil
		} else {
			lastErr = err
		}
		// 每 3 次再补一次结束进程（用户可能又点开了）
		if attempt%3 == 2 {
			stopRunningClient()
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", fmt.Errorf("替换主程序失败（请先退出正在运行的 ProxyPilot 后重试）：%w", lastErr)
}

// stopRunningClient 结束正在运行的客户端进程，释放 ProxyPilot.exe 占用。
func stopRunningClient() {
	_, _ = runWin("taskkill", "/F", "/IM", "ProxyPilot.exe")
	time.Sleep(400 * time.Millisecond)
}