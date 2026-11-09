//go:build !windows

package main

import (
	"fmt"
	"log"
	"os"
	"time"
)

// 非 Windows 平台：日志直接走标准输出，诊断信息简化。

func setupFileLog() {
	log.Printf("==== ProxyPilot %s 启动 %s ====", Version, time.Now().Format("2006-01-02 15:04:05"))
}

func logFilePath() string { return "" }

func diagReport(routes []string) string {
	exe, _ := os.Executable()
	return fmt.Sprintf("ProxyPilot 客户端 %s\n时间: %s\n当前程序: %s\n已下发路由: %v\n",
		Version, time.Now().Format("2006-01-02 15:04:05"), exe, routes)
}