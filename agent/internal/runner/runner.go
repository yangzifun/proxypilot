// Package runner 执行下发的任务并回传结果。
//
// 语义与 V8.1 shell agent 的 run_task 完全对齐（顺序不能改，否则「先校验后替换」的安全性会丢失）：
//
//	deploy：校验路径 → 建目录 → 备份 → 写临时文件 → sha256 校验 → 原子替换 → check_cmd → restart_cmd → 记录 applied
//	restart：restart_cmd（缺省由 service_name 推导）
//	exec：command（可带超时）
//	probe：回报本机信息摘要
package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"proxypilot-agent/internal/client"
	"proxypilot-agent/internal/metrics"
	"proxypilot-agent/internal/state"
	"proxypilot-agent/internal/unlock"
)

// ShellRunner 执行一段 shell 脚本并返回（输出, 退出码）。
type ShellRunner func(ctx context.Context, script string, timeoutSec int) (string, int)

// Runner 任务执行器。
type Runner struct {
	Shell ShellRunner
	Store state.Store
	Log   func(format string, args ...any)
	Now   func() time.Time
	Probe func() string // probe 任务的输出（默认用 metrics 采集摘要）
	// InstallMissing=true 时，校验命令用到的 sing-box / mtg-multi 缺失会自动下载安装
	InstallMissing bool
	// ComponentBase 组件下载基址（服务端 poll 下发；空 = 只走 GitHub release）。
	// 自建 sing-box（带 v2ray_api）就放在 <ComponentBase>/sing-box/ 下。
	ComponentBase string
}

// DefaultShell 用 /bin/sh -c 执行脚本（Agent 目标平台为 Linux）。
func DefaultShell(ctx context.Context, script string, timeoutSec int) (string, int) {
	cctx := ctx
	if timeoutSec > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()
	}
	out, err := exec.CommandContext(cctx, "/bin/sh", "-c", script).CombinedOutput()
	if cctx.Err() == context.DeadlineExceeded {
		out = append(out, []byte("\n[proxypilot-agent] 命令超时（"+strconv.Itoa(timeoutSec)+"s）")...)
		return string(out), 1
	}
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() > 0 {
			code = ee.ExitCode()
		}
	}
	return string(out), code
}

func (r *Runner) shell() ShellRunner {
	if r.Shell != nil {
		return r.Shell
	}
	return DefaultShell
}

func (r *Runner) log(format string, args ...any) {
	if r.Log != nil {
		r.Log(format, args...)
	}
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Run 执行一条任务并返回回执结果。
func (r *Runner) Run(ctx context.Context, a client.Action) client.Result {
	var (
		exitCode int
		output   string
	)
	switch a.Type {
	case "deploy":
		exitCode, output = r.deploy(ctx, a)
	case "restart":
		exitCode, output = r.restart(ctx, a)
	case "update_agent":
		exitCode, output = r.updateAgent(ctx, a)
	case "exec":
		exitCode, output = r.exec(ctx, a)
	case "unlock":
		exitCode, output = r.unlock(ctx, a)
	case "probe":
		exitCode, output = 0, r.probe()
	case "inspect":
		exitCode, output = 0, r.inspectConfig(a.Path)
	default:
		exitCode, output = 1, "未知任务类型："+a.Type
	}
	status := "ok"
	if exitCode != 0 {
		status = "failed"
	}
	r.log("%s #%d rc=%d %s", a.Type, a.ID, exitCode, truncate(output, 120))
	return client.Result{ID: a.ID, Status: status, ExitCode: exitCode, OutputB64: client.EncodeOutput(output)}
}

func (r *Runner) restart(ctx context.Context, a client.Action) (int, string) {
	cmd := r.restartCmd(a)
	if cmd == "" {
		return 1, "缺少重启命令"
	}
	out, code := r.shell()(ctx, cmd, 0)
	if code != 0 {
		return 1, "重启命令失败（rc=" + strconv.Itoa(code) + "）：" + strings.TrimSpace(out)
	}
	return 0, "已重启：" + strings.TrimSpace(out)
}

func (r *Runner) unlock(ctx context.Context, a client.Action) (int, string) {
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = 180
	}
	out, code := r.shell()(ctx, unlock.Command(), timeout)
	if code != 0 {
		return 1, "解锁检测失败（rc=" + strconv.Itoa(code) + "）：" + strings.TrimSpace(out)
	}
	return 0, strings.TrimSpace(out)
}

