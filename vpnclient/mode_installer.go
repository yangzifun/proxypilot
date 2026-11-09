//go:build windows && desktop && installer

package main

// installerBuild 为 true 时，本二进制只做安装：
//   - 启动即进入安装向导，不进入主界面
//   - 安装时写入内置的客户端主程序（而不是自身）
//   - 安装完成后可拉起已安装的客户端并退出
const installerBuild = true

// embeddedAppName 安装时写入安装目录的文件名。
const embeddedAppName = "ProxyPilot.exe"