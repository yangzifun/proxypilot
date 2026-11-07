package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	regexpIP   = regexp.MustCompile(`^[0-9a-fA-F:.]+$`)
	regexpIPv4 = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{1,3}){3}$`)
)

const (
	agentPollThrottleMS  = 5000
	agentTaskMaxPending  = 20
	agentTaskMaxAttempts = 3
	agentTaskExpireMS    = 24 * 3600 * 1000
	agentActionsPerPoll  = 5
)

// ---------------- 指标解析 ----------------

type agentMetrics struct {
	Load1, Load5, Load15          float64
	UptimeSec                     int64
	MemUsedMb, MemTotalMb         int64
	MemPercent                    float64
	DiskPercent                   float64
	CpuTicks                      int64
	CpuPercent                    float64
	Cores                         int64
	OS, Kernel, Hostname          string
	Platform, Arch, Sshd          string
	TcpPorts                      string
	Services                      map[string]string
	Versions                      map[string]string
	SSH                           any
	NetRxBytes, NetTxBytes        int64
	NetRxPackets, NetTxPackets    int64
	NetRxErrs, NetTxErrs          int64
	NetIfaces                     int64
	DiskReadBytes, DiskWriteBytes int64
	DiskReadOps, DiskWriteOps     int64
	ConnTotal, ConnEstablished    int64
	ConnListen, ConnTimeWait      int64
	ConnCloseWait, ConnSynSent    int64
	ProcTotal                     int64
	TopProcs                      []map[string]any
}

func jsonNum(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	case uint64:
		return float64(x)
	}
	return 0
}

func jsonInt(v any) int64 { return int64(jsonNum(v)) }

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func jsonMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func jsonSlice(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

func parseAgentMetrics(body map[string]any) *agentMetrics {
	m := &agentMetrics{
		Services: map[string]string{}, Versions: map[string]string{},
		TopProcs: []map[string]any{},
	}
	if load := jsonSlice(body["load"]); load != nil {
		if len(load) > 0 {
			m.Load1 = jsonNum(load[0])
		}
		if len(load) > 1 {
			m.Load5 = jsonNum(load[1])
		}
		if len(load) > 2 {
			m.Load15 = jsonNum(load[2])
		}
	} else if s := jsonStr(body["load"]); s != "" {
		parts := strings.Split(s, ",")
		if len(parts) > 0 {
			m.Load1 = jsonNum(parts[0])
		}
		if len(parts) > 1 {
			m.Load5 = jsonNum(parts[1])
		}
		if len(parts) > 2 {
			m.Load15 = jsonNum(parts[2])
		}
	}
	m.UptimeSec = jsonInt(body["uptime_sec"])
	m.MemUsedMb = jsonInt(body["mem_used_mb"])
	m.MemTotalMb = jsonInt(body["mem_total_mb"])
	m.DiskPercent = jsonNum(body["disk_percent"])
	m.CpuTicks = jsonInt(body["cpu_ticks"])
	m.CpuPercent = jsonNum(body["cpu_percent"])
	m.Cores = jsonInt(body["cores"])
	if m.Cores < 1 {
		m.Cores = 1
	}
	m.OS = jsonStr(body["os"])
	m.Kernel = jsonStr(body["kernel"])
	m.Hostname = jsonStr(body["hostname"])
	m.Platform = jsonStr(body["platform"])
	m.Arch = jsonStr(body["arch"])
	m.TcpPorts = jsonStr(body["tcp_ports"])
	if m.MemTotalMb > 0 {
		m.MemPercent = float64(int64(float64(m.MemUsedMb)/float64(m.MemTotalMb)*1000)) / 10
	}

	net := jsonMap(body["net"])
	m.NetRxBytes = jsonInt(net["rx_bytes"])
	m.NetTxBytes = jsonInt(net["tx_bytes"])
	m.NetRxPackets = jsonInt(net["rx_packets"])
	m.NetTxPackets = jsonInt(net["tx_packets"])
	m.NetRxErrs = jsonInt(net["rx_errs"])
	m.NetTxErrs = jsonInt(net["tx_errs"])
	m.NetIfaces = jsonInt(net["ifaces"])

	dio := jsonMap(body["disk_io"])
	m.DiskReadBytes = jsonInt(dio["read_bytes"])
	m.DiskWriteBytes = jsonInt(dio["write_bytes"])
	m.DiskReadOps = jsonInt(dio["reads"])
	m.DiskWriteOps = jsonInt(dio["writes"])

	cn := jsonMap(body["conn"])
	m.ConnTotal = jsonInt(cn["total"])
	m.ConnEstablished = jsonInt(cn["established"])
	m.ConnListen = jsonInt(cn["listen"])
	m.ConnTimeWait = jsonInt(cn["time_wait"])
	m.ConnCloseWait = jsonInt(cn["close_wait"])
	m.ConnSynSent = jsonInt(cn["syn_sent"])

	pr := jsonMap(body["proc"])
	m.ProcTotal = jsonInt(pr["total"])
	for i, item := range jsonSlice(pr["top"]) {
		if i >= 5 {
			break
		}
		p := jsonMap(item)
		m.TopProcs = append(m.TopProcs, map[string]any{
			"pid": jsonInt(p["pid"]), "name": truncateRunes(jsonStr(p["name"]), 64),
			"ticks": jsonInt(p["ticks"]), "rssMb": jsonInt(p["rss_mb"]),
		})
	}

	if svc := jsonMap(body["services"]); len(svc) > 0 {
		n := 0
		for k, v := range svc {
			if n >= 40 {
				break
			}
			m.Services[truncateRunes(k, 64)] = truncateRunes(jsonStr(v), 32)
			n++
		}
	}
	if vers := jsonMap(body["versions"]); len(vers) > 0 {
		n := 0
		for k, v := range vers {
			if n >= 20 {
				break
			}
			val := strings.TrimSpace(jsonStr(v))
			if val != "" {
				m.Versions[truncateRunes(k, 64)] = truncateRunes(val, 32)
			}
			n++
		}
	}
	if ssh := jsonMap(body["ssh"]); len(ssh) > 0 {
		m.SSH = map[string]any{
			"unit": truncateRunes(jsonStr(ssh["unit"]), 32), "enabled": truncateRunes(jsonStr(ssh["enabled"]), 32),
			"active": truncateRunes(jsonStr(ssh["active"]), 32),
		}
	}
	if v, ok := m.Services["sshd"]; ok {
		m.Sshd = v
	}
	return m
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

type prevSample struct {
	Raw       map[string]any
	CreatedAt int64
}

func (h *handlers) agentPrevMetric(ctx context.Context, serverID int64) *prevSample {
	var raw sql.NullString
	var createdAt int64
	err := h.db.QueryRowContext(ctx,
		`SELECT raw, created_at FROM server_metrics WHERE server_id = ? ORDER BY id DESC LIMIT 1`, serverID).
		Scan(&raw, &createdAt)
	if err != nil || !raw.Valid {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw.String), &m) != nil {
		return nil
	}
	return &prevSample{Raw: m, CreatedAt: createdAt}
}

func agentCpuPercentFromSample(prev *prevSample, m *agentMetrics, intervalSec int64) float64 {
	if prev == nil || prev.Raw == nil {
		return 0
	}
	prevTicks := int64(jsonNum(prev.Raw["cpuTicks"]))
	if prevTicks > 0 && m.CpuTicks > prevTicks {
		seconds := float64(time.Now().UnixMilli()-prev.CreatedAt) / 1000
		if seconds < 1 {
			seconds = float64(intervalSec)
		}
		if seconds < 1 {
			seconds = 60
		}
		cores := m.Cores
		if cores < 1 {
			cores = 1
		}
		pct := (float64(m.CpuTicks-prevTicks) / (float64(cores) * seconds * 100)) * 1000
		return math.Min(100, math.Round(pct)/10)
	}
	return 0
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

type agentRates struct {
	NetRxRate, NetTxRate, DiskReadRate, DiskWriteRate int64
}

func agentThroughputFromSample(prev *prevSample, m *agentMetrics, intervalSec int64) agentRates {
	var out agentRates
	if prev == nil || prev.Raw == nil {
		return out
	}
	seconds := int64((time.Now().UnixMilli() - prev.CreatedAt) / 1000)
	if seconds < 1 {
		seconds = intervalSec
	}
	if seconds < 1 {
		seconds = 60
	}
	rate := func(cur int64, key string) int64 {
		prevVal, ok := prev.Raw[key]
		if !ok {
			return 0
		}
		prevInt := int64(jsonNum(prevVal))
		if cur >= prevInt {
			return (cur - prevInt) / seconds
		}
		return 0
	}
	out.NetRxRate = rate(m.NetRxBytes, "netRxBytes")
	out.NetTxRate = rate(m.NetTxBytes, "netTxBytes")
	out.DiskReadRate = rate(m.DiskReadBytes, "diskReadBytes")
	out.DiskWriteRate = rate(m.DiskWriteBytes, "diskWriteBytes")
	return out
}

func (m *agentMetrics) summary(rates agentRates, agentVersion string) map[string]any {
	return map[string]any{
		"status": "online", "latencyMs": 0,
		"load1": m.Load1, "load5": m.Load5, "load15": m.Load15,
		"cpuPercent": m.CpuPercent, "memPercent": m.MemPercent, "diskPercent": m.DiskPercent,
		"uptimeSec": m.UptimeSec, "hostname": m.Hostname, "kernel": m.Kernel, "os": m.OS,
		"platform": m.Platform, "services": m.Services, "versions": m.Versions,
		"ssh": m.SSH, "sshd": m.Sshd, "checkedAt": time.Now().UnixMilli(),
		"net": map[string]any{
			"rxBytes": m.NetRxBytes, "txBytes": m.NetTxBytes,
			"rxRate": rates.NetRxRate, "txRate": rates.NetTxRate,
			"rxPackets": m.NetRxPackets, "txPackets": m.NetTxPackets,
			"rxErrs": m.NetRxErrs, "txErrs": m.NetTxErrs, "ifaces": m.NetIfaces,
		},
		"diskIo": map[string]any{
			"readBytes": m.DiskReadBytes, "writeBytes": m.DiskWriteBytes,
			"readRate": rates.DiskReadRate, "writeRate": rates.DiskWriteRate,
			"readOps": m.DiskReadOps, "writeOps": m.DiskWriteOps,
		},
		"conn": map[string]any{
			"total": m.ConnTotal, "established": m.ConnEstablished, "listen": m.ConnListen,
			"timeWait": m.ConnTimeWait, "closeWait": m.ConnCloseWait, "synSent": m.ConnSynSent,
		},
		"proc":      map[string]any{"total": m.ProcTotal, "top": m.TopProcs},
		"memUsedMb": m.MemUsedMb, "memTotalMb": m.MemTotalMb, "cores": m.Cores,
		"source": "agent", "agentVersion": agentVersion,
	}
}

// ---------------- 快照与采样 ----------------

func (h *handlers) agentUpdateSnapshot(ctx context.Context, srv *agentServer, m *agentMetrics,
	touchPoll bool,
	opts struct {
		NextPollSec    int64
		PersistPollSec int64
		PollCount      int64
		AgentVersion   string
		IP, IP4, IP6   string
		PrivateIP      string
		MeshIP4, MeshIP6 string
	}, rates agentRates) error {
	now := nowMillis()
	summary := m.summary(rates, opts.AgentVersion)
	summaryJSON, _ := json.Marshal(summary)

	osInfo := ""
	parts := []string{m.OS}
	if m.Kernel != "" {
		parts = append(parts, "kernel "+m.Kernel)
	}
	if m.Arch != "" {
		parts = append(parts, m.Arch)
	}
	for _, p := range parts {
		if p != "" {
			osInfo = strings.Join(parts, " · ")
			break
		}
	}
	if osInfo == "" {
		osInfo = srv.OSInfo
	}
	platform := firstNonEmpty(m.Platform, srv.AgentPlatform)
	version := firstNonEmpty(opts.AgentVersion, srv.AgentVersion)

	pollFlag := 0
	if touchPoll {
		pollFlag = 1
	}
	_, err := h.db.ExecContext(ctx, `UPDATE servers SET last_status = ?, last_error = ?, last_check_at = ?,
	      last_metrics = ?, os_info = ?, link_mode = ?, agent_platform = ?, agent_version = ?,
	      agent_last_seen = ?, agent_last_poll_at = IF(?, ?, agent_last_poll_at),
	      agent_next_poll_sec = COALESCE(NULLIF(?, 0), agent_next_poll_sec), agent_poll_count = IF(?, ?, agent_poll_count),
	      last_ip = COALESCE(NULLIF(?, ''), last_ip),
	      last_ip4 = COALESCE(NULLIF(?, ''), last_ip4),
	      last_ip6 = COALESCE(NULLIF(?, ''), last_ip6),
      last_ip_private = COALESCE(NULLIF(?, ''), last_ip_private),
      last_mesh_ip4 = COALESCE(NULLIF(?, ''), last_mesh_ip4),
      last_mesh_ip6 = COALESCE(NULLIF(?, ''), last_mesh_ip6),
	      agent_key_used_at = COALESCE(NULLIF(agent_key_used_at, 0), ?),
	      updated_at = ?
	    WHERE id = ?`,
		"online", "", now, string(summaryJSON), osInfo, "agent", platform, version,
		now, pollFlag, now, opts.PersistPollSec, pollFlag, opts.PollCount,
		opts.IP, opts.IP4, opts.IP6, opts.PrivateIP, opts.MeshIP4, opts.MeshIP6, now, now, srv.ID)
	if err == nil && srv.LastStatus == "offline" {
		go h.notifyStatus(context.Background(), "服务器恢复", []serverStatusNotice{{ID: srv.ID, Detail: "Agent 心跳恢复"}})
	}
	return err
}

func (h *handlers) insertServerMetric(ctx context.Context, serverID int64, m *agentMetrics, rates agentRates) {
	servicesJSON, _ := json.Marshal(m.Services)
	rawJSON, _ := json.Marshal(map[string]any{
		"cpuTicks": m.CpuTicks, "cores": m.Cores, "source": "agent",
		"netRxBytes": m.NetRxBytes, "netTxBytes": m.NetTxBytes,
		"diskReadBytes": m.DiskReadBytes, "diskWriteBytes": m.DiskWriteBytes,
	})
	_, err := h.db.ExecContext(ctx, `INSERT INTO server_metrics
	  (server_id, status, latency_ms, load1, load5, load15, cpu_percent, mem_percent,
	   mem_used_mb, mem_total_mb, disk_percent, uptime_sec, services, raw, created_at,
	   net_rx_bytes, net_tx_bytes, net_rx_rate, net_tx_rate, disk_read_bytes, disk_write_bytes,
	   disk_read_rate, disk_write_rate, conn_total, conn_established, conn_timewait, proc_total)
	  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		serverID, "online", 0, m.Load1, m.Load5, m.Load15, m.CpuPercent, m.MemPercent,
		m.MemUsedMb, m.MemTotalMb, m.DiskPercent, m.UptimeSec, string(servicesJSON), string(rawJSON), nowMillis(),
		m.NetRxBytes, m.NetTxBytes, rates.NetRxRate, rates.NetTxRate,
		m.DiskReadBytes, m.DiskWriteBytes, rates.DiskReadRate, rates.DiskWriteRate,
		m.ConnTotal, m.ConnEstablished, m.ConnTimeWait, m.ProcTotal)
	if err != nil {
		h.log.Warn("写入监控采样失败", "err", err)
		return
	}
	h.accrueServerTraffic(ctx, serverID, m.NetRxBytes, m.NetTxBytes)
}

