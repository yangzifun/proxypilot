package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 本地模式：控制面板所在服务器无需安装 Agent，由进程内采集器/执行器直接管理。
var localPrev = struct {
	sync.Mutex
	at                  time.Time
	cpuTotal, cpuIdle   uint64
	netRx, netTx        uint64
	diskRead, diskWrite uint64
}{}

// EnsureLocalServer 启动时保证存在一条 link_mode=local 的服务器记录。
func EnsureLocalServer(ctx context.Context, db *sql.DB, log interface{ Info(string, ...any) }) int64 {
	if db == nil {
		return 0
	}
	var id int64
	err := db.QueryRowContext(ctx, `SELECT id FROM servers WHERE link_mode='local' ORDER BY id LIMIT 1`).Scan(&id)
	if err == nil {
		_, _ = db.ExecContext(ctx, `UPDATE servers SET enabled=1, allow_exec=1, agent_next_poll_sec=5, metrics_every=1, updated_at=? WHERE id=?`, nowMillis(), id)
		return id
	}
	res, err := db.ExecContext(ctx, `INSERT INTO servers
	  (name, host, username, link_mode, enabled, allow_exec, metrics_every, agent_next_poll_sec,
	   last_status, last_error, created_at, updated_at)
	  VALUES ('本机（控制服务器）', '127.0.0.1', 'root', 'local', 1, 1, 1, 5, 'online', '', ?, ?)`,
		nowMillis(), nowMillis())
	if err != nil {
		log.Info("创建本地服务器失败", "err", err)
		return 0
	}
	id, _ = res.LastInsertId()
	log.Info("已创建本地服务器（无需 Agent）", "id", id)
	return id
}

