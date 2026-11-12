package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

// serverRow 对齐 Worker 的 serverRowToJson 输出（JSON 字段名保持原样）。
type serverRow struct {
	ID                int64           `json:"id"`
	Name              string          `json:"name"`
	Port              int64           `json:"port"`
	LinkMode          string          `json:"link_mode"`
	HasAgentToken     bool            `json:"has_agent_token"`
	AgentTokenHint    string          `json:"agent_token_hint"`
	AgentKeyViewable  bool            `json:"agent_key_viewable"`
	AgentVersion      string          `json:"agent_version"`
	AgentLastSeen     int64           `json:"agent_last_seen"`
	AgentPlatform     string          `json:"agent_platform"`
	AgentKeyCreatedAt int64           `json:"agent_key_created_at"`
	AgentKeyUsedAt    int64           `json:"agent_key_used_at"`
	AgentNextPollSec  int64           `json:"agent_next_poll_sec"`
	AgentPollCount    int64           `json:"agent_poll_count"`
	MetricsEvery      int64           `json:"metrics_every"`
	AllowExec         bool            `json:"allow_exec"`
	LastIP            string          `json:"last_ip"`
	LastIP4           string          `json:"last_ip4"`
	LastIP6           string          `json:"last_ip6"`
	LastIPPrivate     string          `json:"last_ip_private"`
	LastMeshIP4       string          `json:"last_mesh_ip4"`
	LastMeshIP6       string          `json:"last_mesh_ip6"`
	// 自建组网（10.99.0.0/16，vpn_mesh_nodes）
	MeshIP4   string `json:"mesh_ip4"`
	MeshState string `json:"mesh_state"`
	MeshRTTMs int64  `json:"mesh_rtt_ms"`
	SSHGuard          int64           `json:"ssh_guard"`
	Host              string          `json:"host"`
	IPCountry         string          `json:"ip_country"`
	IPRegion          string          `json:"ip_region"`
	IPLat             *float64        `json:"ip_lat"`
	IPLon             *float64        `json:"ip_lon"`
	Tags              string          `json:"tags"`
	Remark            string          `json:"remark"`
	Enabled           bool            `json:"enabled"`
	LastStatus        string          `json:"last_status"`
	LastLatencyMs     int64           `json:"last_latency_ms"`
	LastError         string          `json:"last_error"`
	LastCheckAt       int64           `json:"last_check_at"`
	OSInfo            string          `json:"os_info"`
	SingboxTemplateID int64           `json:"singbox_template_id"`
	SingboxDomain     string          `json:"singbox_domain"`
	SingboxWarp       int64           `json:"singbox_warp"`
	SingboxAutoDeploy int64           `json:"singbox_auto_deploy"`
	CreatedAt         int64           `json:"created_at"`
	UpdatedAt         int64           `json:"updated_at"`
	LastMetrics       json.RawMessage `json:"last_metrics"`
	AgentLatest       string          `json:"agent_latest"`
	AgentUpdate       bool            `json:"agent_update"`
}

const serversSelectColumns = `SELECT id, COALESCE(name,''), COALESCE(port,22),
  COALESCE(agent_token_hash,''), COALESCE(agent_token_hint,''), COALESCE(agent_token_enc,''),
  COALESCE(agent_version,''), COALESCE(agent_last_seen,0), COALESCE(agent_platform,''),
  COALESCE(agent_key_created_at,0), COALESCE(agent_key_used_at,0), COALESCE(agent_next_poll_sec,2),
  COALESCE(agent_poll_count,0), COALESCE(metrics_every,1), COALESCE(allow_exec,0),
  COALESCE(last_ip,''), COALESCE(last_ip4,''), COALESCE(last_ip6,''), COALESCE(last_ip_private,''), COALESCE(last_mesh_ip4,''), COALESCE(last_mesh_ip6,''), COALESCE(ssh_guard,0), COALESCE(host,''),
  COALESCE(ip_country,''), COALESCE(ip_region,''), ip_lat, ip_lon,
  COALESCE(tags,''), COALESCE(remark,''), COALESCE(enabled,0),
  COALESCE(last_status,'unknown'), COALESCE(last_latency_ms,0), COALESCE(last_error,''),
  COALESCE(last_check_at,0), COALESCE(os_info,''), COALESCE(singbox_template_id,0),
  COALESCE(singbox_domain,''), COALESCE(singbox_warp,-1), COALESCE(singbox_auto_deploy,0),
  COALESCE(created_at,0), COALESCE(updated_at,0), COALESCE(link_mode,'agent'), last_metrics
FROM servers`

