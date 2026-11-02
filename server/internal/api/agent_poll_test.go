package api

import (
	"strings"
	"testing"
	"time"
)

func TestRenderNodeContent(t *testing.T) {
	vars := map[string]string{"SERVER_NAME": "jp-01", "NODES": "a\nb", "UUID": ""}
	in := "name={{SERVER_NAME}} nodes={{ NODES }} uuid={{UUID}} missing={{NOPE}}"
	got := renderNodeContent(in, vars)
	want := "name=jp-01 nodes=a\nb uuid= missing={{NOPE}}"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseAgentMetrics(t *testing.T) {
	body := map[string]any{
		"load":         []any{float64(1.5), float64(0.5), float64(0.25)},
		"uptime_sec":   float64(3600),
		"mem_used_mb":  float64(512),
		"mem_total_mb": float64(1024),
		"disk_percent": float64(42.5),
		"cpu_ticks":    float64(10000),
		"cores":        float64(4),
		"hostname":     "jp-01",
		"os":           "Ubuntu 24.04",
		"kernel":       "6.8.0",
		"platform":     "linux/amd64",
		"net":          map[string]any{"rx_bytes": float64(100), "tx_bytes": float64(200), "ifaces": float64(2)},
		"disk_io":      map[string]any{"read_bytes": float64(10), "write_bytes": float64(20)},
		"conn":         map[string]any{"total": float64(50), "established": float64(20)},
		"proc": map[string]any{
			"total": float64(120),
			"top": []any{
				map[string]any{"pid": float64(1), "name": "systemd", "ticks": float64(10), "rss_mb": float64(20)},
			},
		},
		"services": map[string]any{"sing-box": "active", "sshd": "active"},
		"versions": map[string]any{"sing-box": "1.15.0"},
		"ssh":      map[string]any{"unit": "ssh", "enabled": "enabled", "active": "active"},
	}
	m := parseAgentMetrics(body)
	if m.Load1 != 1.5 || m.Load15 != 0.25 {
		t.Fatalf("load = %v/%v/%v", m.Load1, m.Load5, m.Load15)
	}
	if m.MemPercent != 50 {
		t.Fatalf("memPercent = %v", m.MemPercent)
	}
	if m.Cores != 4 || m.ProcTotal != 120 {
		t.Fatalf("cores/proc = %d/%d", m.Cores, m.ProcTotal)
	}
	if m.Services["sing-box"] != "active" || m.Versions["sing-box"] != "1.15.0" {
		t.Fatalf("services/versions = %v/%v", m.Services, m.Versions)
	}
	if m.Sshd != "active" {
		t.Fatalf("sshd = %q", m.Sshd)
	}
	ssh, ok := m.SSH.(map[string]any)
	if !ok || ssh["active"] != "active" {
		t.Fatalf("ssh = %#v", m.SSH)
	}
	if len(m.TopProcs) != 1 || m.TopProcs[0]["name"] != "systemd" {
		t.Fatalf("top = %#v", m.TopProcs)
	}

	summary := m.summary(agentRates{NetRxRate: 7}, "1.6.0")
	netMap, _ := summary["net"].(map[string]any)
	if netMap["rxRate"].(int64) != 7 {
		t.Fatalf("summary net = %#v", netMap)
	}
	if summary["agentVersion"] != "1.6.0" {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestAgentWireLine(t *testing.T) {
	a := agentAction{
		ID: 12, Type: "deploy", ConfigID: 7,
		Payload: map[string]any{
			"name": "sb.json", "path": "/etc/sing-box/sb.json", "content": "{}",
			"sha256": "abc", "backup": true, "restart_cmd": "", "service_name": "sing-box",
			"check_cmd": "sing-box check -c {{TARGET_PATH}}", "version": float64(3),
			"command": "", "timeout": float64(0), "install_versions": "",
		},
	}
	line := agentWireLine(a)
	parts := strings.Split(line, "|")
	if len(parts) != 15 {
		t.Fatalf("字段数 = %d: %s", len(parts), line)
	}
	if parts[0] != "12" || parts[1] != "deploy" || parts[2] != "7" {
		t.Fatalf("头部 = %v", parts[:3])
	}
	if parts[6] != "abc" || parts[7] != "1" {
		t.Fatalf("sha/backup = %q/%q", parts[6], parts[7])
	}
	// restart_cmd 为空时回落到 systemctl restart <service>
	if parts[8] != b64Text("systemctl restart sing-box") {
		t.Fatalf("restart = %q", parts[8])
	}
}

func TestAgentCpuAndThroughput(t *testing.T) {
	prev := &prevSample{
		Raw:       map[string]any{"cpuTicks": float64(1000), "netRxBytes": float64(1000), "netTxBytes": float64(500)},
		CreatedAt: time.Now().Add(-1 * time.Second).UnixMilli(),
	}
	m := &agentMetrics{CpuTicks: 1100, Cores: 1, NetRxBytes: 2000, NetTxBytes: 1000}
	pct := agentCpuPercentFromSample(prev, m, 60)
	if pct != 100 {
		t.Fatalf("cpu = %v，期望 100", pct)
	}
	rates := agentThroughputFromSample(prev, m, 60)
	if rates.NetRxRate != 1000 || rates.NetTxRate != 500 {
		t.Fatalf("rates = %#v", rates)
	}
}

func TestClashUnsupportedReasonSupported(t *testing.T) {
	for _, p := range []string{"vmess", "vless", "trojan", "shadowsocks", "hysteria2", "tuic", "anytls"} {
		if got := clashUnsupportedReason("x", p); got != "" {
			t.Errorf("%s 应视为受支持，得到 %q", p, got)
		}
	}
	if got := clashUnsupportedReason("vless://a@b:443?type=quic", "vless"); got == "" {
		t.Error("QUIC 应视为不支持")
	}
}
