package metrics

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseLoadAvg(t *testing.T) {
	cases := map[string]string{
		"0.15 0.20 0.25 2/345 12345": "0.15,0.20,0.25",
		"":                           "0,0,0",
		"1.5 2.5":                    "1.5,2.5,0",
		"abc def ghi":                "0,0,0",
	}
	for in, want := range cases {
		if got := ParseLoadAvg(in); got != want {
			t.Fatalf("ParseLoadAvg(%q)=%q，期望 %q", in, got, want)
		}
	}
}

func TestParseUptime(t *testing.T) {
	if got := ParseUptime("123456.78 98765.43"); got != 123456 {
		t.Fatalf("uptime=%d", got)
	}
	if got := ParseUptime("bad"); got != 0 {
		t.Fatalf("非法输入应为 0：%d", got)
	}
}

func TestParseMeminfo(t *testing.T) {
	// 有 MemAvailable：used = total - available
	text := "MemTotal:        2048000 kB\nMemFree:          512000 kB\nMemAvailable:    1024000 kB\nBuffers: 1000 kB\nCached: 2000 kB\n"
	used, total := ParseMeminfo(text)
	if total != 2000 || used != 1000 {
		t.Fatalf("used/total=%d/%d，期望 1000/2000", used, total)
	}
	// 无 MemAvailable：经典公式
	text2 := "MemTotal: 2048000 kB\nMemFree: 1024000 kB\nBuffers: 1024 kB\nCached: 1024 kB\n"
	used2, total2 := ParseMeminfo(text2)
	if total2 != 2000 || used2 != 998 {
		t.Fatalf("回退公式 used/total=%d/%d，期望 998/2000", used2, total2)
	}
	// 负值钳制
	used3, _ := ParseMeminfo("MemTotal: 1024 kB\nMemFree: 999999 kB\n")
	if used3 != 0 {
		t.Fatalf("应钳制为 0：%d", used3)
	}
	// 缺 MemTotal
	if u, tot := ParseMeminfo("MemFree: 100 kB\n"); u != 0 || tot != 0 {
		t.Fatalf("缺 MemTotal 应返回 0/0：%d/%d", u, tot)
	}
}

func TestParseCPUStat(t *testing.T) {
	text := "cpu  100 20 30 400 5 6 7 8 9 10\ncpu0 50 10 15 200 2 3 4 5 6 7\nintr 12345\n"
	// 100+20+30+400+5+6+7 = 568（与 shell agent 的 awk $2+..+$8 一致）
	// busy = user100 + nice20 + system30 + irq6 + softirq7 + steal8 = 171（排除 idle400 / iowait5）
	if got := ParseCPUStat(text); got != 171 {
		t.Fatalf("cpu_ticks=%d，期望 171（只算忙时）", got)
	}
	if got := ParseCPUStat(""); got != 0 {
		t.Fatalf("空输入应为 0：%d", got)
	}
}

func TestParseOSRelease(t *testing.T) {
	text := "NAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\n"
	if got := ParseOSRelease(text); got != "Ubuntu 24.04.1 LTS" {
		t.Fatalf("os=%q", got)
	}
	if got := ParseOSRelease("NAME=Debian\n"); got != "" {
		t.Fatalf("无 PRETTY_NAME 应为空：%q", got)
	}
}

const procNetTCPFixture = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0
   1: 00000000:1F90 00000000:0000 01 00000000:00000000 00:00000000 00000000     0        0 12346 1 0000000000000000 100 0 0 10 0
   2: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12347 1 0000000000000000 100 0 0 10 0
