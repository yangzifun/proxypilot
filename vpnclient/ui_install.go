package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// 安装向导：未安装时前端展示分步引导，后端负责执行安装并回报进度。

type installStep struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Done  bool   `json:"done"`
	Err   string `json:"error,omitempty"`
}

type installState struct {
	mu        sync.Mutex
	running   bool
	finished  bool
	failed    bool
	message   string
	steps     []installStep
	adapter   string
	installTo string
	startedAt int64
}

var installer = &installState{}

func (s *installState) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	steps := make([]installStep, len(s.steps))
	copy(steps, s.steps)
	return map[string]any{
		"running":    s.running,
		"finished":   s.finished,
		"failed":     s.failed,
		"message":    s.message,
		"steps":      steps,
		"adapter":    s.adapter,
		"install_to": s.installTo,
		"started_at": s.startedAt,
		"installed":  IsInstalled(),
		"default_dir": DefaultInstallDir(),
		"version":    Version,
		"is_admin":   isAdmin(),
		"platform":   platformName(),
	}
}

// setStep 更新某一步的状态。
func (s *installState) setStep(key string, done bool, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.steps {
		if s.steps[i].Key == key {
			s.steps[i].Done = done
			s.steps[i].Err = errMsg
			return
		}
	}
}

func (s *installState) current(key, label string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.message = label
	for i := range s.steps {
		if s.steps[i].Key == key && s.steps[i].Done {
			return
		}
	}
}

// GET /api/install/status
func (s *uiServer) handleInstallStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, installer.snapshot())
}

// POST /api/install/start  {dir, autostart, shortcuts}
func (s *uiServer) handleInstallStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir       string `json:"dir"`
		Autostart bool   `json:"autostart"`
		Shortcuts bool   `json:"shortcuts"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)

	installer.mu.Lock()
	if installer.running {
		installer.mu.Unlock()
		writeJSON(w, map[string]any{"ok": false, "error": "安装已在进行中"})
		return
	}
	dir := body.Dir
	if dir == "" {
		dir = DefaultInstallDir()
	}
	installer.running = true
	installer.finished = false
	installer.failed = false
	installer.message = "准备安装…"
	installer.installTo = dir
	installer.startedAt = time.Now().UnixMilli()
	installer.steps = []installStep{
		{Key: "check", Label: "检查管理员权限与环境"},
		{Key: "copy", Label: "复制程序到安装目录"},
		{Key: "driver", Label: "释放 Wintun 驱动"},
		{Key: "adapter", Label: "注册虚拟网卡 ProxyPilot VPN"},
		{Key: "registry", Label: "写入卸载登记项"},
		{Key: "firewall", Label: "配置 Windows 防火墙放行"},
		{Key: "autostart", Label: "设置开机后台运行"},
		{Key: "shortcut", Label: "创建桌面与开始菜单图标"},
	}
	installer.mu.Unlock()

	go runInstallWizard(dir, body.Autostart, body.Shortcuts)
	writeJSON(w, map[string]any{"ok": true})
}

// POST /api/install/launch —— 安装完成后启动已安装版本并退出当前进程
func (s *uiServer) handleInstallLaunch(w http.ResponseWriter, r *http.Request) {
	target := InstalledExePath()
	if _, err := os.Stat(target); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "未找到已安装程序：" + target})
		return
	}
	if err := launchProgram(target, ""); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
	go func() {
		time.Sleep(800 * time.Millisecond)
		os.Exit(0)
	}()
}

// POST /api/install/exit —— 用户放弃安装，关闭程序
func (s *uiServer) handleInstallExit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ok": true})
	go func() {
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}()
}

// runInstallWizard 按步骤执行安装并逐条回报。
func runInstallWizard(dir string, autostart, shortcuts bool) {
	fail := func(step, msg string) {
		installer.mu.Lock()
		installer.running = false
		installer.finished = true
		installer.failed = true
		installer.message = msg
		installer.mu.Unlock()
		installer.setStep(step, false, msg)
		log.Printf("安装失败（%s）：%s", step, msg)
	}

	step := func(key, label string) {
		installer.current(key, label)
		time.Sleep(180 * time.Millisecond) // 让前端能看到步骤推进
	}

	// 1) 权限与环境
	step("check", "检查管理员权限与环境")
	if !isAdmin() {
		fail("check", "需要管理员权限：请右键「以管理员身份运行」")
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail("check", "无法创建安装目录："+err.Error())
		return
	}
	installer.setStep("check", true, "")

	// 2) 写入客户端主程序
	step("copy", "写入客户端主程序")
	dst, err := installAppBinary(dir)
	if err != nil {
		fail("copy", err.Error())
		return
	}
	installer.setStep("copy", true, "")

	// 3) 驱动
	step("driver", "释放 Wintun 驱动")
	driver, err := releaseDriverTo(dir)
	if err != nil {
		fail("driver", err.Error())
		return
	}
	_ = driver
	installer.setStep("driver", true, "")

	// 4) 网卡
	step("adapter", "注册虚拟网卡 ProxyPilot VPN")
	dev, err := openTUN(defaultTunName())
	if err != nil {
		fail("adapter", "注册虚拟网卡失败："+err.Error())
		return
	}
	name := dev.Name()
	_ = dev.Close()
	installer.mu.Lock()
	installer.adapter = name
	installer.mu.Unlock()
	installer.setStep("adapter", true, "")

	// 清理旧版本残留的路由/网关
	cleanStaleTunnel()

	// 5) 卸载登记
	step("registry", "写入卸载登记项")
	if err := installWriteRegistry(dir, dst); err != nil {
		// 非致命
		log.Printf("写卸载登记失败：%v", err)
	}
	installer.setStep("registry", true, "")

	// 6) 防火墙放行
	step("firewall", "配置 Windows 防火墙放行")
	if err := installFirewallRules(dst); err != nil {
		log.Printf("配置防火墙失败（可忽略）：%v", err)
	}
	installer.setStep("firewall", true, "")

	// 7) 开机自启
	step("autostart", "设置开机后台运行")
	if autostart {
		if err := installSetAutostart(dst, true); err != nil {
			log.Printf("设置开机自启失败：%v", err)
		}
	}
	installer.setStep("autostart", true, "")

	// 8) 桌面 / 开始菜单图标
	step("shortcut", "创建桌面与开始菜单图标")
	if shortcuts {
		if err := installCreateShortcuts(dst); err != nil {
			fail("shortcut", "创建图标失败："+err.Error())
			return
		}
		log.Printf("已创建图标：%v", shortcutPaths())
	}
	installer.setStep("shortcut", true, "")

	installer.mu.Lock()
	installer.running = false
	installer.finished = true
	installer.failed = false
	installer.message = fmt.Sprintf("安装完成：%s，可从桌面/开始菜单图标启动", dir)
	installer.mu.Unlock()
	log.Printf("安装向导完成：目录=%s 网卡=%s", dir, name)
}