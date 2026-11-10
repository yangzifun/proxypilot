package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"
)

// 崩溃现场落盘。
//
// 为什么需要：客户端是 GUI 程序，从开始菜单启动时没有控制台，
// Go 的 panic / fatal error 默认写到 stderr —— 那里没人接，
// 于是用户看到的就是「点一下就闪退」，事件查看器里也查不到东西。
//
// Go 1.24 起提供 debug.SetCrashOutput：进程因运行时致命错误退出时，
// 会把完整的 goroutine 栈写到指定文件。我们把它指到日志目录下的
// crash.log，下次闪退就能直接看到是哪一行炸的。
//
// 注意：SetCrashOutput 只能设置一次，返回的 stop 要在退出时调用。
func setupCrashLog() {
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "ProxyPilot", "logs")
	if dir == filepath.Join("ProxyPilot", "logs") {
		dir = "logs"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(dir, "crash.log")

	// 每次启动截断，只保留本次运行的崩溃记录，避免无限增长
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	// 写一行启动标记，便于确认文件是本次运行创建的
	_, _ = fmt.Fprintf(f, "==== ProxyPilot %s 启动 %s ====\n", Version,
		time.Now().Format("2006-01-02 15:04:05"))

	debug.SetTraceback("all")
	debug.SetCrashOutput(f, debug.CrashOptions{})
}