// StartLocalMonitor 每 5 秒采集一次本机指标，写入与 Agent 相同的数据结构。
func StartLocalMonitor(ctx context.Context, db *sql.DB, log interface{ Info(string, ...any) }) {
	if db == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			if id := EnsureLocalServer(ctx, db, log); id > 0 {
				runLocalSample(ctx, db, id, log)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

type localSample struct {
	summary             map[string]any
	osInfo              string
	ip, ip4, ip6        string
	services            map[string]string
	versions            map[string]string
	netRx, netTx        uint64
	diskRead, diskWrite uint64
	cpuTotal, cpuIdle   uint64
}

var localVersionCache = struct {
	sync.Mutex
	at   time.Time
	data map[string]string
}{data: map[string]string{}}

var localVersionRE = regexp.MustCompile(`\b(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)\b`)

// localComponentVersions 采集控制服务器本机组件版本（5 分钟缓存，避免每 5 秒 fork）。
func localComponentVersions() map[string]string {
	localVersionCache.Lock()
	defer localVersionCache.Unlock()
	if !localVersionCache.at.IsZero() && time.Since(localVersionCache.at) < 5*time.Minute {
		out := make(map[string]string, len(localVersionCache.data))
		for k, v := range localVersionCache.data {
			out[k] = v
		}
		return out
	}
	out := map[string]string{}
	for _, item := range []struct {
		bin, key string
		arg      string
	}{
		{"sing-box", "sing-box", "version"},
		{"xray", "xray", "version"},
		{"mtg-multi", "mtg", "version"},
		{"cloudflared", "cloudflared", "--version"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
		b, err := exec.CommandContext(ctx, item.bin, item.arg).Output()
		cancel()
		if err != nil {
			continue
		}
		if m := localVersionRE.FindStringSubmatch(string(b)); len(m) > 1 {
			out[item.key] = m[1]
		}
	}
	localVersionCache.data = out
	localVersionCache.at = time.Now()
	return out
}

func collectLocal() localSample {
	out := localSample{summary: map[string]any{}, services: map[string]string{}, versions: map[string]string{}}
	if runtime.GOOS != "linux" {
		out.summary["status"] = "online"
		return out
	}
	// CPU
	if b, err := os.ReadFile("/proc/stat"); err == nil {
		fields := strings.Fields(strings.SplitN(string(b), "\n", 2)[0])
		if len(fields) >= 5 {
			for i, f := range fields[1:] {
				v, _ := strconv.ParseUint(f, 10, 64)
				out.cpuTotal += v
				if i == 3 || i == 4 {
					out.cpuIdle += v
				}
			}
		}
	}
	// 内存
	memTotal, memAvail := uint64(0), uint64(0)
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			v, _ := strconv.ParseUint(f[1], 10, 64)
			switch f[0] {
			case "MemTotal:":
				memTotal = v * 1024
			case "MemAvailable:":
				memAvail = v * 1024
			}
		}
	}
	memUsed := uint64(0)
	if memTotal > memAvail {
		memUsed = memTotal - memAvail
	}
	// 磁盘
	diskPercent := float64(0)
	if out, err := exec.Command("df", "-P", "/").Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) >= 2 {
			f := strings.Fields(lines[1])
			if len(f) >= 5 {
				if v, err := strconv.ParseFloat(strings.TrimSuffix(f[4], "%"), 64); err == nil {
					diskPercent = v
				}
			}
		}
	}
	// 负载 / uptime
	load1, load5, load15 := 0.0, 0.0, 0.0
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		fmt.Sscanf(string(b), "%f %f %f", &load1, &load5, &load15)
	}
	uptime := int64(0)
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		var up float64
		_, _ = fmt.Sscanf(string(b), "%f", &up)
		uptime = int64(up)
	}
	// 网络（排除 lo）
	if b, err := os.ReadFile("/proc/net/dev"); err == nil {
		lines := strings.Split(string(b), "\n")
		for _, line := range lines[2:] {
			f := strings.Fields(strings.ReplaceAll(line, ":", " "))
			if len(f) < 10 || f[0] == "lo" {
				continue
			}
			rx, _ := strconv.ParseUint(f[1], 10, 64)
			tx, _ := strconv.ParseUint(f[9], 10, 64)
			out.netRx += rx
			out.netTx += tx
		}
	}
	// 磁盘 IO
	if b, err := os.ReadFile("/proc/diskstats"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 10 {
				continue
			}
			if !strings.HasPrefix(f[2], "sd") && !strings.HasPrefix(f[2], "vd") && !strings.HasPrefix(f[2], "nvme") {
				continue
			}
			r, _ := strconv.ParseUint(f[5], 10, 64)
			w, _ := strconv.ParseUint(f[9], 10, 64)
			out.diskRead += r * 512
			out.diskWrite += w * 512
		}
	}
	// 连接数 / 进程数
	connTotal, connEst := int64(0), int64(0)
	for _, p := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		if b, err := os.ReadFile(p); err == nil {
			for _, line := range strings.Split(string(b), "\n")[1:] {
				f := strings.Fields(line)
				if len(f) < 4 {
					continue
				}
				connTotal++
				if f[3] == "01" {
					connEst++
				}
			}
		}
	}
	procTotal := int64(0)
	if ents, err := os.ReadDir("/proc"); err == nil {
		for _, e := range ents {
			if e.IsDir() {
				if _, err := strconv.Atoi(e.Name()); err == nil {
					procTotal++
				}
			}
		}
	}
	osName := ""
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				osName = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
			}
		}
	}
	kernel := strings.TrimSpace(readFileTrim("/proc/sys/kernel/osrelease"))
	hostname, _ := os.Hostname()
	ip, ip4, ip6 := localIPs()
	out.ip, out.ip4, out.ip6 = ip, ip4, ip6
	// 服务状态（常见服务）
	for _, svc := range []string{"sshd", "ssh", "sing-box", "xray", "cloudflared", "mihomo", "clash", "nginx", "caddy"} {
		if _, err := exec.LookPath("systemctl"); err != nil {
			break
		}
		cmd := exec.Command("systemctl", "is-active", svc)
		b, _ := cmd.Output()
		st := strings.TrimSpace(string(b))
		if st == "" {
			st = "unknown"
		}
		if st == "unknown" {
			st = "not-installed"
		}
		out.services[svc] = st
	}

	// 计算速率
	now := time.Now()
	localPrev.Lock()
	prevAt := localPrev.at
	prevCpuTotal, prevCpuIdle := localPrev.cpuTotal, localPrev.cpuIdle
	prevNetRx, prevNetTx := localPrev.netRx, localPrev.netTx
	prevDiskRead, prevDiskWrite := localPrev.diskRead, localPrev.diskWrite
	dt := now.Sub(prevAt).Seconds()
	rates := map[string]uint64{}
	if dt > 0 && prevAt.After(time.Time{}) {
		if out.netRx >= prevNetRx {
			rates["rx"] = uint64(float64(out.netRx-prevNetRx) / dt)
		}
		if out.netTx >= prevNetTx {
			rates["tx"] = uint64(float64(out.netTx-prevNetTx) / dt)
		}
		if out.diskRead >= prevDiskRead {
			rates["dr"] = uint64(float64(out.diskRead-prevDiskRead) / dt)
		}
		if out.diskWrite >= prevDiskWrite {
			rates["dw"] = uint64(float64(out.diskWrite-prevDiskWrite) / dt)
		}
	}
	cpuPercent := float64(0)
	if prevCpuTotal > 0 && out.cpuTotal > prevCpuTotal {
		total := out.cpuTotal - prevCpuTotal
		idle := out.cpuIdle - prevCpuIdle
		if total > 0 {
			cpuPercent = float64(total-idle) / float64(total) * 100
		}
	}
	localPrev.at, localPrev.cpuTotal, localPrev.cpuIdle = now, out.cpuTotal, out.cpuIdle
	localPrev.netRx, localPrev.netTx = out.netRx, out.netTx
	localPrev.diskRead, localPrev.diskWrite = out.diskRead, out.diskWrite
	localPrev.Unlock()

	memPercent := float64(0)
	if memTotal > 0 {
		memPercent = float64(memUsed) / float64(memTotal) * 100
	}
	out.versions = localComponentVersions()
	out.summary = map[string]any{
		"status": "online", "latencyMs": 0,
		"load1": load1, "load5": load5, "load15": load15,
		"cpuPercent": cpuPercent, "memPercent": memPercent, "diskPercent": diskPercent,
		"uptimeSec": uptime, "hostname": hostname, "kernel": kernel, "os": osName,
		"platform": "local", "services": out.services, "versions": out.versions,
		"ssh": localSSHStatus(),
		"ip": out.ip, "ip4": out.ip4, "ip6": out.ip6,
		"checkedAt": now.UnixMilli(),
		"net": map[string]any{
			"rxBytes": out.netRx, "txBytes": out.netTx,
			"rxRate": rates["rx"], "txRate": rates["tx"],
		},
		"diskIo": map[string]any{
			"readBytes": out.diskRead, "writeBytes": out.diskWrite,
			"readRate": rates["dr"], "writeRate": rates["dw"],
		},
		"conn":      map[string]any{"total": connTotal, "established": connEst},
		"proc":      map[string]any{"total": procTotal},
		"memUsedMb": memUsed / 1024 / 1024, "memTotalMb": memTotal / 1024 / 1024,
		"cores": runtime.NumCPU(), "source": "local", "agentVersion": "local",
	}
	out.osInfo = strings.TrimSpace(fmt.Sprintf("%s · kernel %s", osName, kernel))
	return out
}

