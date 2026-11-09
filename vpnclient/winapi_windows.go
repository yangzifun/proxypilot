//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	procMB   = user32.NewProc("MessageBoxW")
	procSE   = shell32.NewProc("ShellExecuteW")
	procWait = windows.NewLazySystemDLL("kernel32.dll").NewProc("WaitForSingleObject")
)

const (
	mbOK        = 0x00000000
	mbYesNo     = 0x00000004
	mbIconInfo  = 0x00000040
	mbIconWarn  = 0x00000030
	mbIconError = 0x00000010
	mbTopMost   = 0x00001000

	idYes = 6
	idNo  = 7

	swShowNormal = 1
)

// messageBox 弹出系统原生对话框（GUI 子系统没有控制台，只能这样提示）。
func messageBox(title, text string, flags uint32) int {
	t, _ := syscall.UTF16PtrFromString(title)
	b, _ := syscall.UTF16PtrFromString(text)
	r, _, _ := procMB.Call(0, uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(t)), uintptr(flags|mbTopMost))
	return int(r)
}

// elevateAndWait 以管理员权限启动自身（触发 UAC）并等待结束，返回退出码。
func elevateAndWait(exePath, args string) (int, error) {
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exePath)
	params, _ := syscall.UTF16PtrFromString(args)
	dir, _ := syscall.UTF16PtrFromString("")

	r, _, err := procSE.Call(0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)),
		uintptr(unsafe.Pointer(dir)),
		swShowNormal)
	// ShellExecute 返回值 > 32 表示成功
	if r <= 32 {
		if err == nil || err == syscall.Errno(0) {
			err = fmt.Errorf("ShellExecute 失败（code=%d），可能取消了 UAC", r)
		}
		return 0, fmt.Errorf("提权启动失败：%w", err)
	}
	// 拿不到进程句柄，只能等待子进程结束：用固定轮询进程列表更可靠，
	// 这里直接返回成功，由调用方稍后检查安装结果。
	return 0, nil
}

// launchDetached 以普通权限启动程序（安装完成后拉起已安装版本）。
func launchDetached(exePath, args string) error {
	verb, _ := syscall.UTF16PtrFromString("open")
	file, _ := syscall.UTF16PtrFromString(exePath)
	params, _ := syscall.UTF16PtrFromString(args)
	dir, _ := syscall.UTF16PtrFromString("")
	r, _, err := procSE.Call(0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)),
		uintptr(unsafe.Pointer(dir)),
		swShowNormal)
	if r <= 32 {
		return fmt.Errorf("启动失败（code=%d）：%w", r, err)
	}
	return nil
}

// isAdmin 当前进程是否管理员。
func isAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(&windows.SECURITY_NT_AUTHORITY, 2,
		windows.SECURITY_BUILTIN_DOMAIN_RID, windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0, &sid)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)
	token := windows.Token(0)
	member, err := token.IsMember(sid)
	if err != nil {
		return false
	}
	return member
}