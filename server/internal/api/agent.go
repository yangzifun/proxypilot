package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
	LastIPPrivate   string
	LastMeshIP4     string
	LastMeshIP6     string
	IPCountry       string
	IPRegion        string
	LastMetrics    string
	AgentKeyUsedAt int64
	// GraceAuth 只存在于本次请求的内存态：true 表示本次是按来源 IP 放行的（见 agent_grace.go）。
	GraceAuth bool
}

// agentServerSelectColumns 供 agentAuthenticate 使用的 servers 字段列表。
// 正常令牌查询与 grace 按 IP 查询共用同一份列表，避免两处字段漂移。
//
// 注意：这里刻意不含 agent_auth_grace —— 它由迁移 0053 新增，如果面板二进制先于迁移
// 上线，把新列写进这条主查询会让「全部节点」的 Agent 认证直接 500。grace 状态在
// 需要时单独查（见 agent_grace.go），迁移没上时只影响 grace 放行能力，不影响正常认证。
const agentServerSelectColumns = `SELECT id, COALESCE(name,''), COALESCE(link_mode,'ssh'),
	      COALESCE(enabled,0), COALESCE(agent_next_poll_sec,60), COALESCE(agent_version,''),
	      COALESCE(agent_platform,''), COALESCE(agent_token_enc,''), COALESCE(agent_last_poll_at,0),
	      COALESCE(agent_poll_count,0), COALESCE(agent_last_seen,0), COALESCE(last_status,'unknown'),
	      COALESCE(metrics_every,1), COALESCE(allow_exec,0), COALESCE(os_info,''), COALESCE(tags,''),
	      COALESCE(username,''), COALESCE(host,''), COALESCE(port,22), COALESCE(last_ip,''),
	      COALESCE(last_ip4,''), COALESCE(last_ip6,''), COALESCE(last_ip_private,''), COALESCE(last_mesh_ip4,''), COALESCE(last_mesh_ip6,''), COALESCE(ip_country,''), COALESCE(ip_region,''),
	      COALESCE(last_metrics,''), COALESCE(agent_key_used_at,0)
	    FROM servers`

// agentServerRowScanner 抽象 *sql.Row / *sql.Rows 的 Scan，供上面同一份列表使用。
type agentServerRowScanner interface {
	Scan(dest ...any) error
}

