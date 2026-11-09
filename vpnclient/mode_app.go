//go:build !installer || !windows || !desktop

package main

// installerBuild 为 false 时，本二进制是客户端主程序：必须已安装才可运行。
const installerBuild = false

// embeddedAppName 主程序写入安装目录后的文件名。
const embeddedAppName = "ProxyPilot.exe"