const serversSelect = serversSelectColumns + ` ORDER BY id ASC`

// scanServerRow 读取一行 servers（列顺序与 serversSelectColumns 一致）。
func scanServerRow(rows *sql.Rows) (serverRow, error) {
	var (
		s                    serverRow
		tokenHash, tokenHint string
		tokenEnc             string
		lat, lon             sql.NullFloat64
		metrics              sql.NullString
		allowExec, enabled   int64
		lastStatus           string
		linkMode             string
	)
	if err := rows.Scan(
		&s.ID, &s.Name, &s.Port,
		&tokenHash, &tokenHint, &tokenEnc,
		&s.AgentVersion, &s.AgentLastSeen, &s.AgentPlatform,
		&s.AgentKeyCreatedAt, &s.AgentKeyUsedAt, &s.AgentNextPollSec,
		&s.AgentPollCount, &s.MetricsEvery, &allowExec,
		&s.LastIP, &s.LastIP4, &s.LastIP6, &s.LastIPPrivate, &s.LastMeshIP4, &s.LastMeshIP6, &s.SSHGuard, &s.Host,
		&s.IPCountry, &s.IPRegion, &lat, &lon,
		&s.Tags, &s.Remark, &enabled,
		&lastStatus, &s.LastLatencyMs, &s.LastError,
		&s.LastCheckAt, &s.OSInfo, &s.SingboxTemplateID,
		&s.SingboxDomain, &s.SingboxWarp, &s.SingboxAutoDeploy,
		&s.CreatedAt, &s.UpdatedAt, &linkMode, &metrics,
	); err != nil {
		return s, err
	}
	s.Enabled = enabled == 1
	s.AllowExec = allowExec == 1
	s.HasAgentToken = tokenHash != ""
	s.AgentTokenHint = tokenHint
	s.AgentKeyViewable = tokenEnc != ""
	s.LinkMode = linkMode
	if s.LastIP == "unknown" {
		s.LastIP = ""
	}
	s.LastIP4 = publicDisplayIPv4(s.LastIP4, s.LastIP)
	// 展示兜底：历史脏数据里的 WARP 段 / 非全球单播地址不展示（task-15）
	s.LastIP6 = displayIPv6(s.LastIP6)
	// 内网地址单独字段，避免把 10.x / 100.64.x 当成公网展示
	if s.LastIPPrivate == "" && isPrivateIP(s.LastIP) && s.LastIP != s.LastIP4 {
		s.LastIPPrivate = s.LastIP
	}
	s.AgentNextPollSec = clampInt(s.AgentNextPollSec, 2, 900)
	s.LastStatus = agentOnlineStatus(s.AgentLastSeen, lastStatus, s.AgentNextPollSec)
	s.IPLat = floatPtr(lat)
	s.IPLon = floatPtr(lon)
	s.LastMetrics = rawJSON(metrics)
	return s, nil
}

