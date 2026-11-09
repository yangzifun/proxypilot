//go:build !windows

package main

import "os/exec"

// hideWindow 在非 Windows 平台无需处理。
func hideWindow(cmd *exec.Cmd) {}