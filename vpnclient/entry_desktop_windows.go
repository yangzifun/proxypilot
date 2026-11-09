//go:build windows && desktop

// 桌面版入口：Wails v3（WebView2）原生窗口 + 内置本地控制台 + 系统托盘。
//
// 运行形态：
//   - 默认：打开窗口；关闭窗口不退出，隐藏到托盘继续后台运行
//   - -background / -tray：启动即隐藏，只在托盘（安装时勾选「开机后台运行」用它）
//   - 托盘菜单：显示窗口 / 连接 / 断开 / 应用设置 / 退出
package main

import (
	_ "embed"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed assets/app.ico
var appIcon []byte

func main() {
	background := flag.Bool("background", false, "启动后只在系统托盘运行，不显示窗口")
	trayOnly := flag.Bool("tray", false, "同 -background")
	flag.Parse()
	startHidden := *background || *trayOnly

	// 允许与安装/卸载/自检共用同一个 exe：这些参数已在 CLI 入口处理，
	// 桌面版这里只需忽略未知参数带来的误判。
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
			// 关闭窗口后进程继续留在托盘
			DisableQuitOnLastWindowClosed: true,
		},
		OnShutdown: func() {
			cancel()
		},
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "ProxyPilot",
		Width:     420,
		Height:    780,
		MinWidth:  380,
		MinHeight: 640,
		URL:       "http://" + addr + "/",
		Hidden:    startHidden,
	})

	// ---------- 系统托盘 ----------
	tray := app.SystemTray.New()
	tray.SetIcon(appIcon)
	tray.SetTooltip("ProxyPilot VPN")

	menu := app.NewMenu()
	menu.Add("显示窗口").OnClick(func(*application.Context) {
		window.Show()
		window.Focus()
	})
	menu.Add("隐藏到托盘").OnClick(func(*application.Context) {
		window.Hide()
	})
	menu.AddSeparator()
	menu.Add("连接").OnClick(func(*application.Context) {
		if err := localPost(addr, "/api/connect", map[string]any{"node_id": 0}); err != nil {
			log.Printf("托盘连接失败：%v", err)
			return
		}
		window.Show()
	})
	menu.Add("断开").OnClick(func(*application.Context) {
		if err := localPost(addr, "/api/disconnect", nil); err != nil {
			log.Printf("托盘断开失败：%v", err)
			return
		}
	})
	menu.AddSeparator()
	menu.Add("打开设置").OnClick(func(*application.Context) {
		window.Show()
		window.Focus()
		// 前端监听 hash 变化切到首选项
		window.ExecJS(`location.hash = '#settings'`)
	})
	menu.Add("退出").OnClick(func(*application.Context) {
		cancel()
		app.Quit()
	})
	tray.SetMenu(menu)

	// 左键单击托盘：切换窗口显示
	tray.OnClick(func() {
		tray.ToggleWindow()
	})

	// 关闭窗口 = 隐藏到托盘（不退出进程）
	hideOnClose := func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	}
	window.OnWindowEvent(events.Common.WindowClosing, hideOnClose)
	window.OnWindowEvent(events.Windows.WindowClosing, hideOnClose)

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

// localPost 调用内置控制台的本地接口（托盘菜单用）。
func localPost(addr, path string, body map[string]any) error {
	var buf *bytes.Reader
	if body == nil {
		buf = bytes.NewReader([]byte("{}"))
	} else {
		raw, _ := json.Marshal(body)
		buf = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+path, buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}