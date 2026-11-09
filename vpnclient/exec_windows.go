//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideWindow 让子进程不弹黑窗（Windows 桌面程序必需）。
func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= 0x08000000 // CREATE_NO_WINDOW
}