// scanAgentServerRow 按 agentServerSelectColumns 的字段顺序扫描一行。
func scanAgentServerRow(row agentServerRowScanner) (*agentServer, error) {
	var s agentServer
	err := row.Scan(&s.ID, &s.Name, &s.LinkMode, &s.Enabled, &s.NextPollSec, &s.AgentVersion,
		&s.AgentPlatform, &s.AgentTokenEnc, &s.AgentLastPollAt, &s.AgentPollCount, &s.AgentLastSeen,
		&s.LastStatus, &s.MetricsEvery, &s.AllowExec, &s.OSInfo, &s.Tags,
		&s.Username, &s.Host, &s.Port, &s.LastIP, &s.LastIP4, &s.LastIP6, &s.LastIPPrivate, &s.LastMeshIP4, &s.LastMeshIP6,
		&s.IPCountry, &s.IPRegion, &s.LastMetrics, &s.AgentKeyUsedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// agentTokenFromRequest 只从请求头提取 Agent 接入令牌。
//
// 审计中-04：旧实现还会从 body.token 与 ?token= 读取令牌，导致令牌进入访问日志、
// 反向代理日志、浏览器/Referer 历史，泄露面被放大。节点上运行的是 Agent 1.1，
// 它统一使用 X-Agent-Token 头（install.sh 的 keycheck/bootstrap 同样用 Header），
// 因此这里移除 URL/body 兼容路径 —— 已部署节点不受影响。
func agentTokenFromRequest(r *http.Request) string {
	if authz := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(authz, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	}
	return strings.TrimSpace(r.Header.Get("X-Agent-Token"))
}

// agentAuthenticate 校验 Agent 令牌（Bearer 或 X-Agent-Token 头）。
func (h *handlers) agentAuthenticate(r *http.Request, body map[string]any) (*agentServer, int, string) {
	raw := agentTokenFromRequest(r)
	if raw == "" {
		return nil, http.StatusUnauthorized, "缺少接入令牌"
	}
	if !strings.HasPrefix(raw, agentTokenPrefixDefault) {
		return nil, http.StatusUnauthorized, "接入令牌格式无效"
	}

	s, err := scanAgentServerRow(h.db.QueryRowContext(r.Context(),
		agentServerSelectColumns+` WHERE agent_token_hash = ? LIMIT 1`, sha256Hex(raw)))
	if err == sql.ErrNoRows {
		// 令牌哈希匹配失败：若该节点被显式置了 agent_auth_grace=1 且来源 IP 对得上，
		// 临时放行一次，让节点有机会跑到自愈逻辑把 conf 里的旧令牌改掉（见 agent_grace.go）。
		if g, ok := h.agentGraceAuthenticate(r, raw); ok {
			return g, 0, ""
		}
		// 临时诊断：令牌不匹配时打印提示与来源 IP，便于定位「节点上到底是哪一套令牌」
		h.log.Warn("Agent 鉴权失败：令牌不匹配",
			"token_hint", hintOf(raw), "remote", r.RemoteAddr,
			"ua", truncateRunes(r.UserAgent(), 60), "path", r.URL.Path)
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
	// 用正常令牌认证成功一次即撤销临时放行，临时通道自动关闭。
	h.clearAgentAuthGrace(r.Context(), s)
	return s, 0, ""
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

// agentBinaryBaseURL 返回显式配置的基址（统一使用对象存储桶的 agent 目录）；
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

// componentBaseURL 组件（自建 sing-box）分发基址：桶里单独一个目录。
// 优先级：后台设置（系统设置 → 高级设置）> PP_COMPONENT_BASE_URL > Agent 基址同级 singbox/。
func (h *handlers) componentBaseURL() string {
	if s := strings.TrimRight(strings.TrimSpace(h.setting(context.Background(), settingComponentBase, "")), "/"); s != "" {
		return s
	}
	if s := strings.TrimRight(strings.TrimSpace(os.Getenv("PP_COMPONENT_BASE_URL")), "/"); s != "" {
		return s
	}
	base := h.agentBinaryBaseURL()
	if base == "" {
		return ""
	}
	if strings.HasSuffix(base, "/agent") {
		return strings.TrimSuffix(base, "/agent") + "/singbox"
	}
	return base + "/singbox"
}

// componentManifest 是组件基址下的 manifest.json（版本真相来源）。
type componentManifest struct {
	Version string `json:"version"`
}

var (
	componentVerCache   string
	componentVerExpires time.Time
	componentVerMu      sync.Mutex
)

// componentVersion 返回 sing-box 组件当前分发版本：优先桶里的 manifest，
// 其次是后台设置 component_version，最后是环境变量。
//
// 「版本默认为存储桶中的」——模板里留空时就用这里的值，
// 保证面板下发的版本与实际可下载的二进制一致。
func (h *handlers) componentVersion(ctx context.Context) string {
	if v := strings.TrimSpace(h.setting(ctx, settingComponentVersion, "")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("SINGBOX_VERSION")); v != "" {
		return v
	}
	componentVerMu.Lock()
	defer componentVerMu.Unlock()
	if componentVerCache != "" && time.Now().Before(componentVerExpires) {
		return componentVerCache
	}
	base := h.componentBaseURL()
	if base == "" {
		return ""
	}
	cl := &http.Client{Timeout: 5 * time.Second}
	resp, err := cl.Get(base + "/manifest.json")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return ""
	}
	var m componentManifest
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m) != nil {
		return ""
	}
	componentVerCache = strings.TrimSpace(m.Version)
	componentVerExpires = time.Now().Add(10 * time.Minute)
	return componentVerCache
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
	// local-first：本地分发目录存在时优先使用，避免外部基址/CDN 缓存导致版本不一致
	if local, err := h.readLocalManifest(); err == nil && strings.TrimSpace(local.Version) != "" {
		local.Base = base
		manifestMemo, manifestAt = &local, time.Now()
		return local
	}

	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/manifest.json?t="+strconv.FormatInt(time.Now().UnixMilli(), 10), nil)
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

// hintOf 输出令牌的可辨别前缀/后缀（不打全量令牌）。
func hintOf(tok string) string {
	if len(tok) <= 12 {
		return strings.Repeat("*", len(tok))
	}
	return tok[:8] + "****" + tok[len(tok)-4:]
}