func (r *Runner) exec(ctx context.Context, a client.Action) (int, string) {
	if a.Command == "" {
		return 1, "命令为空"
	}
	out, code := r.shell()(ctx, a.Command, a.Timeout)
	if code != 0 {
		return 1, strings.TrimSpace(out) + "\n命令执行失败（rc=" + strconv.Itoa(code) + "）"
	}
	return 0, strings.TrimSpace(out)
}

// applyInstallVersions 按模板要求切换组件版本（在写配置之前执行）。
// 返回说明、是否真的换了版本、错误信息。
func (r *Runner) applyInstallVersions(ctx context.Context, a client.Action) ([]string, bool, string) {
	notes := make([]string, 0, 2)
	changed := false
	for bin, want := range a.InstallVersions {
		if strings.TrimSpace(want) == "" {
			continue
		}
		code, out := r.ensureVersion(ctx, bin, want)
		if code != 0 {
			return notes, changed, out
		}
		if out != "" {
			notes = append(notes, out)
			if strings.Contains(out, "->") {
				changed = true
			}
		}
	}
	return notes, changed, ""
}

// deploy 下发配置：先校验、后替换，任何一步失败都不改动目标文件。
func (r *Runner) deploy(ctx context.Context, a client.Action) (int, string) {
	if a.Path == "" {
		return 1, "目标路径为空"
	}
	if a.DecodeErr {
		return 1, "任务内容解码失败（base64 非法），已中止以避免写入空文件"
	}
	// 二进制可能是预装或旧版 Agent 装的（没有 systemd 单元）；缺单元时 restart 会直接失败
	if r.InstallMissing {
		r.ensureServiceUnit(a)
	}
	if safePath, perr := safeConfigPath(a.Path); perr != nil {
		return 1, perr.Error()
	} else {
		a.Path = safePath
	}
	dir := filepath.Dir(a.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 1, "无法创建目录 " + dir + "：" + err.Error()
	}
	// 先切组件版本：失败时完全不动目标文件（避免「新配置 + 旧二进制」的坏组合）
	compNotes, compChanged, verr := r.applyInstallVersions(ctx, a)
	if verr != "" {
		return 1, verr
	}
	// 内容完全一致：不备份、不重写；但组件换过或服务没在跑时仍要校验 + 重启
	if cur, err := os.ReadFile(a.Path); err == nil && bytes.Equal(cur, []byte(a.Content)) {
		msg := fmt.Sprintf("内容未变化，已跳过写入（%s，v%d）", a.Path, a.Version)
		if len(compNotes) > 0 {
			msg += "\n[component] " + strings.Join(compNotes, "; ")
		}
		if compChanged {
			if a.CheckCmd != "" {
				out, code := r.shell()(ctx, a.CheckCmd, 0)
				if code != 0 {
					return 1, msg + "\n[check] " + strings.TrimSpace(out) + "\n组件已切换但校验失败"
				}
			}
			if restart := r.restartCmd(a); restart != "" {
				out, rc := r.shell()(ctx, restart, 0)
				if rc != 0 {
					return rc, msg + fmt.Sprintf("\n[restart] 组件已切换，但服务重启失败：%s", strings.TrimSpace(out))
				}
				return 0, msg + "\n[restart] 组件已切换，已重启"
			}
		}
		if svc := firstServiceName(a.ServiceName); svc != "" {
			if _, code := r.shell()(ctx, "systemctl is-active --quiet "+svc, 0); code != 0 {
				if restart := r.restartCmd(a); restart != "" {
					out, rc := r.shell()(ctx, restart, 0)
					if rc != 0 {
						return rc, fmt.Sprintf("内容未变化，但服务 %s 未运行且重启失败：%s", svc, strings.TrimSpace(out))
					}
					return 0, fmt.Sprintf("内容未变化，服务 %s 未运行，已重启（%s，v%d）", svc, a.Path, a.Version)
				}
			}
		}
		return 0, msg
	}
	if a.Backup {
		if _, err := os.Stat(a.Path); err == nil {
			backup := a.Path + ".bak." + r.now().Format("20060102150405")
			if out, code := r.shell()(ctx, "cp -a "+quote(a.Path)+" "+quote(backup), 0); code != 0 {
				r.log("备份失败（忽略）：%s", truncate(out, 120))
			}
		}
	}
	tmp := a.Path + ".pp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, []byte(a.Content), 0o644); err != nil {
		return 1, "写入临时文件失败：" + err.Error()
	}
	if a.Sha256 != "" {
		got := sha256Hex([]byte(a.Content))
		if got != a.Sha256 {
			_ = os.Remove(tmp)
			return 1, "内容校验失败（sha256 不一致）：期望 " + short(a.Sha256) + "，实际 " + short(got)
		}
	}
	if err := os.Rename(tmp, a.Path); err != nil {
		_ = os.Remove(tmp)
		return 1, "替换文件失败：" + err.Error()
	}
	if a.Backup {
		pruneBackups(a.Path, 5) // 只留最近 5 个 .bak，避免长期下发把磁盘塞满
	}
	// 校验命令用到的二进制如果不存在，先自动安装（sing-box / mtg-multi）
	if r.InstallMissing {
		if bin := firstCommandWord(a.CheckCmd); bin != "" {
			if _, err := exec.LookPath(bin); err != nil {
				if code, out := r.ensureComponent(ctx, bin); code != 0 {
					return code, out
				}
			}
		}
	}

	output := fmt.Sprintf("已下发 %s（%d 字节，v%d）", a.Path, len(a.Content), a.Version)
	if len(compNotes) > 0 {
		output += "\n[component] " + strings.Join(compNotes, "; ")
	}
	if a.CheckCmd != "" {
		out, code := r.shell()(ctx, a.CheckCmd, 0)
		output += "\n[check] " + strings.TrimSpace(out)
		if code != 0 {
			return 1, output + "\n校验命令失败（rc=" + strconv.Itoa(code) + "）"
		}
	}
	restart := r.restartCmd(a)
	if restart != "" {
		out, code := r.shell()(ctx, restart, 0)
		output += "\n[restart] " + strings.TrimSpace(out)
		if code != 0 {
			return 1, output + "\n重启命令失败（rc=" + strconv.Itoa(code) + "）"
		}
	}
	if a.ConfigID > 0 {
		if err := r.Store.SetApplied(a.ConfigID, a.Version); err != nil {
			r.log("写入 applied 失败：%v", err)
		}
	}
	return 0, output
}

