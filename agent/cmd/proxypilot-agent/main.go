// ProxyPilot Agent（Go 版）
//
// 用法：
//
//	proxypilot-agent run       常驻运行（systemd 服务调用；崩溃由 Restart=always 拉起）
//	proxypilot-agent once      单次运行（供 cron / systemd timer）
//	proxypilot-agent install   写入二进制/配置/systemd 单元并启动（需 root）
//	proxypilot-agent uninstall 卸载（需 root）
//	proxypilot-agent doctor    自检：配置 + 一次 poll（不执行任务、不 ack）
//	proxypilot-agent version   版本与平台信息
//
// 配置优先级：命令行参数 > 配置文件 > 默认值（环境变量 PROXYPILOT_* 由 config 包处理）。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"proxypilot-agent/internal/agent"
	"proxypilot-agent/internal/client"
	"proxypilot-agent/internal/config"
	"proxypilot-agent/internal/installer"
	"proxypilot-agent/internal/vpnnode"
)

// version 由构建脚本注入：-ldflags "-X main.version=v1.0.0"
var version = agent.Version

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cmd := "run"
	flags := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		flags = args[1:]
	}
	for _, a := range flags {
		if a == "-once" || a == "--once" {
			cmd = "once"
		}
	}
	opts, err := parseFlags(flags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[proxypilot-agent] 参数错误：%v\n", err)
		return 2
	}
	switch cmd {
	case "version", "-version", "--version":
		fmt.Printf("proxypilot-agent %s（%s/%s，go %s）\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return 0
	case "install":
		return cmdInstall(opts)
	case "uninstall":
		return cmdUninstall()
	case "doctor":
		return cmdDoctor(opts)
	case "once":
		return cmdOnce(opts)
	case "run":
		return cmdRun(opts)
	}
	fmt.Fprintf(os.Stderr, "[proxypilot-agent] 未知子命令：%s（可用：run / once / install / uninstall / doctor / version）\n", cmd)
	return 2
}

// options 命令行参数（覆盖配置文件）。
type options struct {
	confPath string
	origin   string
	key      string
	interval int
	stateDir string
	vpnConfig string
	services string
	insecure bool
}

func parseFlags(args []string) (options, error) {
	o := options{confPath: config.DefaultPath, vpnConfig: vpnnode.DefaultConfigPath}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case arg == "-once" || arg == "--once":
			// 已在上层识别为 once 子命令
		case arg == "-c" || arg == "--config":
			o.confPath = next()
		case arg == "-url" || arg == "--url":
			o.origin = next()
		case arg == "-key" || arg == "--key" || arg == "-token" || arg == "--token":
			o.key = next()
		case arg == "-interval" || arg == "--interval":
			o.interval = atoi(next())
		case arg == "-state-dir" || arg == "--state-dir":
			o.stateDir = next()
		case arg == "-vpn-config" || arg == "--vpn-config":
			o.vpnConfig = next()
		case arg == "-services" || arg == "--services":
			o.services = next()
		case arg == "-insecure" || arg == "--insecure":
			o.insecure = true
		case arg == "-h" || arg == "--help":
			usage()
		default:
			return o, fmt.Errorf("未知参数 %q", arg)
		}
	}
	return o, nil
}

func usage() {
	fmt.Print(`ProxyPilot Agent（Go 版）

用法：proxypilot-agent [子命令] [参数]

子命令：run（默认）/ once / install / uninstall / doctor / version

参数：
  -c, --config <路径>    配置文件（默认 /etc/proxypilot-agent.conf）
  --url <地址>           Worker 地址（覆盖配置）
  --key <密钥>           接入密钥 ag_xxx（覆盖配置）
  --interval <秒>         轮询间隔兜底值（10–900）
  --state-dir <路径>      状态目录（默认 /var/lib/proxypilot-agent）
  --services <列表>       需要探测的 systemd 服务（空格/逗号分隔）
  --insecure             跳过 TLS 证书校验（仅排查用）
`)
	os.Exit(0)
}

// loadConfig 合并配置文件与命令行参数，并校验。
func loadConfig(o options) (config.Config, error) {
	cfg, err := config.Load(o.confPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[proxypilot-agent] %v\n", err)
	}
	if o.origin != "" {
		cfg.Origin = strings.TrimRight(o.origin, "/")
	}
	if o.key != "" {
		cfg.Key = o.key
	}
	if o.interval > 0 {
		cfg.Interval = o.interval
	}
	if o.stateDir != "" {
		cfg.StateDir = o.stateDir
	}
	if o.services != "" {
		cfg.Services = strings.FieldsFunc(o.services, func(r rune) bool { return r == ' ' || r == ',' })
	}
	if o.insecure {
		cfg.Insecure = true
	}
	return cfg, cfg.Validate()
}

func newAgent(cfg config.Config) *agent.Agent {
	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "[proxypilot-agent] "+format+"\n", args...)
	}
	return agent.New(agent.Options{Config: cfg, Log: logf})
}

