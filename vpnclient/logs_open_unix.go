//go:build !windows

package main

import "os"

// openLogFolder 非 Windows 平台暂不打开文件管理器。
func openLogFolder() bool {
	if p := logFilePath(); p != "" {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}