func readFileTrim(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

func runLocalSample(ctx context.Context, db *sql.DB, serverID int64, log interface{ Info(string, ...any) }) {
	s := collectLocal()
	b, _ := json.Marshal(s.summary)
	now := nowMillis()
	if _, err := db.ExecContext(ctx, `UPDATE servers SET last_status='online', last_error='',
	  last_check_at=?, agent_last_seen=?, agent_last_poll_at=?, agent_version='local',
	  agent_platform='local', os_info=?, last_metrics=?,
	  last_ip=COALESCE(NULLIF(?, ''), last_ip),
	  last_ip4=COALESCE(NULLIF(?, ''), last_ip4),
	  last_ip6=COALESCE(NULLIF(?, ''), last_ip6),
	  updated_at=? WHERE id=?`,
		now, now, now, s.osInfo, string(b), s.ip, s.ip4, s.ip6, now, serverID); err != nil {
		log.Info("本地指标更新失败", "err", err)
		return
	}
	servicesJSON, _ := json.Marshal(s.services)
	rawJSON, _ := json.Marshal(map[string]any{"source": "local"})
	_, err := db.ExecContext(ctx, `INSERT INTO server_metrics
	  (server_id, status, latency_ms, load1, load5, load15, cpu_percent, mem_percent,
	   mem_used_mb, mem_total_mb, disk_percent, uptime_sec, services, raw, created_at,
	   net_rx_bytes, net_tx_bytes, net_rx_rate, net_tx_rate,
	   disk_read_bytes, disk_write_bytes, disk_read_rate, disk_write_rate,
	   conn_total, conn_established, conn_timewait, proc_total)
	  VALUES (?, 'online', 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		serverID,
		f(jsonNum(s.summary["load1"])), f(jsonNum(s.summary["load5"])), f(jsonNum(s.summary["load15"])),
		f(jsonNum(s.summary["cpuPercent"])), f(jsonNum(s.summary["memPercent"])),
		jsonInt(s.summary["memUsedMb"]), jsonInt(s.summary["memTotalMb"]),
		f(jsonNum(s.summary["diskPercent"])), jsonInt(s.summary["uptimeSec"]),
		string(servicesJSON), string(rawJSON), now,
		s.netRx, s.netTx, rateOf(s.summary, "net", "rxRate"), rateOf(s.summary, "net", "txRate"),
		s.diskRead, s.diskWrite, rateOf(s.summary, "diskIo", "readRate"), rateOf(s.summary, "diskIo", "writeRate"),
		jsonInt(jsonMap(s.summary["conn"])["total"]), jsonInt(jsonMap(s.summary["conn"])["established"]),
		jsonInt(jsonMap(s.summary["proc"])["total"]))
	if err != nil {
		log.Info("本地监控采样写入失败", "err", err)
	}
}

func f(v float64) float64 { return v }

func rateOf(summary map[string]any, key, sub string) int64 {
	m, _ := summary[key].(map[string]any)
	return jsonInt(m[sub])
}

// runLocalTask 在控制服务器本机执行 Agent 同款任务。
func (h *handlers) runLocalTask(taskID int64) {
	ctx := context.Background()
	var (
		serverID, configID int64
		typ, payloadText   string
	)
	if err := h.db.QueryRowContext(ctx, `SELECT server_id, config_id, type, payload FROM agent_tasks WHERE id = ?`, taskID).
		Scan(&serverID, &configID, &typ, &payloadText); err != nil {
		return
	}
	payload := map[string]any{}
	_ = json.Unmarshal([]byte(payloadText), &payload)
	start := nowMillis()
	_, _ = h.db.ExecContext(ctx, `UPDATE agent_tasks SET status='sent', sent_at=? WHERE id=?`, start, taskID)
	ok, output, exitCode := h.execLocalTask(ctx, typ, configID, payload)
	finished := nowMillis()
	status := "failed"
	if ok {
		status = "done"
	}
	_, _ = h.db.ExecContext(ctx, `UPDATE agent_tasks SET status=?, exit_code=?, output=?, finished_at=?, sent_at=0 WHERE id=?`,
		status, exitCode, truncateRunes(output, 4000), finished, taskID)
	if typ == "deploy" && configID > 0 && ok {
		_, _ = h.db.ExecContext(ctx, `UPDATE node_configs SET applied_version=?, applied_at=?, last_push_at=?, last_push_status='success' WHERE id=?`,
			jsonInt(payload["version"]), finished, finished, configID)
	}
	logStatus := "failed"
	if ok {
		logStatus = "success"
	}
	h.recordNodeLog(ctx, configID, serverID, typ, logStatus, int64(exitCode), "本地执行："+truncateRunes(output, 200), output, "local")
}

func (h *handlers) execLocalTask(ctx context.Context, typ string, configID int64, payload map[string]any) (bool, string, int) {
	switch typ {
	case "deploy":
		path := strings.TrimSpace(jsonStr(payload["path"]))
		content := jsonStr(payload["content"])
		if path == "" {
			return false, "缺少目标路径", 1
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return false, "创建目录失败：" + err.Error(), 1
		}
		if _, err := os.Stat(path); err == nil {
			_ = os.WriteFile(path+".bak", mustRead(path), 0o644)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return false, "写入失败：" + err.Error(), 1
		}
		out := "已写入 " + path
		for _, cmd := range []string{jsonStr(payload["check_cmd"]), agentRestartCommand(payload)} {
			if strings.TrimSpace(cmd) == "" {
				continue
			}
			rc, o := runShell(ctx, cmd, 60)
			out += "\n$ " + cmd + "\n" + o
			if rc != 0 {
				return false, out, rc
			}
		}
		return true, out, 0
	case "restart":
		cmd := agentRestartCommand(payload)
		if cmd == "" {
			return false, "缺少重启命令", 1
		}
		rc, out := runShell(ctx, cmd, 60)
		return rc == 0, out, rc
	case "exec":
		cmd := jsonStr(payload["command"])
		if strings.TrimSpace(cmd) == "" {
			return false, "命令为空", 1
		}
		timeout := jsonInt(payload["timeout"])
		if timeout <= 0 {
			timeout = 30
		}
		rc, out := runShell(ctx, cmd, int(timeout))
		return rc == 0, out, rc
	case "probe":
		return true, "本地探测完成", 0
	case "update_agent":
		return true, "本地服务器无需 Agent", 0
	}
	return false, "不支持的任务类型：" + typ, 1
}

func mustRead(path string) []byte {
	b, _ := os.ReadFile(path)
	return b
}

func runShell(ctx context.Context, command string, timeoutSec int) (int, string) {
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "/bin/sh", "-c", command)
	cmd.Env = append(os.Environ(), "LANG=C.UTF-8")
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = 1
		}
	}
	if cctx.Err() == context.DeadlineExceeded {
		return 124, string(out) + "\n命令超时"
	}
	return code, string(out)
}

// localIPs 返回本机公网/网卡地址，优先非私网 IPv4，其次 IPv6。
func localIPs() (ip, ip4, ip6 string) {
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, a := range addrs {
				ipnet, ok := a.(*net.IPNet)
				if !ok || ipnet.IP == nil {
					continue
				}
				candidate := ipnet.IP
				if candidate.IsLoopback() || candidate.IsLinkLocalUnicast() {
					continue
				}
				if v4 := candidate.To4(); v4 != nil {
					cur := net.ParseIP(ip4)
					if ip4 == "" || (cur != nil && cur.IsPrivate() && !v4.IsPrivate()) {
						ip4 = v4.String()
					}
					continue
				}
				if candidate.IsGlobalUnicast() && ip6 == "" {
					ip6 = candidate.String()
				}
			}
		}
	}
	if ip4 == "" {
		if conn, err := net.Dial("udp", "1.1.1.1:80"); err == nil {
			if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP != nil {
				ip4 = addr.IP.String()
			}
			_ = conn.Close()
		}
	}
	ip = ip4
	if ip == "" {
		ip = ip6
	}
	return ip, ip4, ip6
}

// localSSHStatus 读取本机 sshd/ssh 的 active/enabled 状态，供服务器列表启停按钮使用。
func localSSHStatus() map[string]any {
	for _, unit := range []string{"ssh", "sshd"} {
		active := systemctlField("is-active", unit)
		enabled := systemctlField("is-enabled", unit)
		if active == "" {
			active = "inactive"
		}
		if enabled == "" || enabled == "not-found" {
			enabled = "not-installed"
		}
		if active == "active" || enabled != "not-installed" {
			return map[string]any{"unit": unit, "active": active, "enabled": enabled}
		}
	}
	return map[string]any{"unit": "ssh", "active": "inactive", "enabled": "not-installed"}
}

func systemctlField(action, unit string) string {
	cmd := exec.Command("systemctl", action, unit)
	b, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			_ = ee
		}
		return strings.TrimSpace(string(b))
	}
	return strings.TrimSpace(string(b))
}
