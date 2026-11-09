//go:build windows && desktop && installer

package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
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
func installAppBinary(dir string) (string, error) {
	if len(embeddedApp) < 1024 {
		return "", fmt.Errorf("安装包内未包含客户端主程序（构建顺序有误）")
	}
	dst := filepath.Join(dir, embeddedAppName)
	tmp := dst + ".new"
	if err := os.WriteFile(tmp, embeddedApp, 0o755); err != nil {
		return "", fmt.Errorf("写入主程序失败：%w", err)
	}
	_ = os.Remove(dst)
	if err := os.Rename(tmp, dst); err != nil {
		return "", fmt.Errorf("替换主程序失败：%w", err)
	}
	return dst, nil
}