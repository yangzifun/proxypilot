//go:build windows && desktop

// 桌面版入口：Wails v3（WebView2）原生窗口 + 内置本地控制台。
//
// 界面复用 ui/index.html 与 /api/*，窗口加载本地控制台地址，
// 不弹外部浏览器、不需要单独起服务。
package main

import (
	_ "embed"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed assets/app.ico
var appIcon []byte

func main() {
	addr, err := freeLocalAddr()
	if err != nil {
		log.Fatalf("找不到可用端口：%v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		defaults := uiSettings{
			API:     "https://proxypilot.yangzifun.org",
			DNSAddr: "1.1.1.1:53",
		}
		if err := runUI(ctx, addr, defaults, false, false); err != nil {
			log.Printf("本地控制台退出：%v", err)
		}
	}()

	// 等本地服务就绪，避免窗口加载空白
	for i := 0; i < 60; i++ {
		c, derr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if derr == nil {
			_ = c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	app := application.New(application.Options{
		Name:        "ProxyPilot",
		Description: "ProxyPilot MASQUE / HTTP-3 客户端",
		Icon:        appIcon,
		Windows: application.WindowsOptions{
			WebviewUserDataPath: webviewDataDir(),
		},
		OnShutdown: func() {
			cancel()
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "ProxyPilot",
		Width:     420,
		Height:    780,
		MinWidth:  380,
		MinHeight: 640,
		URL:       "http://" + addr + "/",
	})

	if err := app.Run(); err != nil {
		log.Fatalf("窗口启动失败（WebView2 运行库是否已安装？）：%v", err)
	}
}

// freeLocalAddr 申请一个空闲的本机端口。
func freeLocalAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	return fmt.Sprintf("127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port), nil
}

// webviewDataDir 把 WebView2 用户数据放在 LocalAppData（安装到 Program Files 也能写）。
func webviewDataDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "ProxyPilot", "webview2")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}