// fetchServerRow 按 id 取一台服务器；不存在返回 sql.ErrNoRows。
func (h *handlers) fetchServerRow(ctx context.Context, id int64) (serverRow, error) {
	rows, err := h.db.QueryContext(ctx, serversSelectColumns+` WHERE id = ?`, id)
	if err != nil {
		return serverRow{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return serverRow{}, sql.ErrNoRows
	}
	return scanServerRow(rows)
}

// attachMeshInfo 把自建组网（vpn_mesh_nodes）的地址与状态合并进服务器列表。
// 与 WARP（last_mesh_ip4/6，100.96.x）区分开：这里是 10.99.0.0/16。
func (h *handlers) attachMeshInfo(ctx context.Context, list []serverRow) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT server_id, COALESCE(mesh_ip4,''), COALESCE(state,''), COALESCE(rtt_ms,0) FROM vpn_mesh_nodes`)
	if err != nil {
		h.log.Error("查询组网成员失败", "err", err)
		return
	}
	defer rows.Close()
	type mesh struct {
		ip4   string
		state string
		rtt   int64
	}
	m := map[int64]mesh{}
	for rows.Next() {
		var id, rtt int64
		var ip4, st string
		if rows.Scan(&id, &ip4, &st, &rtt) != nil {
			continue
		}
		m[id] = mesh{ip4: ip4, state: st, rtt: rtt}
	}
	for i := range list {
		if v, ok := m[list[i].ID]; ok {
			list[i].MeshIP4 = v.ip4
			list[i].MeshState = v.state
			list[i].MeshRTTMs = v.rtt
		}
	}
}

// serversList 对应 GET /api/servers?q=&status=
func (h *handlers) serversList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.QueryContext(r.Context(), serversSelect)
	if err != nil {
		h.log.Error("查询服务器列表失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询服务器列表失败")
		return
	}
	defer rows.Close()

	var list []serverRow
	for rows.Next() {
		s, err := scanServerRow(rows)
		if err != nil {
			h.log.Error("扫描服务器行失败", "err", err)
			writeError(w, http.StatusInternalServerError, "解析服务器数据失败")
			return
		}
		list = append(list, s)
	}
	if err := rows.Err(); err != nil {
		h.log.Error("遍历服务器行失败", "err", err)
		writeError(w, http.StatusInternalServerError, "读取服务器数据失败")
		return
	}

	h.attachMeshInfo(r.Context(), list)

	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	wantStatus := strings.TrimSpace(r.URL.Query().Get("status"))
	out := make([]serverRow, 0, len(list))
	for _, s := range list {
		if wantStatus != "" && s.LastStatus != wantStatus {
			continue
		}
		if q != "" {
			hay := strings.ToLower(s.Name + " " + s.LastIP + " " + s.LastIP4 + " " + s.LastIP6 + " " + s.Tags)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, s)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"total":   len(out),
		"data":    out,
	})
}

type serverStatusItem struct {
	ID             int64           `json:"id"`
	Name           string          `json:"name"`
	LastIP         string          `json:"last_ip"`
	Port           int64           `json:"port"`
	Enabled        bool            `json:"enabled"`
	LastIP4        string          `json:"last_ip4"`
	LastIP6        string          `json:"last_ip6"`
	LastIPPrivate  string          `json:"last_ip_private"`
	LastMeshIP4    string          `json:"last_mesh_ip4"`
	LastMeshIP6    string          `json:"last_mesh_ip6"`
	MeshIP4        string          `json:"mesh_ip4"`
	MeshState      string          `json:"mesh_state"`
	MeshRTTMs      int64           `json:"mesh_rtt_ms"`
	LinkMode       string          `json:"link_mode"`
	AllowExec      bool            `json:"allow_exec"`
	Status         string          `json:"status"`
	LatencyMs      int64           `json:"latencyMs"`
	Error          string          `json:"error"`
	CheckedAt      int64           `json:"checkedAt"`
	Metrics        json.RawMessage `json:"metrics"`
	AgentVersion   string          `json:"agent_version"`
	AgentLastSeen  int64           `json:"agent_last_seen"`
	AgentPlatform  string          `json:"agent_platform"`
	AgentTokenHint string          `json:"agent_token_hint"`
	NextPollSec    int64           `json:"next_poll_sec"`
}

// serversStatus 对应 GET /api/servers/status（监控页的轻量结构）。
func (h *handlers) serversStatus(w http.ResponseWriter, r *http.Request) {
	const q = `SELECT id, COALESCE(name,''), COALESCE(port,22), COALESCE(enabled,0),
	  COALESCE(link_mode,'agent'), COALESCE(allow_exec,0), COALESCE(last_status,'unknown'),
	  COALESCE(last_latency_ms,0), COALESCE(last_error,''), COALESCE(last_check_at,0), last_metrics,
	  COALESCE(agent_version,''), COALESCE(agent_platform,''), COALESCE(agent_last_seen,0),
	  COALESCE(agent_token_hint,''), COALESCE(agent_next_poll_sec,2),
	  COALESCE(last_ip,''), COALESCE(last_ip4,''), COALESCE(last_ip6,''), COALESCE(last_ip_private,''), COALESCE(last_mesh_ip4,''), COALESCE(last_mesh_ip6,'')
	FROM servers ORDER BY id ASC`

	rows, err := h.db.QueryContext(r.Context(), q)
	if err != nil {
		h.log.Error("查询服务器状态失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询服务器状态失败")
		return
	}
	defer rows.Close()

	var items []serverStatusItem
	online, offline, unknown := 0, 0, 0
	for rows.Next() {
		var (
			it                 serverStatusItem
			enabled, allowExec int64
			lastStatus         string
			metrics            sql.NullString
		)
		if err := rows.Scan(
			&it.ID, &it.Name, &it.Port, &enabled,
			&it.LinkMode, &allowExec, &lastStatus,
			&it.LatencyMs, &it.Error, &it.CheckedAt, &metrics,
			&it.AgentVersion, &it.AgentPlatform, &it.AgentLastSeen,
			&it.AgentTokenHint, &it.NextPollSec,
			&it.LastIP, &it.LastIP4, &it.LastIP6, &it.LastIPPrivate, &it.LastMeshIP4, &it.LastMeshIP6,
		); err != nil {
			h.log.Error("扫描服务器状态失败", "err", err)
			writeError(w, http.StatusInternalServerError, "解析服务器状态失败")
			return
		}
		it.Enabled = enabled == 1
		it.AllowExec = allowExec == 1
		if it.LastIP == "unknown" {
			it.LastIP = ""
		}
		it.LastIP4 = publicDisplayIPv4(it.LastIP4, it.LastIP)
		it.LastIP6 = displayIPv6(it.LastIP6)
		it.NextPollSec = clampInt(it.NextPollSec, 2, 900)
		it.Status = agentOnlineStatus(it.AgentLastSeen, lastStatus, it.NextPollSec)
		it.Metrics = rawJSON(metrics)
		switch it.Status {
		case "online":
			online++
		case "offline":
			offline++
		default:
			unknown++
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		h.log.Error("遍历服务器状态失败", "err", err)
		writeError(w, http.StatusInternalServerError, "读取服务器状态失败")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"total":   len(items),
		"online":  online,
		"offline": offline,
		"unknown": unknown,
		"data":    items,
	})
}

func agentOnlineStatus(lastSeen int64, status string, nextPoll int64) string {
	if status == "" {
		status = "unknown"
	}
	if lastSeen <= 0 {
		// 从未收到心跳：即使历史状态是 online，也不能继续显示在线
		if status == "online" {
			return "offline"
		}
		return status
	}
	if status != "offline" && nowMillis()-lastSeen > agentOfflineTimeoutMs(nextPoll) {
		return "offline"
	}
	return status
}

// agentOfflineTimeoutMs 返回「允许多久没有心跳才判离线」。
// 必须严格大于心跳间隔，并至少留 60 秒缓冲，避免网络抖动/采集耗时导致误判。
func agentOfflineTimeoutMs(nextPoll int64) int64 {
	poll := clampInt(nextPoll, 2, 900)
	timeout := poll * 4 * 1000
	if timeout < 60_000 {
		timeout = 60_000
	}
	if timeout <= poll*1000 {
		timeout = (poll + 1) * 1000
	}
	if timeout > 3_600_000 {
		timeout = 3_600_000
	}
	return timeout
}

func floatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

func rawJSON(v sql.NullString) json.RawMessage {
	if !v.Valid || !json.Valid([]byte(v.String)) {
		return nil
	}
	return json.RawMessage(v.String)
}

// publicDisplayIPv4 展示用的公网 IPv4：
//   - last_ip4 本身是合法公网 IPv4 → 原样展示
//   - 是私网 / CGNAT(100.64/10，WARP 组网) 等非公网地址 → 用连接来源的公网 IPv4 顶替
//   - 顶替不到就隐藏（空串），绝不把组网地址当成主机 IPv4 展示
func publicDisplayIPv4(v4, connectionIP string) string {
	v4 = strings.TrimSpace(v4)
	if v4 == "" {
		// 没有 IPv4 就保持空（IPv6-only 节点不拿连接来源硬凑一个 v4）
		return ""
	}
	if pub := usablePublicIPv4(v4); pub != "" {
		return pub
	}
	if pub := usablePublicIPv4(connectionIP); pub != "" {
		return pub
	}
	return ""
}

func isPrivateIP(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}
