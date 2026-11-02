// Package metrics 采集主机指标（供 /api/agent/poll 上报）。
//
// 设计要点：
//   - 任何读取失败都降级为 0/空，函数不返回错误（与 V8.1 shell agent 的容错口径一致）；
//   - 需要外部命令的地方（systemctl / uname）通过 Runner 注入，便于单测；
//   - 所有解析逻辑都是「字符串进、结构出」的纯函数，可在任意平台单测。
package metrics

import (
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Metrics 的 JSON 字段名必须与 Worker 的 agentMetricsFromBody 完全一致。
type Metrics struct {
	Hostname    string            `json:"hostname"`
	OS          string            `json:"os"`
	Kernel      string            `json:"kernel"`
	Platform    string            `json:"platform"`
	Arch        string            `json:"arch"`
	Cores       int               `json:"cores"`
	UptimeSec   int64             `json:"uptime_sec"`
	Load        string            `json:"load"`
	MemUsedMB   int64             `json:"mem_used_mb"`
	MemTotalMB  int64             `json:"mem_total_mb"`
	DiskPercent float64           `json:"disk_percent"`
	CPUTicks    int64             `json:"cpu_ticks"`
	Services    map[string]string `json:"services"`
	SSH         SSHState          `json:"ssh"`      // V14.2：SSH 服务是否启用（监控页只读展示）
	Versions    map[string]string `json:"versions"` // V13.5：sing-box / xray / mtg-multi 的版本（5 分钟缓存）
	TCPPorts    string            `json:"tcp_ports"`
	IP          string            `json:"ip"`
	IPv6        string            `json:"ipv6,omitempty"` // 本机首个全局 IPv6（没有则为空，V11.25）

	// V11.0：服务器监控新增采集（缺失/读不到时为零值，服务端按 0 处理）
	Net    NetStats   `json:"net"`     // 网卡累计流量（/proc/net/dev）
	DiskIO DiskIO     `json:"disk_io"` // 磁盘累计读写（/proc/diskstats）
	Conns  ConnStates `json:"conn"`    // TCP/UDP 连接状态计数（/proc/net/{tcp,tcp6,udp,udp6}）
	Procs  ProcInfo   `json:"proc"`    // 进程总数 + Top N（/proc/<pid>）
}

// Runner 执行外部命令并返回 stdout（err 非空时 stdout 仍可能是有效输出，如 systemctl is-active）。
type Runner func(name string, args ...string) (string, error)

// Options 采集参数。
type Options struct {
	Services []string // 需要探测的 systemd 服务（空则用默认列表）
	Runner   Runner   // 外部命令执行器（nil 则用默认实现）
}

// Collect 采集一次指标（永不失败）。
func Collect(opts Options) Metrics {
	r := opts.Runner
	if r == nil {
		r = DefaultRunner
	}
	m := Metrics{Services: map[string]string{}}
	if h, err := os.Hostname(); err == nil {
		m.Hostname = strings.TrimSpace(h)
	}
	m.OS = ParseOSRelease(readFile("/etc/os-release"))
	m.Kernel = kernel(r)
	m.Platform = runtime.GOOS + "/" + runtime.GOARCH
	m.Arch = unameArch(runtime.GOARCH)
	m.Cores = runtime.NumCPU()
	if m.Cores < 1 {
		m.Cores = 1
	}
	m.UptimeSec = ParseUptime(readFile("/proc/uptime"))
	m.Load = ParseLoadAvg(readFile("/proc/loadavg"))
	m.MemUsedMB, m.MemTotalMB = ParseMeminfo(readFile("/proc/meminfo"))
	m.CPUTicks = ParseCPUStat(readFile("/proc/stat"))
	m.DiskPercent = DiskPercent("/")
	names := opts.Services
	if len(names) == 0 {
		names = defaultServices()
	}
	m.Services = ServiceStates(r, names)
	m.Versions = componentVersions(r)
	m.SSH = SSHStateOf(r)
	m.TCPPorts = tcpPorts()
	m.IP = PrimaryIP()
	m.IPv6 = PrimaryIPv6()
	// V11.0：服务器监控（网络 / 磁盘 IO / 连接 / 进程 / 温度）——读不到一律留零值
	m.Net = ParseProcNetDev(readFile("/proc/net/dev"))
	m.DiskIO = ParseDiskStats(readFile("/proc/diskstats"))
	m.Conns = connStates()
	m.Procs = ListProcStats()
	return m
}

// defaultServices 不依赖 config 包，避免包间循环（与 config.DefaultServices 保持一致）。
func defaultServices() []string {
	return []string{"sing-box", "xray", "mtg", "cloudflared"}
}

// DefaultRunner 用 PATH 查找并执行命令（不经过 shell）。
func DefaultRunner(name string, args ...string) (string, error) {
	return defaultRunner(name, args...)
}

// ServiceStates 逐个执行 systemctl is-active。
// 与 shell agent 一致：命令不存在或返回空 → "unknown"；inactive/failed 等状态照样上报。
func ServiceStates(r Runner, names []string) map[string]string {
	out := map[string]string{}
	for _, name := range names {
		if !validServiceName(name) {
			continue
		}
		// 先看 LoadState：not-found 表示这台机器没装这个服务，
		// 上报成 not-installed，后台就能区分「没装」和「装了但没启动」
		if load, _ := r("systemctl", "show", "-p", "LoadState", "--value", name); strings.TrimSpace(load) == "not-found" {
			out[name] = "not-installed"
			continue
		}
		text, _ := r("systemctl", "is-active", name)
		state := strings.TrimSpace(text)
		if state == "" {
			state = "unknown"
		}
		if len(state) > 32 {
			state = state[:32]
		}
		out[name] = state
	}
	// sshd 可能叫 ssh（Debian/Ubuntu）——两者都探测，并保证 "sshd" 键存在（服务端会单独取用）
	if _, ok := out["sshd"]; !ok {
		if _, ok := out["ssh"]; !ok {
			if text, _ := r("systemctl", "is-active", "sshd"); strings.TrimSpace(text) != "" {
				out["sshd"] = strings.TrimSpace(text)
			} else {
				out["sshd"] = "unknown"
			}
		} else {
			out["sshd"] = out["ssh"]
		}
	}
	if len(out) > 40 {
		trimmed := map[string]string{}
		n := 0
		for k, v := range out {
			trimmed[k] = v
			n++
			if n >= 40 {
				break
			}
		}
		return trimmed
	}
	return out
}

func validServiceName(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '@' || r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// kernel 优先读 /proc/sys/kernel/osrelease，其次 `uname -r`。
func kernel(r Runner) string {
	if v := strings.TrimSpace(readFile("/proc/sys/kernel/osrelease")); v != "" {
		return v
	}
	if text, err := r("uname", "-r"); err == nil {
		return strings.TrimSpace(text)
	}
	return ""
}

// unameArch 把 Go 的 GOARCH 映射成 uname -m 风格（与 shell agent 上报一致）。
func unameArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "386":
		return "i686"
	case "arm":
		return "armv7l"
	case "ppc64le":
		return "ppc64le"
	case "s390x":
		return "s390x"
	case "riscv64":
		return "riscv64"
	}
	return goarch
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// versionRE 从 "sing-box version 1.14.4" / "Xray 25.9.11 ..." / "mtg-multi version 1.13.0" 里取版本号。
var versionRE = regexp.MustCompile(`\b(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)\b`)

// ParseVersionOutput 取第一行里出现的语义化版本号；解析不到返回空串。
func ParseVersionOutput(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if m := versionRE.FindStringSubmatch(line); len(m) > 1 {
			return m[1]
		}
	}
	return ""
}

