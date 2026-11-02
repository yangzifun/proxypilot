package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	agentTokenPrefixDefault = "ag_"
	agentPollDefaultSec     = 60
	agentPollMinSec         = 2
	agentPollMaxSec         = 900
	agentScriptVersion      = "1.0.0"
)

// agentServer 是鉴权后需要的服务器字段子集。
type agentServer struct {
	ID              int64
	Name            string
	LinkMode        string
	Enabled         int64
	NextPollSec     int64
	AgentVersion    string
	AgentPlatform   string
	AgentTokenEnc   string
	AgentLastPollAt int64
	AgentPollCount  int64
	AgentLastSeen   int64
	LastStatus      string
	MetricsEvery    int64
	AllowExec       int64
	OSInfo          string
	Tags            string
	Username        string
	Host            string
	Port            int64
	LastIP          string
	LastIP4         string
	LastIP6         string
	IPCountry       string
	IPRegion        string
	LastMetrics     string
	AgentKeyUsedAt  int64
}

// agentAuthenticate 复刻 Worker 的令牌提取顺序：Bearer → X-Agent-Token → body.token → ?token=。
func (h *handlers) agentAuthenticate(r *http.Request, body map[string]any) (*agentServer, int, string) {
	raw := ""
	if authz := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(authz, "Bearer ") {
		raw = strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	}
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("X-Agent-Token"))
	}
	if raw == "" && body != nil {
		if v, ok := body["token"].(string); ok {
			raw = strings.TrimSpace(v)
		}
	}
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if raw == "" {
		return nil, http.StatusUnauthorized, "缺少接入令牌"
	}
	if !strings.HasPrefix(raw, agentTokenPrefixDefault) {
		return nil, http.StatusUnauthorized, "接入令牌格式无效"
	}

	var s agentServer
	err := h.db.QueryRowContext(r.Context(), `SELECT id, COALESCE(name,''), COALESCE(link_mode,'ssh'),
	      COALESCE(enabled,0), COALESCE(agent_next_poll_sec,60), COALESCE(agent_version,''),
	      COALESCE(agent_platform,''), COALESCE(agent_token_enc,''), COALESCE(agent_last_poll_at,0),
	      COALESCE(agent_poll_count,0), COALESCE(agent_last_seen,0), COALESCE(last_status,'unknown'),
	      COALESCE(metrics_every,1), COALESCE(allow_exec,0), COALESCE(os_info,''), COALESCE(tags,''),
	      COALESCE(username,''), COALESCE(host,''), COALESCE(port,22), COALESCE(last_ip,''),
	      COALESCE(last_ip4,''), COALESCE(last_ip6,''), COALESCE(ip_country,''), COALESCE(ip_region,''),
	      COALESCE(last_metrics,''), COALESCE(agent_key_used_at,0)
	    FROM servers WHERE agent_token_hash = ? LIMIT 1`, sha256Hex(raw)).
		Scan(&s.ID, &s.Name, &s.LinkMode, &s.Enabled, &s.NextPollSec, &s.AgentVersion,
			&s.AgentPlatform, &s.AgentTokenEnc, &s.AgentLastPollAt, &s.AgentPollCount, &s.AgentLastSeen,
			&s.LastStatus, &s.MetricsEvery, &s.AllowExec, &s.OSInfo, &s.Tags,
			&s.Username, &s.Host, &s.Port, &s.LastIP, &s.LastIP4, &s.LastIP6,
			&s.IPCountry, &s.IPRegion, &s.LastMetrics, &s.AgentKeyUsedAt)
	if err == sql.ErrNoRows {
		return nil, http.StatusUnauthorized, "接入令牌无效或已轮换"
	}
	if err != nil {
		h.log.Error("查询 Agent 服务器失败", "err", err)
		return nil, http.StatusInternalServerError, "数据库错误"
	}
	if s.LinkMode != "agent" {
		return nil, http.StatusForbidden, "该服务器未启用 Agent 接入方式"
	}
	if s.Enabled != 1 {
		return nil, http.StatusForbidden, "该服务器已被禁用"
	}
	return &s, 0, ""
}

// GET/POST /api/agent/keycheck
func (h *handlers) agentKeycheck(w http.ResponseWriter, r *http.Request) {
	srv, status, msg := h.agentAuthenticate(r, nil)
	if srv == nil {
		writeJSON(w, status, map[string]any{"ok": false, "error": msg})
		return
	}
	if srv.AgentKeyUsedAt == 0 {
		now := nowMillis()
		if _, err := h.db.ExecContext(r.Context(),
			`UPDATE servers SET agent_key_used_at = ?, updated_at = ? WHERE id = ?`, now, now, srv.ID); err != nil {
			h.log.Warn("记录密钥首次使用失败", "err", err)
		}
	}
	viewable := srv.AgentTokenEnc != "" && h.agentKeySecret() != ""
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "server_id": srv.ID, "server_name": srv.Name,
		"poll_sec":             clampInt(srv.NextPollSec, agentPollMinSec, agentPollMaxSec),
		"binary_base_url":      h.agentBaseFor(r),
		"agent_script_version": agentScriptVersion,
		"viewable":             viewable,
		"server_time":          nowMillis(),
	})
}

