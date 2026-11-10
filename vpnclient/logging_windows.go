//go:build windows

package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// 客户端文件日志：GUI 版没有控制台，日志落到 %LOCALAPPDATA%\ProxyPilot\logs\client.log，
// 便于用户直接发文件排查（超过 2MB 自动滚动一次）。

var logMu sync.Mutex

// logFileOut 是当前日志文件句柄（setupFileLog 设置）。
var logFileOut *os.File

func logDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "ProxyPilot", "logs")
}

func logFilePath() string { return filepath.Join(logDir(), "client.log") }

// setupFileLog 把标准日志同时写入文件。
func setupFileLog() {
	if err := os.MkdirAll(logDir(), 0o755); err != nil {
		return
	}
	path := logFilePath()
	if st, err := os.Stat(path); err == nil && st.Size() > 2<<20 {
		_ = os.Remove(path + ".1")
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	// 空文件先写 UTF-8 BOM：否则记事本 / PowerShell 会按 GBK 解码，中文全乱码
	if st, err := f.Stat(); err == nil && st.Size() == 0 {
		_, _ = f.Write([]byte{0xEF, 0xBB, 0xBF})
	}
	logFileOut = f
	log.SetOutput(&multiWriter{file: f, stderr: os.Stderr})
	log.Printf("==== ProxyPilot %s 启动 %s ====", Version, time.Now().Format("2006-01-02 15:04:05"))
}

// logSink 供其他包在重设 log 输出时把文件一起接上。
//
// 事故：runUI 里把 log 输出改成了 io.MultiWriter(os.Stderr, ring)，
// 文件 sink 被丢掉，于是启动横幅之后的所有日志（连接过程、数据面、
// 自检）全都写到了不存在的 stderr —— 客户端日志永远是空的。
func logSink() io.Writer {
	if logFileOut == nil {
		return io.Discard
	}
	return logFileOut
}

type multiWriter struct {
	file   *os.File
	stderr *os.File
}

func (w *multiWriter) Write(p []byte) (int, error) {
	logMu.Lock()
	defer logMu.Unlock()
	_, _ = w.file.Write(p)
	return w.stderr.Write(p)
}

// diagReport 生成一份诊断报告（网卡、路由、计数），用于用户反馈问题。
func diagReport(routes []string) string {
	var b []byte
	add := func(format string, args ...any) {
		b = append(b, []byte(fmt.Sprintf(format, args...))...)
		b = append(b, '\n')
	}
	add("ProxyPilot 客户端 %s", Version)
	add("时间: %s", time.Now().Format("2006-01-02 15:04:05"))
	add("已安装: %v", IsInstalled())
	if exe, err := os.Executable(); err == nil {
		add("当前程序: %s", exe)
	}
	add("安装路径: %s", InstalledExePath())
	if name, ok := adapterStatus(); ok {
		add("虚拟网卡: %s 已注册", name)
	} else {
		add("虚拟网卡: %s 未找到", name)
	}
	add("已下发路由: %v", routes)

	add("\n--- ipconfig (ProxyPilot) ---")
	if out, err := runWin("netsh", "interface", "ipv4", "show", "config", "name="+defaultTunName()); err == nil {
		add("%s", out)
	}
	add("\n--- 路由表(0.0.0.0/1 与客户端网段) ---")
	if out, err := runWin("route", "print", "-4"); err == nil {
		for _, line := range splitLines(out) {
			if containsAny(line, "0.0.0.0", "128.0.0.0", "10.88.", "10.99.") {
				add("%s", line)
			}
		}
	}
	return string(b)
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, trimCR(cur))
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, trimCR(cur))
	}
	return out
}

func trimCR(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}