// ---------------- 任务队列与 wire ----------------

type agentAction struct {
	ID       int64
	Type     string
	ConfigID int64
	Payload  map[string]any
}

func b64Text(s string) string {
	if s == "" {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func agentRestartCommand(p map[string]any) string {
	if rc := strings.TrimSpace(jsonStr(p["restart_cmd"])); rc != "" {
		return rc
	}
	if svc := strings.TrimSpace(jsonStr(p["service_name"])); svc != "" {
		return "systemctl restart " + strings.TrimSpace(strings.Split(svc, ",")[0])
	}
	return ""
}

func agentWireLine(a agentAction) string {
	p := a.Payload
	backup := "0"
	if b, ok := p["backup"].(bool); ok && b {
		backup = "1"
	}
	return strings.Join([]string{
		fmt.Sprint(a.ID), a.Type, fmt.Sprint(a.ConfigID),
		b64Text(jsonStr(p["name"])), b64Text(jsonStr(p["path"])), b64Text(jsonStr(p["content"])),
		jsonStr(p["sha256"]), backup, b64Text(agentRestartCommand(p)),
		b64Text(jsonStr(p["check_cmd"])), b64Text(jsonStr(p["service_name"])),
		fmt.Sprint(jsonInt(p["version"])), b64Text(jsonStr(p["command"])),
		fmt.Sprint(jsonInt(p["timeout"])), b64Text(jsonStr(p["install_versions"])),
	}, "|")
}

func agentActionsJSON(actions []agentAction) []map[string]any {
	out := make([]map[string]any, 0, len(actions))
	for _, a := range actions {
		p := a.Payload
		base := map[string]any{"id": a.ID, "type": a.Type, "config_id": a.ConfigID}
		switch a.Type {
		case "deploy":
			base["name"] = jsonStr(p["name"])
			base["version"] = jsonInt(p["version"])
			base["path"] = jsonStr(p["path"])
			base["sha256"] = jsonStr(p["sha256"])
			base["backup"] = p["backup"] == true
			base["service_name"] = jsonStr(p["service_name"])
			base["restart_cmd"] = agentRestartCommand(p)
			base["check_cmd"] = jsonStr(p["check_cmd"])
		case "exec":
			base["command"] = jsonStr(p["command"])
			base["timeout"] = jsonInt(p["timeout"])
		case "unlock":
			base["timeout"] = jsonInt(p["timeout"])
		}
		out = append(out, base)
	}
	return out
}

func (h *handlers) agentQueueTask(ctx context.Context, srv *agentServer, typ string, configID int64, payload map[string]any, actor string) (int64, bool, string) {
	payloadText, _ := json.Marshal(payload)
	sum := sha256Hex(typ + "|" + strconv.FormatInt(configID, 10) + "|" + string(payloadText))
	var dupID int64
	if err := h.db.QueryRowContext(ctx,
		`SELECT id FROM agent_tasks WHERE server_id = ? AND payload_hash = ? AND status IN ('pending','sent') LIMIT 1`,
		srv.ID, sum).Scan(&dupID); err == nil {
		return dupID, true, ""
	}
	var count int64
	_ = h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_tasks WHERE server_id = ? AND status IN ('pending','sent')`, srv.ID).Scan(&count)
	if count >= agentTaskMaxPending {
		return 0, false, fmt.Sprintf("待执行任务已达上限（%d 条）", agentTaskMaxPending)
	}
	res, err := h.db.ExecContext(ctx,
		`INSERT INTO agent_tasks (server_id, config_id, type, payload, payload_hash, status, actor, created_at)
		 VALUES (?, ?, ?, ?, ?, 'pending', ?, ?)`,
		srv.ID, configID, typ, string(payloadText), sum, actor, nowMillis())
	if err != nil {
		return 0, false, "任务入队失败：" + err.Error()
	}
	id, _ := res.LastInsertId()
	if strings.EqualFold(srv.LinkMode, "local") {
		go h.runLocalTask(id)
	} else {
		wakeAgent(srv.ID)
	}
	return id, false, ""
}

func (h *handlers) agentCollectActions(ctx context.Context, srv *agentServer, body map[string]any) []agentAction {
	appliedMap := map[int64]int64{}
	for _, item := range jsonSlice(body["applied"]) {
		m := jsonMap(item)
		cid := jsonInt(m["config_id"])
		if cid <= 0 {
			continue
		}
		v := jsonInt(m["version"])
		if v > appliedMap[cid] {
			appliedMap[cid] = v
		}
		if len(appliedMap) >= 50 {
			break
		}
	}

	configs, err := h.listNodeConfigs(ctx, srv.ID)
	if err == nil {
		for i := range configs {
			cfg := &configs[i]
			applied := cfg.Applied
			if v, ok := appliedMap[cfg.ID]; ok && v > applied {
				applied = v
			}
			if cfg.Version <= applied {
				continue
			}
			payload := h.agentBuildDeployPayload(ctx, cfg, srv)
			if _, _, msg := h.agentQueueTask(ctx, srv, "deploy", cfg.ID, payload, "auto"); msg != "" {
				h.log.Warn("Agent deploy 入队失败", "err", msg, "config", cfg.ID)
			}
		}
	}

	rows, err := h.db.QueryContext(ctx,
		`SELECT id, type, COALESCE(config_id,0), COALESCE(payload,'') FROM agent_tasks
		  WHERE server_id = ? AND status = 'pending' ORDER BY id ASC LIMIT ?`, srv.ID, agentActionsPerPoll)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var actions []agentAction
	for rows.Next() {
		var (
			id       int64
			typ      string
			configID int64
			payload  string
		)
		if rows.Scan(&id, &typ, &configID, &payload) != nil {
			continue
		}
		var p map[string]any
		if json.Unmarshal([]byte(payload), &p) != nil {
			p = map[string]any{}
		}
		if typ == "exec" && srv.AllowExec != 1 && !isSystemExecPayload(p) {
			_, _ = h.db.ExecContext(ctx,
				`UPDATE agent_tasks SET status = 'failed', finished_at = ?, output = ? WHERE id = ?`,
				nowMillis(), "未开启「允许执行命令」（allow_exec）", id)
			h.recordNodeLog(ctx, configID, srv.ID, "agent-exec", "failed", 0, "任务被拒绝：未开启 allow_exec", "", "agent")
			continue
		}
		_, _ = h.db.ExecContext(ctx,
			`UPDATE agent_tasks SET status = 'sent', sent_at = ? WHERE id = ?`, nowMillis(), id)
		actions = append(actions, agentAction{ID: id, Type: typ, ConfigID: configID, Payload: p})
	}
	return actions
}

func (h *handlers) agentReclaimStaleTasks(ctx context.Context, serverID int64) {
	cutoff := nowMillis() - 10*60*1000
	_, _ = h.db.ExecContext(ctx,
		`UPDATE agent_tasks SET status = 'pending', attempts = attempts + 1, sent_at = 0
		  WHERE server_id = ? AND status = 'sent' AND sent_at != 0 AND sent_at < ? AND attempts + 1 < ?`,
		serverID, cutoff, agentTaskMaxAttempts)
	_, _ = h.db.ExecContext(ctx,
		`UPDATE agent_tasks SET status = 'failed', finished_at = ?, output = '任务未回执（超过 10 分钟）'
		  WHERE server_id = ? AND status = 'sent' AND sent_at != 0 AND sent_at < ? AND attempts + 1 >= ?`,
		nowMillis(), serverID, cutoff, agentTaskMaxAttempts)
}

func (h *handlers) agentExpireTasks(ctx context.Context, serverID int64) {
	cutoff := nowMillis() - agentTaskExpireMS
	if serverID > 0 {
		_, _ = h.db.ExecContext(ctx,
			`UPDATE agent_tasks SET status = 'expired', finished_at = ?
			  WHERE server_id = ? AND status = 'pending' AND created_at < ?`, nowMillis(), serverID, cutoff)
	} else {
		_, _ = h.db.ExecContext(ctx,
			`UPDATE agent_tasks SET status = 'expired', finished_at = ?
			  WHERE status = 'pending' AND created_at < ?`, nowMillis(), cutoff)
	}
}

func (h *handlers) recordNodeLog(ctx context.Context, configID, serverID int64, action, status string, exitCode int64, message, output, actor string) {
	_, err := h.db.ExecContext(ctx,
		`INSERT INTO node_push_logs (config_id, server_id, action, status, exit_code, message, output, duration_ms, actor, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		configID, serverID, action, status, exitCode, truncateRunes(message, 2000), truncateRunes(output, 4000), actor, nowMillis())
	if err != nil {
		h.log.Warn("写入下发日志失败", "err", err)
	}
}

// ---------------- poll ----------------

// POST /api/agent/poll
func (h *handlers) agentPoll(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil || body == nil {
		body = map[string]any{}
	}
	srv, status, msg := h.agentAuthenticate(r, body)
	if srv == nil {
		writeError(w, status, msg)
		return
	}
	now := nowMillis()
	if srv.AgentLastPollAt > 0 && now-srv.AgentLastPollAt < agentPollThrottleMS {
		retry := (agentPollThrottleMS - (now - srv.AgentLastPollAt)) / 1000
		if retry < 1 {
			retry = 1
		}
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "轮询过于频繁，请稍后重试", "retry_after": retry})
		return
	}
	if srv.LastIP == "unknown" {
		_, _ = h.db.ExecContext(r.Context(), `UPDATE servers SET last_ip = '' WHERE id = ?`, srv.ID)
	}

	configured := clampInt(srv.NextPollSec, agentPollMinSec, agentPollMaxSec)
	nextPollSec := configured
	if !agentVersionAtLeast(srv.AgentVersion, "1.5.5") && nextPollSec > 10 {
		nextPollSec = 10
	}
	metricsEvery := clampInt(srv.MetricsEvery, 1, 10)
	pollCount := srv.AgentPollCount + 1

	m := parseAgentMetrics(body)
	prev := h.agentPrevMetric(r.Context(), srv.ID)
	m.CpuPercent = agentCpuPercentFromSample(prev, m, nextPollSec)

	shouldSample := pollCount%metricsEvery == 0 || srv.LastStatus != "online"

	ip := clientIP(r)
	if ip == "" || ip == "unknown" {
		mine := strings.TrimSpace(jsonStr(body["ip"]))
		if regexpIP.MatchString(mine) {
			ip = truncateRunes(mine, 64)
		}
	}
	// Agent 主动上报的出口公网 IP：走内网通道时，连接来源是 100.64/10 等内网地址，
	// 必须靠这个字段才能拿到真正的公网 IP。
	agentPublicIP := strings.TrimSpace(jsonStr(body["public_ip"]))
	agentPrivateIP := strings.TrimSpace(jsonStr(body["private_ip"]))
	if agentPrivateIP == "" {
		agentPrivateIP = strings.TrimSpace(jsonStr(body["ip"]))
	}
	// 组网地址（Cloudflare WARP）：Agent 直接从组网网卡读取，v4/v6 各一
	meshIP4 := ""
	meshIP6 := ""
	{
		v4 := strings.TrimSpace(jsonStr(body["mesh_ip4"]))
		if regexpIPv4.MatchString(v4) {
			meshIP4 = truncateRunes(v4, 64)
		}
		v6 := strings.TrimSpace(jsonStr(body["mesh_ip6"]))
		if strings.Contains(v6, ":") && regexpIP.MatchString(v6) {
			meshIP6 = truncateRunes(v6, 64)
		}
	}

	// 内网地址：优先 Agent 上报的本机内网 IP，其次连接来源（WARP / 内网）
	privateIP := ""
	if agentPrivateIP != "" && isPrivateIP(agentPrivateIP) && regexpIP.MatchString(agentPrivateIP) {
		privateIP = truncateRunes(agentPrivateIP, 64)
	} else if ip != "" && isPrivateIP(ip) && regexpIP.MatchString(ip) {
		privateIP = truncateRunes(ip, 64)
	}
	// 公网 IPv4：Agent 主动探测 > 连接来源；都拿不到时留空，保留库里已有的公网 IP
	ip4 := firstNonEmpty(publicIPv4(agentPublicIP), publicIPv4(ip))
	agentPublicV6 := ""
	if ip4 == "" && agentPublicIP != "" && strings.Contains(agentPublicIP, ":") &&
		regexpIP.MatchString(agentPublicIP) && !isPrivateIP(agentPublicIP) {
		agentPublicV6 = agentPublicIP
	}
	if ip4 == "" {
		mine := strings.TrimSpace(jsonStr(body["ipv4"]))
		if mine == "" {
			mine = strings.TrimSpace(jsonStr(body["ip"]))
		}
		if regexpIPv4.MatchString(mine) && !isPrivateIP(mine) &&
			!strings.HasPrefix(mine, "0.") && !strings.HasPrefix(mine, "127.") &&
			!strings.HasPrefix(mine, "169.254.") {
			ip4 = mine
		}
	}
	ip6 := agentPublicV6
	if ip6 == "" {
		mine := strings.TrimSpace(jsonStr(body["ipv6"]))
		if strings.Contains(mine, ":") && regexpIP.MatchString(mine) && !isPrivateIP(mine) {
			ip6 = mine
		}
	}

	// 最终落库值：拿不到新公网 IP 时保留旧的公网 IP；若旧值是内网地址则清空（修复历史脏数据）
	if ip4 == "" {
		if !isPrivateIP(srv.LastIP4) {
			ip4 = strings.TrimSpace(srv.LastIP4)
		}
	}
	if ip6 == "" && !isPrivateIP(srv.LastIP6) {
		ip6 = strings.TrimSpace(srv.LastIP6)
	}
	if privateIP == "" {
		privateIP = strings.TrimSpace(srv.LastIPPrivate)
	}
	if meshIP4 == "" {
		meshIP4 = strings.TrimSpace(srv.LastMeshIP4)
	}
	if meshIP6 == "" {
		meshIP6 = strings.TrimSpace(srv.LastMeshIP6)
	}

	rates := agentThroughputFromSample(prev, m, nextPollSec)
	agentVersion := firstNonEmpty(jsonStr(body["agent_version"]), jsonStr(body["agentVersion"]))
	if err := h.agentUpdateSnapshot(r.Context(), srv, m, true, struct {
		NextPollSec    int64
		PersistPollSec int64
		PollCount      int64
		AgentVersion   string
		IP, IP4, IP6   string
		PrivateIP      string
		MeshIP4, MeshIP6 string
	}{nextPollSec, configured, pollCount, agentVersion, ip, ip4, ip6, privateIP, meshIP4, meshIP6}, rates); err != nil {
		h.log.Error("更新 Agent 快照失败", "err", err)
	}

	if shouldSample {
		h.insertServerMetric(r.Context(), srv.ID, m, rates)
	}
	// V15.6：sing-box 统计快照（用户流量 / 连接明细），失败不影响心跳
	h.agentIngestStats(r.Context(), srv.ID, body["stats"])
	if pollCount <= 1 || pollCount%5 == 0 {
		h.agentReclaimStaleTasks(r.Context(), srv.ID)
		h.agentExpireTasks(r.Context(), srv.ID)
	}

	actions := h.agentCollectActions(r.Context(), srv, body)
	var shell any
	if sess, ok := h.pendingShellSession(r.Context(), srv.ID); ok {
		shell = map[string]any{"session": sess, "min_agent": "1.3.0"}
		// 有终端在等：立刻唤醒已连 WS 的 Agent 去拨号
		wakeAgent(srv.ID)
	}
	wireParts := make([]string, 0, len(actions))
	for _, a := range actions {
		wireParts = append(wireParts, agentWireLine(a))
	}

	// V15.6：告诉 Agent 该采哪些节点的统计（Agent 自己读盘解析 sb.json 的 experimental）
	statsTargets := make([]map[string]any, 0, 1)
	if rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT id, COALESCE(sb_path,'') AS sb_path FROM singbox_nodes
		  WHERE server_id = ? AND enabled = 1 ORDER BY id ASC`, srv.ID); err == nil {
		for _, row := range rows {
			statsTargets = append(statsTargets, map[string]any{
				"node_id": mapInt(row, "id", 0), "sb_path": mapStr(row, "sb_path", ""),
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "server_id": srv.ID, "server_name": srv.Name,
		"next_poll_sec": nextPollSec, "metrics_every": metricsEvery,
		"agent_access_url":  h.settingValue(r.Context(), settingAgentAccessURL),
		"agent_internal_url": h.settingValue(r.Context(), settingAgentInternalURL),
		"server_time": now, "agent_script_version": agentScriptVersion,
		"shell": shell, "actions": agentActionsJSON(actions),
		"stats_targets":     statsTargets,
		"agent_binary_base": h.agentBinaryBaseURL(),
		"component_base":    h.componentBaseURL(),
		"wire":              strings.Join(wireParts, "~"),
	})
}

// POST /api/agent/ack
func (h *handlers) agentAck(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil || body == nil {
		body = map[string]any{}
	}
	srv, status, msg := h.agentAuthenticate(r, body)
	if srv == nil {
		writeError(w, status, msg)
		return
	}
	now := nowMillis()
	results := jsonSlice(body["results"])
	if len(results) > 20 {
		results = results[:20]
	}
	handled := make([]map[string]any, 0, len(results))

	for _, item := range results {
		m := jsonMap(item)
		taskID := jsonInt(m["id"])
		if taskID <= 0 {
			continue
		}
		var (
			typ, taskPayload string
			configID         int64
			taskStatus       string
			attempts         int64
		)
		err := h.db.QueryRowContext(r.Context(),
			`SELECT COALESCE(type,''), COALESCE(config_id,0), COALESCE(payload,''), COALESCE(status,''), COALESCE(attempts,0)
			   FROM agent_tasks WHERE id = ? AND server_id = ? LIMIT 1`, taskID, srv.ID).
			Scan(&typ, &configID, &taskPayload, &taskStatus, &attempts)
		if err == sql.ErrNoRows {
			handled = append(handled, map[string]any{"id": taskID, "ok": false, "error": "任务不存在"})
			continue
		}
		if err != nil {
			h.log.Error("查询任务失败", "err", err)
			continue
		}
		if taskStatus == "done" || taskStatus == "failed" || taskStatus == "expired" {
			handled = append(handled, map[string]any{"id": taskID, "ok": true, "note": "任务已处理（幂等跳过）"})
			continue
		}

		var payload map[string]any
		_ = json.Unmarshal([]byte(taskPayload), &payload)
		if payload == nil {
			payload = map[string]any{}
		}
		exitCode := jsonInt(m["exit_code"])
		if exitCode < 0 {
			exitCode = 0
		}
		if exitCode > 255 {
			exitCode = 255
		}
		ok := jsonStr(m["status"]) == "ok" || (exitCode == 0 && jsonStr(m["status"]) != "failed")
		output := truncateRunes(decodeAgentOutput(m), 2000)

		depService := strings.TrimSpace(jsonStr(payload["service"]))
		missingBin := !ok && (exitCode == 127 || strings.Contains(strings.ToLower(output), "command not found"))
		optionalDep := depService == "" || strings.Contains(strings.ToLower(depService), "masque") ||
			strings.Contains(strings.ToLower(depService), "caddy")
		skipMissing := missingBin && optionalDep

		newAttempts := attempts
		if !ok {
			newAttempts++
		}
		finalStatus := "done"
		if !ok {
			switch {
			case skipMissing:
				finalStatus = "skipped"
			case newAttempts >= agentTaskMaxAttempts:
				finalStatus = "failed"
			default:
				finalStatus = "pending"
			}
		}
		finishedAt := int64(0)
		if ok {
			finishedAt = now
		}
		_, _ = h.db.ExecContext(r.Context(),
			`UPDATE agent_tasks SET status = ?, attempts = ?, exit_code = ?, output = ?, finished_at = ?, sent_at = 0
			  WHERE id = ?`, finalStatus, newAttempts, exitCode,
			firstNonEmpty(output, map[bool]string{true: "ok", false: ""}[ok]), finishedAt, taskID)

		if ok && typ == "deploy" && configID > 0 {
			version := jsonInt(payload["version"])
			_, _ = h.db.ExecContext(r.Context(),
				`UPDATE node_configs SET applied_version = ?, applied_at = ?, last_push_at = ?,
				   last_push_status = 'success', last_push_message = ?, updated_at = ?
				 WHERE id = ?`,
				version, now, now, truncateRunes(fmt.Sprintf("Agent 已应用 v%d", version), 200), now, configID)
			// 节点上线并应用后，复核该服务器上 sing-box 节点的三向一致性（最终一致）
			h.verifyServerNodesAsync(r.Context(), srv.ID)
		}

		logStatus := "failed"
		logMessage := ""
		switch {
		case ok:
			logStatus = "success"
			logMessage = "Agent 执行成功"
			if typ == "deploy" {
				logMessage += fmt.Sprintf("（v%d）", jsonInt(payload["version"]))
			}
		case skipMissing:
			logStatus = "skipped"
			logMessage = "未安装 " + firstNonEmpty(depService, "依赖组件") + "，已跳过该文件（不重试；不需要该协议可在模板里关掉）"
		case missingBin:
			logMessage = "缺少关键组件 " + firstNonEmpty(depService, "sing-box") + "：请在服务器上安装，或重装 agent"
		default:
			logMessage = fmt.Sprintf("Agent 执行失败（第 %d 次）", newAttempts)
		}
		action := "agent-" + typ
		if typ == "deploy" {
			action = "agent-deploy"
		}
		h.recordNodeLog(r.Context(), configID, srv.ID, action, logStatus, exitCode, logMessage, output, "agent")
		handled = append(handled, map[string]any{"id": taskID, "ok": true, "status": finalStatus, "attempts": newAttempts})
	}

	_, _ = h.db.ExecContext(r.Context(),
		`UPDATE servers SET agent_last_seen = ?, updated_at = ? WHERE id = ?`, now, now, srv.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "server_time": now, "handled": handled,
		"next_poll_sec": clampInt(srv.NextPollSec, agentPollMinSec, agentPollMaxSec),
	})
}

func decodeAgentOutput(m map[string]any) string {
	if b64 := jsonStr(m["output_b64"]); b64 != "" {
		if raw, err := base64.StdEncoding.DecodeString(b64); err == nil {
			return string(raw)
		}
	}
	return jsonStr(m["output"])
}

// isSystemExecPayload 系统级固定动作（非用户任意命令），不受 allow_exec 限制。
func isSystemExecPayload(p map[string]any) bool {
	switch mapStr(p, "system_action", "") {
	case "cloudflared_update":
		return true
	}
	return false
}

// ingestAgentWSMetrics 处理 Agent 通过 WebSocket 实时上报的监控样本：
// 每次都刷新快照（面板立即变化），明细采样最多 60s 落一条，避免表膨胀。
func (h *handlers) ingestAgentWSMetrics(ctx context.Context, srv *agentServer, body map[string]any, ip string) {
	m := parseAgentMetrics(body)
	prev := h.agentPrevMetric(ctx, srv.ID)
	// 按上一次样本的真实间隔计算 CPU/速率，避免 WS 推送频率变化后数值失真
	intervalSec := int64(5)
	if prev != nil && prev.CreatedAt > 0 {
		if d := (nowMillis() - prev.CreatedAt) / 1000; d >= 1 && d <= 3600 {
			intervalSec = d
		}
	}
	m.CpuPercent = agentCpuPercentFromSample(prev, m, intervalSec)
	rates := agentThroughputFromSample(prev, m, intervalSec)
	version := firstNonEmpty(jsonStr(body["agent_version"]), jsonStr(body["agentVersion"]))
	opts := struct {
		NextPollSec    int64
		PersistPollSec int64
		PollCount      int64
		AgentVersion   string
		IP, IP4, IP6   string
		PrivateIP      string
		MeshIP4, MeshIP6 string
	}{srv.NextPollSec, 0, srv.AgentPollCount, version, ip, srv.LastIP4, srv.LastIP6, srv.LastIPPrivate, srv.LastMeshIP4, srv.LastMeshIP6}
	if err := h.agentUpdateSnapshot(ctx, srv, m, false, opts, rates); err != nil {
		h.log.Warn("WS 快照更新失败", "err", err)
	}
	if prev == nil || nowMillis()-prev.CreatedAt >= 60000 {
		h.insertServerMetric(ctx, srv.ID, m, rates)
	}
}
