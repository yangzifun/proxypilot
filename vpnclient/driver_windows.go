//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows"
)

// releaseDriverTo 把内置的 wintun.dll 写到指定目录并预加载。
//
// wintun 包用 LoadLibraryEx("wintun.dll", LOAD_LIBRARY_SEARCH_APPLICATION_DIR|SYSTEM32)，
// 只认 exe 同目录与 System32，不认 WINTUN_DLL_PATH；因此安装时先把 dll 放到安装目录，
// 再用绝对路径 LoadLibrary 预加载，后续同名加载会复用已加载模块。
func releaseDriverTo(dir string) (string, error) {
	blob := wintunAMD64
	if runtime.GOARCH == "arm64" {
		blob = wintunARM64
	}
	if len(blob) == 0 {
		return "", fmt.Errorf("内置 wintun 驱动为空（架构 %s）", runtime.GOARCH)
	}
	dst := filepath.Join(dir, "wintun.dll")
	need := true
	if cur, err := os.ReadFile(dst); err == nil && len(cur) == len(blob) {
		need = false
	}
	if need {
		if err := os.WriteFile(dst, blob, 0o644); err != nil {
			return "", fmt.Errorf("释放 wintun.dll 到 %s 失败：%w", dir, err)
		}
	}
	return dst, nil
}

// preloadDriver 从指定路径预加载驱动模块（已加载则直接返回）。
func preloadDriver(dllPath string) error {
	if _, err := os.Stat(dllPath); err != nil {
		return fmt.Errorf("驱动不存在：%s", dllPath)
	}
	h, err := windows.LoadLibrary(dllPath)
	if err != nil {
		return fmt.Errorf("加载 %s 失败：%w", dllPath, err)
	}
	wintunHandle = h
	return nil
}