//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// 打开日志目录在 Windows 上有两种典型失败方式：
//  1. 客户端以管理员身份运行（提权进程）时，explorer.exe 的请求可能被已有桌面 shell
//     忽略 —— Start() 返回成功但窗口根本不出现（用户看到的就是「点击没反应」）；
//  2. 某些加固/无桌面环境（RDP 断开、服务会话）里某一种方式被禁用。
//
// 因此按 explorer → cmd /c start → rundll32 依次尝试，每级启动后短暂等待并枚举
// 资源管理器窗口（CabinetWClass）确认窗口真的出现，再决定是否回退到下一级；
// 全部失败时返回可读错误，并把每一级的尝试记录写入 logOpenDetail 供 HTTP 接口返回。

var (
	procEnumWindows     = user32.NewProc("EnumWindows")
	procIsWindowVisible = user32.NewProc("IsWindowVisible")
	procGetWindowTextW  = user32.NewProc("GetWindowTextW")
	procGetClassNameW   = user32.NewProc("GetClassNameW")
)

// openLogFolder 在资源管理器里打开日志目录（兼容旧的布尔返回签名）。
func openLogFolder() bool {
	ok, _, _ := openLogFolderDetailed()
	return ok
}

// openLogFolderDetailed 依次尝试多种方式打开日志目录。
// 返回 (是否成功, 成功方式, 最后一次错误)，尝试记录同时写入 logOpenDetail。
func openLogFolderDetailed() (bool, string, error) {
	dir := logDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		wrapped := fmt.Errorf("创建日志目录失败：%w", err)
		logOpenSet(logOpenRecord{Attempts: []string{wrapped.Error()}, Err: wrapped})
		return false, "", wrapped
	}
	base := strings.ToLower(filepath.Base(dir))

	type step struct {
		name string
		cmd  *exec.Cmd
	}
	steps := []step{
		{name: "explorer", cmd: exec.Command("explorer.exe", dir)},
		{name: "cmd-start", cmd: exec.Command("cmd.exe", "/c", "start", "", dir)},
		{name: "rundll32", cmd: exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", dir)},
	}

	attempts := make([]string, 0, len(steps))
	var lastErr error
	for _, st := range steps {
		// explorer / rundll32 自身会创建可见窗口；HideWindow 只是避免 cmd 黑框闪一下。
		hideWindow(st.cmd)
		if err := st.cmd.Start(); err != nil {
			lastErr = fmt.Errorf("%s 启动失败：%w", st.name, err)
			attempts = append(attempts, lastErr.Error())
			continue
		}
		// 桌面 shell 进程可能常驻也可能立刻退出，后台回收避免僵尸进程。
		go func(c *exec.Cmd) { _ = c.Wait() }(st.cmd)

		if waitExplorerFolderWindow(base, 900*time.Millisecond) {
			attempts = append(attempts, st.name+" 已启动，已检测到资源管理器窗口")
			logOpenSet(logOpenRecord{Method: st.name, Attempts: attempts})
			log.Printf("已通过 %s 打开日志目录：%s", st.name, dir)
			return true, st.name, nil
		}
		lastErr = fmt.Errorf("%s 已启动但未检测到资源管理器窗口", st.name)
		attempts = append(attempts, lastErr.Error())
	}
	logOpenSet(logOpenRecord{Attempts: attempts, Err: lastErr})
	return false, "", lastErr
}

// countExplorerFolderWindows 统计可见的资源管理器窗口（窗口类 CabinetWClass）中
// 标题包含 dirName 的数量。用于确认「打开」是否真的发生。
func countExplorerFolderWindows(dirName string) int {
	want := strings.ToLower(dirName)
	count := 0
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
			return 1
		}
		var cls [64]uint16
		n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&cls[0])), uintptr(len(cls)))
		if n == 0 || !strings.EqualFold(syscall.UTF16ToString(cls[:n]), "CabinetWClass") {
			return 1
		}
		var title [512]uint16
		tn, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
		if tn == 0 {
			return 1
		}
		if strings.Contains(strings.ToLower(syscall.UTF16ToString(title[:tn])), want) {
			count++
		}
		return 1
	})
	_, _, _ = procEnumWindows.Call(cb, 0)
	return count
}

// waitExplorerFolderWindow 在超时时间内等待出现标题匹配的资源管理器窗口。
func waitExplorerFolderWindow(dirName string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if countExplorerFolderWindows(dirName) > 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(120 * time.Millisecond)
	}
}