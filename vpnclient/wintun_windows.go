//go:build windows

package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"golang.org/x/sys/windows"
)

// 内置 Wintun 驱动：运行时按架构释放到本地目录并预加载。
//
//go:embed assets/wintun-amd64.dll
var wintunAMD64 []byte

//go:embed assets/wintun-arm64.dll
var wintunARM64 []byte

var (
	wintunOnce sync.Once
	wintunErr  error
	// wintunHandle 保持模块常驻，不能被 GC/释放，否则后续调用会再次去磁盘找 dll
	wintunHandle windows.Handle
)

// ensureWintun 保证 wintun.dll 已在进程内加载：
//
//	wintun 包用 LoadLibraryEx("wintun.dll", LOAD_LIBRARY_SEARCH_APPLICATION_DIR)
//	只会找 exe 同目录与 System32，不认 WINTUN_DLL_PATH。
//	所以这里把内置 dll 释放到本地目录后用 LoadLibrary 绝对路径预加载——
//	同名模块一旦已在进程内，后续 LoadLibraryEx("wintun.dll") 会直接复用。
func ensureWintun() error {
	wintunOnce.Do(func() {
		exe, _ := os.Executable()
		// 1) exe 同目录已有（安装包放置）时直接用
		if exe != "" {
			local := filepath.Join(filepath.Dir(exe), "wintun.dll")
			if _, err := os.Stat(local); err == nil {
				h, lerr := windows.LoadLibrary(local)
				if lerr != nil {
					wintunErr = fmt.Errorf("加载同目录 wintun.dll 失败：%w", lerr)
					return
				}
				wintunHandle = h
				return
			}
		}
		// 2) 释放内置驱动到缓存目录再预加载
		blob := wintunAMD64
		if runtime.GOARCH == "arm64" {
			blob = wintunARM64
		}
		if len(blob) == 0 {
			wintunErr = fmt.Errorf("内置 wintun 驱动为空（架构 %s）", runtime.GOARCH)
			return
		}
		base := os.TempDir()
		if cache, err := os.UserCacheDir(); err == nil && cache != "" {
			base = cache
		}
		dir := filepath.Join(base, "ProxyPilot", "wintun", runtime.GOARCH)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			wintunErr = fmt.Errorf("创建驱动目录失败：%w", err)
			return
		}
		dll := filepath.Join(dir, "wintun.dll")
		if cur, statErr := os.ReadFile(dll); statErr != nil || len(cur) != len(blob) {
			if err := os.WriteFile(dll, blob, 0o644); err != nil {
				wintunErr = fmt.Errorf("释放 wintun.dll 失败：%w", err)
				return
			}
		}
		h, err := windows.LoadLibrary(dll)
		if err != nil {
			wintunErr = fmt.Errorf("预加载 wintun.dll 失败：%w", err)
			return
		}
		wintunHandle = h
	})
	return wintunErr
}