// agentBinaryBaseURL 返回显式配置的基址（外部对象存储或 PP_PUBLIC_BASE_URL + /agent-dist）；
// 返回空串表示只能按请求 origin 推导（见 agentBaseFor）。
func (h *handlers) agentBinaryBaseURL() string {
	if s := strings.TrimSpace(h.setting(context.Background(), settingAgentBinaryBase, h.cfg.AgentBinaryBaseURL)); s != "" {
		return strings.TrimRight(s, "/")
	}
	if s := h.publicBaseURL(context.Background()); s != "" {
		return strings.TrimRight(s, "/") + "/agent-dist"
	}
	return ""
}

// agentBaseFor 优先用显式基址，否则按当前请求推导本地 /agent-dist。
func (h *handlers) agentBaseFor(r *http.Request) string {
	if s := h.agentBinaryBaseURL(); s != "" {
		return s
	}
	if r == nil {
		return "/agent-dist"
	}
	return h.siteOrigin(r) + "/agent-dist"
}

// ---------------- manifest（5 分钟缓存） ----------------

type manifestData struct {
	Base    string         `json:"base"`
	Version string         `json:"version"`
	Files   map[string]any `json:"files"`
	Error   string         `json:"error,omitempty"`
}

var (
	manifestMu   sync.Mutex
	manifestMemo *manifestData
	manifestAt   time.Time
)

func (h *handlers) agentLatestManifest(ctx context.Context, force bool) manifestData {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	if !force && manifestMemo != nil && time.Since(manifestAt) < 5*time.Minute {
		return *manifestMemo
	}
	base := h.agentBinaryBaseURL()
	data := manifestData{Base: base, Files: map[string]any{}}
	if strings.TrimSpace(h.cfg.AgentBinaryBaseURL) == "" {
		if local, err := h.readLocalManifest(); err == nil {
			local.Base = base
			manifestMemo, manifestAt = &local, time.Now()
			return local
		}
	}

	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/manifest.json", nil)
	if err != nil {
		data.Error = "manifest.json 读取失败：" + err.Error()
		manifestMemo, manifestAt = &data, time.Now()
		return data
	}
	resp, err := client.Do(req)
	if err != nil {
		data.Error = "manifest.json 读取失败：" + err.Error()
		manifestMemo, manifestAt = &data, time.Now()
		return data
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data.Error = fmt.Sprintf("manifest.json 读取失败（HTTP %d）", resp.StatusCode)
		manifestMemo, manifestAt = &data, time.Now()
		return data
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		data.Error = "manifest.json 读取失败：" + err.Error()
		manifestMemo, manifestAt = &data, time.Now()
		return data
	}
	var j struct {
		Version string                     `json:"version"`
		Files   map[string]json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		data.Error = "manifest.json 解析失败：" + err.Error()
		manifestMemo, manifestAt = &data, time.Now()
		return data
	}
	data.Version = j.Version
	if j.Files != nil {
		data.Files = map[string]any{}
		for k, v := range j.Files {
			var anyVal any
			if json.Unmarshal(v, &anyVal) == nil {
				data.Files[k] = anyVal
			}
		}
	}
	manifestMemo, manifestAt = &data, time.Now()
	return data
}

// GET /api/agent/latest（后台 JWT）
func (h *handlers) agentLatest(w http.ResponseWriter, r *http.Request) {
	m := h.agentLatestManifest(r.Context(), r.URL.Query().Get("refresh") == "1")
	if strings.TrimSpace(m.Base) == "" {
		m.Base = h.agentBaseFor(r)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "latest": m.Version, "base": m.Base,
		"files": m.Files, "error": m.Error,
	})
}

// ---------------- 版本比较 ----------------

func parseAgentVersion(v string) []int {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if s == "" {
		return nil
	}
	// 只取数字与点，遇到 - / + 截断
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

func agentVersionAtLeast(v, min string) bool {
	a, b := parseAgentVersion(v), parseAgentVersion(min)
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return true
}

func agentVersionNewer(latest, current string) bool {
	a, b := parseAgentVersion(latest), parseAgentVersion(current)
	if len(a) == 0 {
		return false
	}
	if len(b) == 0 {
		return true
	}
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}