`

func TestParseProcNetTCP(t *testing.T) {
	got := ParseProcNetTCP(procNetTCPFixture, false)
	want := []string{"127.0.0.1:22", "0.0.0.0:80"}
	if len(got) != len(want) {
		t.Fatalf("got=%v，期望 %v（非 LISTEN 应被忽略）", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got=%v，期望 %v", got, want)
		}
	}
}

func TestParseProcNetAddrIPv6(t *testing.T) {
	// 全零 = ::（tcp6 通配）
	if got := ParseProcNetAddr("00000000000000000000000000000000:0016", true); got != "[::]:22" {
		t.Fatalf("IPv6 通配=%q", got)
	}
	// ::ffff:127.0.0.1 = 0000000000000000FFFF00000100007F（每 32 位字小端）
	if got := ParseProcNetAddr("0000000000000000FFFF00000100007F:0050", true); got != "[127.0.0.1]:80" {
		t.Fatalf("IPv4 映射=%q", got)
	}
	if got := ParseProcNetAddr("bad", false); got != "" {
		t.Fatalf("非法字段应为空：%q", got)
	}
	if got := ParseProcNetAddr("0100007F:ZZZZ", false); got != "" {
		t.Fatalf("非法端口应为空：%q", got)
	}
}

func TestServiceStates(t *testing.T) {
	run := func(name string, args ...string) (string, error) {
		svc := args[len(args)-1]
		switch svc {
		case "xray", "sshd":
			return "active\n", nil
		case "nginx":
			return "inactive\n", errors.New("exit status 3")
		}
		return "unknown\n", errors.New("exit status 4")
	}
	got := ServiceStates(run, []string{"xray", "nginx"})
	if got["xray"] != "active" || got["nginx"] != "inactive" || got["sshd"] != "active" {
		t.Fatalf("services=%v", got)
	}
	if _, ok := got["bad;svc"]; ok {
		t.Fatalf("非法服务名应被过滤：%v", got)
	}
	// 命令缺失（空输出 + 错误）→ unknown
	empty := ServiceStates(func(string, ...string) (string, error) { return "", errors.New("not found") }, []string{"xray"})
	if empty["xray"] != "unknown" || empty["sshd"] != "unknown" {
		t.Fatalf("应回落 unknown：%v", empty)
	}
}

func TestUnameArch(t *testing.T) {
	cases := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "arm": "armv7l", "386": "i686", "mips64": "mips64"}
	for in, want := range cases {
		if got := unameArch(in); got != want {
			t.Fatalf("unameArch(%s)=%s，期望 %s", in, got, want)
		}
	}
}

func TestCollectNeverPanics(t *testing.T) {
	m := Collect(Options{Runner: func(string, ...string) (string, error) { return "", errors.New("nope") }})
	if m.Cores < 1 || m.Services == nil {
		t.Fatalf("采集结构不完整：%+v", m)
	}
	if !strings.Contains(m.Load, ",") {
		t.Fatalf("load 应为 1,5,15 形式：%q", m.Load)
	}
	if !strings.Contains(m.Platform, "/") {
		t.Fatalf("platform 应为 goos/goarch：%q", m.Platform)
	}
}

func TestServiceStatesNotInstalled(t *testing.T) {
	run := func(name string, args ...string) (string, error) {
		svc := args[len(args)-1]
		if len(args) > 0 && args[0] == "show" {
			if svc == "clash" || svc == "xray" {
				return "not-found\n", nil
			}
			return "loaded\n", nil
		}
		if svc == "sing-box" || svc == "sshd" || svc == "ssh" {
			return "active\n", nil
		}
		return "inactive\n", nil
	}
	got := ServiceStates(run, []string{"clash", "xray", "sing-box"})
	if got["clash"] != "not-installed" || got["xray"] != "not-installed" {
		t.Fatalf("未安装应上报 not-installed：%v", got)
	}
	if got["sing-box"] != "active" {
		t.Fatalf("已安装应上报运行状态：%v", got)
	}
}

func TestParseVersionOutput(t *testing.T) {
	cases := map[string]string{
		"sing-box version 1.14.4\n\nEnvironment: go1.23\n": "1.14.4",
		"Xray 25.9.11 (Xray, Penetration)\n":               "25.9.11",
		"mtg-multi version 1.13.0\n":                       "1.13.0",
		"sing-box version 1.15.0-alpha.13\n":               "1.15.0-alpha.13",
		"command not found":                                "",
		"":                                                 "",
	}
	for in, want := range cases {
		if got := ParseVersionOutput(in); got != want {
			t.Fatalf("ParseVersionOutput(%q)=%q, 期望 %q", in, got, want)
		}
	}
}

func TestComponentVersionsCached(t *testing.T) {
	versionCacheMu.Lock()
	versionCache = map[string]string{}
	versionCacheAt = time.Time{}
	versionCacheMu.Unlock()

	calls := 0
	run := func(name string, args ...string) (string, error) {
		calls++
		if name == "sing-box" {
			return "sing-box version 1.14.4", nil
		}
		return "", os.ErrNotExist
	}
	got := componentVersions(run)
	if got["sing-box"] != "1.14.4" || len(got) != 1 {
		t.Fatalf("versions=%v", got)
	}
	// 第二轮命中缓存，不应再执行命令
	before := calls
	_ = componentVersions(run)
	if calls != before {
		t.Fatalf("缓存未生效：calls %d -> %d", before, calls)
	}
	versionCacheMu.Lock()
	versionCache = map[string]string{}
	versionCacheAt = time.Time{}
	versionCacheMu.Unlock()
}

func TestSSHStateOfSocketActivation(t *testing.T) {
	// Ubuntu 22.10+：ssh.service 是 disabled，SSH 实际由 ssh.socket 激活
	calls := map[string]string{
		"systemctl show -p LoadState --value sshd":       "not-found",
		"systemctl show -p LoadState --value ssh":        "loaded",
		"systemctl is-enabled ssh":                       "disabled",
		"systemctl is-active ssh":                        "inactive",
		"systemctl show -p LoadState --value ssh.socket": "loaded",
		"systemctl is-enabled ssh.socket":                "enabled",
		"systemctl is-active ssh.socket":                 "active",
	}
	run := func(name string, args ...string) (string, error) {
		return calls[name+" "+strings.Join(args, " ")], nil
	}
	got := SSHStateOf(run)
	if got.Enabled != "enabled" || got.Active != "active" {
		t.Fatalf("socket 激活时应为 enabled/active：%+v", got)
	}
}

func TestSSHStateOfRunningButDisabled(t *testing.T) {
	calls := map[string]string{
		"systemctl show -p LoadState --value ssh":        "loaded",
		"systemctl is-enabled ssh":                       "disabled",
		"systemctl is-active ssh":                        "active",
		"systemctl show -p LoadState --value ssh.socket": "not-found",
	}
	run := func(name string, args ...string) (string, error) {
		return calls[name+" "+strings.Join(args, " ")], nil
	}
	got := SSHStateOf(run)
	if got.Enabled != "disabled" || got.Active != "active" {
		t.Fatalf("运行但未自启应为 disabled/active：%+v", got)
	}
}

func TestSSHStateOf(t *testing.T) {
	calls := map[string]string{
		"systemctl show -p LoadState --value sshd": "not-found",
		"systemctl show -p LoadState --value ssh":  "loaded",
		"systemctl is-enabled ssh":                 "disabled",
		"systemctl is-active ssh":                  "inactive",
	}
	run := func(name string, args ...string) (string, error) {
		return calls[name+" "+strings.Join(args, " ")], nil
	}
	got := SSHStateOf(run)
	if got.Unit != "ssh" || got.Enabled != "disabled" || got.Active != "inactive" {
		t.Fatalf("ssh 状态=%+v", got)
	}
	// 两个都没有 → not-installed
	none := func(string, ...string) (string, error) { return "not-found", nil }
	if s := SSHStateOf(none); s.Enabled != "not-installed" {
		t.Fatalf("未安装时应为 not-installed：%+v", s)
	}
}
