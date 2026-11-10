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
	"strings"
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

	setupFileLog()      // GUI 无控制台，日志落 %LOCALAPPDATA%\ProxyPilot\logs\client.log
	cleanStaleTunnel() // 清理上次异常退出残留的路由/网关

	if installerBuild {
		// 这是安装程序：只做安装，永远不进主界面
		runInstallerApp()
		return
	}
	// 主程序：必须已安装才能使用
	if !ensureInstalledForApp() {
		return
	}

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
		Description: "ProxyPilot VPN 客户端（自研 HTTP/3 隧道）",
		Icon:        appIcon,
		Windows: application.WindowsOptions{
			WebviewUserDataPath: webviewDataDir(),
			// 关闭窗口后进程继续留在托盘
			DisableQuitOnLastWindowClosed: true,
		},
		OnShutdown: func() {
			// 退出前先断开隧道（同步等待完成），确保路由/DNS 被清理干净
			if err := localPost(addr, "/api/disconnect", nil); err != nil {
				log.Printf("退出前断开失败：%v", err)
			}
			cancel()
		},
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "ProxyPilot v" + Version,
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
	// showWindow 统一的「把窗口带到前台」逻辑。
	//
	// 只调 Show() 在三种情况下无效：窗口被最小化、WebView 因效率模式挂起、
	// 以及窗口在屏幕外。这里依次做 还原 → 显示 → 居中(首次) → 置顶抖一下 → 聚焦。
	var centered bool
	showWindow := func() {
		if window.IsMinimised() {
			window.UnMinimise()
		}
		window.Restore()
		window.Show()
		if !centered {
			window.Center()
			centered = true
		}
		// Windows 前台锁：先置顶再取消，强制把窗口提到最前
		window.SetAlwaysOnTop(true)
		window.SetAlwaysOnTop(false)
		window.Focus()
	}

	menu.Add("显示窗口").OnClick(func(*application.Context) {
		showWindow()
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
		showWindow()
	})
	menu.Add("断开").OnClick(func(*application.Context) {
		if err := localPost(addr, "/api/disconnect", nil); err != nil {
			log.Printf("托盘断开失败：%v", err)
			return
		}
	})
	menu.AddSeparator()
	menu.Add("打开设置").OnClick(func(*application.Context) {
		showWindow()
		// 前端监听 hash 变化切到首选项
		window.ExecJS(`location.hash = '#settings'`)
	})
	menu.Add("退出").OnClick(func(*application.Context) {
		// 先同步断开，再退出进程（否则残留 /1 路由会让整机断网）
		if err := localPost(addr, "/api/disconnect", nil); err != nil {
			log.Printf("退出前断开失败：%v", err)
		}
		time.Sleep(600 * time.Millisecond) // 给清理脚本一点时间
		cancel()
		app.Quit()
	})
	tray.SetMenu(menu)

	// 左键单击托盘：切换窗口显示。
	//
	// 不用 tray.ToggleWindow()：它依赖 tray.AttachWindow() 挂接的窗口，
	// 未挂接时 attachedWindow.Window == nil，函数直接 return —— 表现为「点了没反应」。
	tray.OnClick(func() {
		if window.IsVisible() && !window.IsMinimised() {
			window.Hide()
			return
		}
		showWindow()
	})

	// 关闭窗口 = 隐藏到托盘（不退出进程）
	hideOnClose := func(e *application.WindowEvent) {
		e.Cancel()   // 先取消关闭，保持窗口对象存活
		window.Hide()
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

// ensureInstalled 保证当前运行的是已安装版本。
//
// 返回 true 表示可以继续启动；false 表示已处理（提示/安装/退出），直接返回即可。
func ensureInstalled() bool {
	if IsInstalled() && samePath(selfPath(), InstalledExePath()) {
		return true
	}
	// 已经在安装目录里但缺卸载项（例如手动复制）→ 直接补一次安装
	if samePath(selfPath(), InstalledExePath()) {
		res, err := RunInstall(InstallOptions{Dir: DefaultInstallDir(), Autostart: true})
		if err != nil {
			messageBox("ProxyPilot 安装未完成", "注册虚拟网卡失败：\n"+err.Error()+"\n\n请以管理员身份重新安装。", mbOK|mbIconError)
			return false
		}
		_ = res
		return true
	}

	// 从下载目录直接运行：先征得同意再安装
	choice := messageBox("ProxyPilot 需要先安装",
		"检测到当前是未安装的临时程序。\n\n"+
			"安装会：\n"+
			"  · 复制程序到 "+DefaultInstallDir()+"\n"+
			"  · 安装 Wintun 驱动并注册「ProxyPilot VPN」虚拟网卡\n"+
			"  · 写入开机后台运行（托盘）\n\n"+
			"现在安装吗？", mbYesNo|mbIconInfo)
	if choice != idYes {
		return false
	}

	args := "-install -autostart"
	if isAdmin() {
		// 已是管理员：直接在当前进程内完成
		if _, err := RunInstall(InstallOptions{Dir: DefaultInstallDir(), Autostart: true}); err != nil {
			messageBox("安装失败", err.Error(), mbOK|mbIconError)
			return false
		}
	} else {
		if _, err := elevateAndWait(selfPath(), args); err != nil {
			messageBox("安装失败", err.Error(), mbOK|mbIconError)
			return false
		}
		// 等提权进程把文件写完（最多 20 秒）
		for i := 0; i < 40; i++ {
			if IsInstalled() {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

	if !IsInstalled() {
		messageBox("安装未完成", "未检测到已安装的文件或卸载登记项。\n请运行安装包里的 install.cmd 重试。", mbOK|mbIconError)
		return false
	}

	// 拉起已安装版本，让当前这份退出
	if err := launchDetached(InstalledExePath(), ""); err != nil {
		messageBox("启动失败", "已安装，但启动失败："+err.Error()+"\n\n请从开始菜单打开 ProxyPilot。", mbOK|mbIconError)
	}
	return false
}

// selfPath 当前可执行文件绝对路径。
func selfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}

// samePath 忽略大小写的路径比较。
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// runInstallerApp 安装程序入口：启动本地控制台并强制显示安装向导。
func runInstallerApp() {
	addr, err := freeLocalAddr()
	if err != nil {
		log.Fatalf("找不到可用端口：%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		defaults := uiSettings{API: "https://proxypilot.yangzifun.org", DNSAddr: defaultDNSAddr}
		if err := runUI(ctx, addr, defaults, false, false); err != nil {
			log.Printf("本地控制台退出：%v", err)
		}
	}()

	for i := 0; i < 60; i++ {
		c, derr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if derr == nil {
			_ = c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	app := application.New(application.Options{
		Name:        "ProxyPilot 安装程序",
		Description: "安装 ProxyPilot VPN 客户端",
		Icon:        appIcon,
		Windows: application.WindowsOptions{
			WebviewUserDataPath: webviewDataDir(),
		},
		OnShutdown: func() { cancel() },
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "ProxyPilot v" + Version + " 安装程序",
		Width:     520,
		Height:    760,
		MinWidth:  480,
		MinHeight: 620,
		URL:       "http://" + addr + "/?installer=1",
	})

	if err := app.Run(); err != nil {
		log.Fatalf("安装程序启动失败：%v", err)
	}
}

// ensureInstalledForApp 主程序必须已安装：未安装时提示运行安装包。
func ensureInstalledForApp() bool {
	if IsInstalled() && samePath(selfPath(), InstalledExePath()) {
		return true
	}
	messageBox("ProxyPilot v"+Version,
		"未检测到已安装的 ProxyPilot。\n\n"+
			"请先运行安装程序（ProxyPilot-"+Version+"-windows-amd64.exe）完成安装，\n"+
			"安装后从开始菜单或桌面快捷方式启动客户端。",
		mbOK|mbIconInfo)
	return false
}