var (
	versionCacheMu sync.Mutex
	versionCacheAt time.Time
	versionCache   = map[string]string{}
)

// componentVersions 读取本机已安装组件的版本；5 分钟内复用缓存，避免每轮 poll 都 fork 进程。
func componentVersions(r Runner) map[string]string {
	versionCacheMu.Lock()
	defer versionCacheMu.Unlock()
	if !versionCacheAt.IsZero() && time.Since(versionCacheAt) < 5*time.Minute {
		return cloneVersions(versionCache)
	}
	out := map[string]string{}
	for _, item := range []struct {
		bin, key, arg string
	}{
		{"sing-box", "sing-box", "version"},
		{"xray", "xray", "version"},
		{"mtg-multi", "mtg", "version"},
		{"cloudflared", "cloudflared", "--version"},
	} {
		text, _ := r(item.bin, item.arg)
		if v := ParseVersionOutput(text); v != "" {
			out[item.key] = v
		}
	}
	versionCache = out
	versionCacheAt = time.Now()
	return cloneVersions(out)
}

func cloneVersions(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// SSHState SSH 服务的启用 / 运行状态（监控页只读展示；启停操作在「服务器管理」）。
type SSHState struct {
	Unit    string `json:"unit"`
	Enabled string `json:"enabled"` // enabled / disabled / masked / static / not-installed / unknown
	Active  string `json:"active"`  // active / inactive / failed / unknown
}

// SSHStateOf 探测 sshd / ssh（发行版命名不同），只上报状态，不做任何修改。
// 注意：Ubuntu 22.10+ 默认用 ssh.socket 套接字激活，ssh.service 本身可能是 disabled，
// 只看 service 会把「实际可用的 SSH」误报成已停用，所以 service + socket 一起判断。
func SSHStateOf(r Runner) SSHState {
	for _, unit := range []string{"ssh", "sshd"} {
		if !sshUnitLoaded(r, unit) {
			continue
		}
		svcEnabled := sshUnitEnabled(r, unit)
		svcActive := sshUnitActive(r, unit)
		sockLoaded := sshUnitLoaded(r, unit+".socket")
		sockEnabled := sockLoaded && sshUnitEnabled(r, unit+".socket")
		sockActive := sockLoaded && sshUnitActive(r, unit+".socket")
		enabled := svcEnabled || sockEnabled
		active := svcActive || sockActive
		state := SSHState{Unit: unit, Enabled: "disabled", Active: "inactive"}
		if enabled {
			state.Enabled = "enabled"
		}
		if active {
			state.Active = "active"
		}
		return state
	}
	return SSHState{Unit: "", Enabled: "not-installed", Active: "not-installed"}
}

func sshUnitLoaded(r Runner, unit string) bool {
	out, _ := r("systemctl", "show", "-p", "LoadState", "--value", unit)
	return strings.TrimSpace(out) == "loaded"
}

func sshUnitEnabled(r Runner, unit string) bool {
	out, _ := r("systemctl", "is-enabled", unit)
	switch strings.TrimSpace(out) {
	case "enabled", "enabled-runtime":
		return true
	default:
		return false
	}
}

func sshUnitActive(r Runner, unit string) bool {
	out, _ := r("systemctl", "is-active", unit)
	return strings.TrimSpace(out) == "active"
}