// cmdRun 常驻运行（systemd 服务）；收到 SIGINT/SIGTERM 优雅退出。
func cmdRun(o options) int {
	cfg, err := loadConfig(o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[proxypilot-agent] 配置无效：%v\n", err)
		return 2
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
	}()
	fmt.Fprintf(os.Stderr, "[proxypilot-agent] 启动：%s → %s（间隔 %ds，密钥 %s）\n", version, cfg.Origin, cfg.Interval, maskKey(cfg.Key))
	logf := func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	vpnMgr, vpnErr := vpnnode.Start(ctx, o.vpnConfig, logf)
	if vpnErr != nil {
		fmt.Fprintf(os.Stderr, "[proxypilot-agent] 内嵌 VPN 未启动：%v\n", vpnErr)
	}
	if vpnMgr != nil {
		defer vpnMgr.Stop()
	}
	if err := newAgent(cfg).Daemon(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "[proxypilot-agent] 退出：%v\n", err)
		return 1
	}
	return 0
}

// cmdOnce 单次运行（cron / systemd timer）。
func cmdOnce(o options) int {
	cfg, err := loadConfig(o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[proxypilot-agent] 配置无效：%v\n", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := newAgent(cfg).Once(ctx); err != nil {
		return 1
	}
	return 0
}

// cmdDoctor 自检：打印配置摘要 + 一次 poll（不执行任务、不 ack，绝对安全）。
func cmdDoctor(o options) int {
	cfg, err := loadConfig(o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[proxypilot-agent] 配置无效：%v\n", err)
		return 2
	}
	fmt.Printf("version   : %s（%s/%s）\n", version, runtime.GOOS, runtime.GOARCH)
	fmt.Printf("config    : %s\n", o.confPath)
	fmt.Printf("origin    : %s\n", cfg.Origin)
	fmt.Printf("key       : %s\n", maskKey(cfg.Key))
	fmt.Printf("interval  : %ds（服务端会下发 next_poll_sec 覆盖）\n", cfg.Interval)
	fmt.Printf("state_dir : %s\n", cfg.StateDir)
	fmt.Printf("services  : %s\n", strings.Join(cfg.Services, " "))
	cli := client.New(cfg.Origin, cfg.Key, version, cfg.Insecure)
	ctx, cancel := context.WithTimeout(context.Background(), client.PollTimeout)
	defer cancel()
	resp, err := cli.Poll(ctx, client.PollRequest{AgentVersion: version, Hostname: "doctor"})
	if err != nil {
		fmt.Printf("poll      : 失败 —— %v\n", err)
		return 1
	}
	actions := client.ParseWire(resp.Wire)
	fmt.Printf("poll      : 成功（服务器 %s #%d，下一轮 %ds，待执行任务 %d 条）\n", resp.ServerName, resp.ServerID, resp.NextPollSec, len(actions))
	for _, a := range actions {
		fmt.Printf("            · #%d %s（config_id=%d）\n", a.ID, a.Type, a.ConfigID)
	}
	return 0
}

// cmdInstall 安装（需 root）：写二进制/配置/单元并启用服务。
func cmdInstall(o options) int {
	if !installer.IsRoot() {
		fmt.Fprintln(os.Stderr, "[proxypilot-agent] 请用 root 执行（sudo）")
		return 2
	}
	cfg, err := loadConfig(o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[proxypilot-agent] 配置无效：%v\n", err)
		return 2
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[proxypilot-agent] 无法定位自身二进制：%v\n", err)
		return 1
	}
	if err := installer.Install("/", cfg, self, installer.DefaultRunner, logfTo(os.Stderr)); err != nil {
		fmt.Fprintf(os.Stderr, "[proxypilot-agent] 安装失败：%v\n", err)
		return 1
	}
	return 0
}

// cmdUninstall 卸载（需 root）：停服务并删除二进制/配置/单元。
func cmdUninstall() int {
	if !installer.IsRoot() {
		fmt.Fprintln(os.Stderr, "[proxypilot-agent] 请用 root 执行（sudo）")
		return 2
	}
	if err := installer.Uninstall("/", installer.DefaultRunner, logfTo(os.Stderr)); err != nil {
		fmt.Fprintf(os.Stderr, "[proxypilot-agent] 卸载失败：%v\n", err)
		return 1
	}
	return 0
}

func logfTo(w *os.File) func(string, ...any) {
	return func(format string, args ...any) {
		fmt.Fprintf(w, "[proxypilot-agent] "+format+"\n", args...)
	}
}

// maskKey 日志里的密钥脱敏（ag_1234…abcd）。
func maskKey(key string) string {
	if len(key) <= 10 {
		return key
	}
	return key[:7] + "…" + key[len(key)-4:]
}

func atoi(v string) int {
	n := 0
	for _, r := range strings.TrimSpace(v) {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