// probe 回报本机信息摘要（比 shell agent 的 "probe ok" 更有用，字段与指标采集同源）。
func (r *Runner) probe() string {
	if r.Probe != nil {
		return r.Probe()
	}
	m := metrics.Collect(metrics.Options{})
	names := make([]string, 0, len(m.Services))
	for name := range m.Services {
		names = append(names, name+"="+m.Services[name])
	}
	sortStrings(names)
	return strings.Join([]string{
		"hostname=" + m.Hostname,
		"os=" + m.OS,
		"kernel=" + m.Kernel,
		"arch=" + m.Arch,
		"cores=" + strconv.Itoa(m.Cores),
		"uptime=" + strconv.FormatInt(m.UptimeSec, 10) + "s",
		"load=" + m.Load,
		"mem=" + strconv.FormatInt(m.MemUsedMB, 10) + "/" + strconv.FormatInt(m.MemTotalMB, 10) + " MB",
		"disk=" + strconv.FormatFloat(m.DiskPercent, 'f', 1, 64) + "%",
		"cpu_ticks=" + strconv.FormatInt(m.CPUTicks, 10),
		"ports=" + m.TCPPorts,
		"services=" + strings.Join(names, ","),
	}, "\n")
}

// restartCmd 重启命令：优先任务自带，其次由 service_name 推导（取逗号分隔的第一个）。
// ensureServiceUnit 在部署前确保服务的 systemd 单元存在（只针对内置引擎，自定义服务名不动）。
func (r *Runner) ensureServiceUnit(a client.Action) {
	if runtime.GOOS != "linux" {
		return
	}
	cfg := strings.TrimSpace(a.Path)
	switch firstServiceName(a.ServiceName) {
	case "sing-box":
		if cfg == "" {
			cfg = "/etc/sing-box/sb.json"
		}
		r.ensureUnit("sing-box", "/usr/local/bin/sing-box run -c "+cfg)
	case "mtg":
		if cfg == "" {
			cfg = "/etc/mtg/config.toml"
		}
		r.ensureUnit("mtg", "/usr/local/bin/mtg-multi run "+cfg, "/bin/kill -HUP $MAINPID")
	}
}

