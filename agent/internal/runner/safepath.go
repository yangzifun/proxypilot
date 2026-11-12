package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// configRoots 允许 Agent 读写的目录白名单（root 权限下的最小暴露面）。
var configRoots = []string{
	"/etc/sing-box/",
	"/etc/mihomo/",
	"/etc/mtg/",
	"/etc/systemd/system/",
}

func withinRoots(abs string) bool {
	for _, root := range configRoots {
		if abs == strings.TrimSuffix(root, "/") || strings.HasPrefix(abs, root) {
			return true
		}
	}
	return false
}

// safeConfigPathFn 是 deploy 做路径校验时实际调用的函数。
//
// 生产代码路径恒等 safeConfigPath。抽成变量只为单测注入临时目录白名单：
// Windows 上不存在 /etc 前缀，deploy 的「校验/重启失败回滚」分支无法直接跑通。
var safeConfigPathFn = safeConfigPath

// safeConfigPath 校验下发路径：绝对化、白名单前缀、拒绝符号链接。
func safeConfigPath(p string) (string, error) {
	clean := strings.TrimSpace(p)
	if clean == "" {
		return "", errors.New("路径为空")
	}
	abs, err := filepath.Abs(filepath.Clean(clean))
	if err != nil {
		return "", errors.New("路径非法")
	}
	if !withinRoots(abs) {
		return "", errors.New("路径不在允许目录内")
	}
	if fi, err := os.Lstat(abs); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("目标为符号链接，拒绝访问")
	}
	return abs, nil
}

// safeReadConfigFile 读取白名单内的普通文件，并限制大小。
func safeReadConfigFile(p string, maxBytes int64) ([]byte, error) {
	abs, err := safeConfigPath(p)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, errors.New("文件不存在或不可读")
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("不是普通文件，拒绝读取")
	}
	if maxBytes > 0 && st.Size() > maxBytes {
		return nil, errors.New("文件超过大小上限")
	}
	return os.ReadFile(abs)
}