func firstServiceName(raw string) string {
	return strings.TrimSpace(strings.Split(raw, ",")[0])
}

func (r *Runner) restartCmd(a client.Action) string {
	if strings.TrimSpace(a.RestartCmd) != "" {
		return a.RestartCmd
	}
	if strings.TrimSpace(a.ServiceName) != "" {
		first := strings.TrimSpace(strings.Split(a.ServiceName, ",")[0])
		if first != "" {
			return "systemctl restart " + first
		}
	}
	return ""
}

// quote 单引号包裹（POSIX shell 安全转义）。
func quote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func short(v string) string {
	if len(v) > 12 {
		return v[:12] + "…"
	}
	return v
}

// 可注入点（测试用）：自身路径 / 重启动作 / 重启延迟
var (
	selfBinaryPath         = func() (string, error) { return os.Executable() }
	restartAgentService    = func() { _ = exec.Command("systemctl", "restart", "--no-block", "proxypilot-agent").Run() }
	selfUpdateRestartDelay = 3 * time.Second
)

// updateAgent 自更新：下载新二进制 → 校验 sha256 → 原子替换 → 延迟重启服务。
// 之所以延迟 3 秒：当前进程还要把 ack 回执发回 Worker，立刻重启会丢回执。
func (r *Runner) updateAgent(ctx context.Context, a client.Action) (int, string) {
	url := strings.TrimSpace(a.Path)
	if url == "" {
		return 1, "缺少下载地址"
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return 1, "下载地址只允许 http(s)：" + short(url)
	}
	self, err := selfBinaryPath()
	if err != nil {
		return 1, "无法定位自身可执行文件：" + err.Error()
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 1, "构造下载请求失败：" + err.Error()
	}
	hc := &http.Client{Timeout: 180 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return 1, "下载失败：" + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 1, fmt.Sprintf("下载失败：HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return 1, "读取下载内容失败：" + err.Error()
	}
	if len(body) < 1024 {
		return 1, "下载内容过小，拒绝替换（" + strconv.Itoa(len(body)) + " 字节）"
	}
	if a.Sha256 != "" {
		got := sha256Hex(body)
		if got != a.Sha256 {
			return 1, "sha256 校验失败：期望 " + short(a.Sha256) + "，实际 " + short(got)
		}
	}

	// 保留上一版便于回滚
	if old, err := os.ReadFile(self); err == nil {
		_ = os.WriteFile(self+".prev", old, 0o700)
	}
	tmp := self + ".new"
	if err := os.WriteFile(tmp, body, 0o755); err != nil {
		return 1, "写入新二进制失败：" + err.Error()
	}
	if err := os.Rename(tmp, self); err != nil {
		_ = os.Remove(tmp)
		return 1, "替换二进制失败：" + err.Error()
	}

	delay := selfUpdateRestartDelay
	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		restartAgentService()
	}()
	ver := strings.TrimSpace(a.Name)
	if ver == "" {
		ver = "最新版"
	}
	return 0, "已更新到 " + ver + "，稍后自动重启（" + strconv.Itoa(len(body)) + " 字节）"
}

// pruneBackups 保留最近 keep 个 <path>.bak.<时间戳>，其余删掉。
// 备份名按时间戳字典序递增，排序后从前删即可。
func pruneBackups(path string, keep int) {
	dir := filepath.Dir(path)
	base := filepath.Base(path) + ".bak."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), base) {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keep {
		return
	}
	sort.Strings(names)
	for _, name := range names[:len(names)-keep] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

func truncate(v string, n int) string {
	runes := []rune(v)
	if len(runes) <= n {
		return v
	}
	return string(runes[:n]) + "…"
}

func sortStrings(list []string) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j] < list[j-1]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}
