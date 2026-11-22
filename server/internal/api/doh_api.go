package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// dohConfigView 是 /api/doh/config 返回的配置视图。
type dohConfigView struct {
	Enabled      bool     `json:"enabled"`
	Domain       string   `json:"domain"`
	DoHURL       string   `json:"doh_url"`
	Upstreams    []string `json:"upstreams"`
	MaxQPS       int      `json:"max_qps"`
	LogQueries   bool     `json:"log_queries"`
	MaxBodyBytes int      `json:"max_body_bytes"`

	// 兼容管理端表单使用的 doh_* 键名（与 app_settings 一致）。
	DoHEnabled    bool     `json:"doh_enabled"`
	DoHDomain     string   `json:"doh_domain"`
	DoHUpstreams  []string `json:"doh_upstreams"`
	DoHMaxQPS     int      `json:"doh_max_qps"`
	DoHLogQueries bool     `json:"doh_log_queries"`
}

func (cfg dohRuntimeConfig) view() dohConfigView {
	return dohConfigView{
		Enabled: cfg.Enabled, Domain: cfg.Domain, DoHURL: "https://" + cfg.Domain + "/dns-query",
		Upstreams: cfg.Upstreams, MaxQPS: cfg.MaxQPS, LogQueries: cfg.LogQueries,
		MaxBodyBytes: cfg.MaxBodyBytes,
		DoHEnabled:   cfg.Enabled, DoHDomain: cfg.Domain, DoHUpstreams: cfg.Upstreams,
		DoHMaxQPS: cfg.MaxQPS, DoHLogQueries: cfg.LogQueries,
	}
}

func dohBoolValue(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		b, ok := boolString(x)
		return b == "1", ok
	case float64:
		if x == 0 || x == 1 {
			return x == 1, true
		}
	case json.Number:
		n, err := x.Int64()
		if err == nil && (n == 0 || n == 1) {
			return n == 1, true
		}
	}
	return false, false
}

func dohIntValue(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case json.Number:
		n, err := x.Int64()
		if err == nil {
			return int(n), true
		}
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		if err == nil {
			return n, true
		}
	}
	return 0, false
}

func dohUpstreamsValue(v any) ([]string, error) {
	switch x := v.(type) {
	case string:
		return parseDoHUpstreams(x)
	case []any:
		raw := make([]string, 0, len(x))
		for _, item := range x {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("必须是字符串数组")
			}
			raw = append(raw, s)
		}
		return parseDoHUpstreams(strings.Join(raw, ","))
	case []string:
		return parseDoHUpstreams(strings.Join(x, ","))
	}
	return nil, fmt.Errorf("必须是字符串或字符串数组")
}

// dohConfigGet 返回当前 DoH 配置（含默认值）。
func (h *handlers) dohConfigGet(w http.ResponseWriter, r *http.Request) {
	cfg := h.dohRuntime(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": cfg.view()})
}

// dohConfigPut 保存 DoH 配置；只允许普通管理员（authed 路由）。
func (h *handlers) dohConfigPut(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	// 管理端表单使用 app_settings 同名键（doh_*），API 同时接受简短键。
	for alias, short := range map[string]string{
		"doh_enabled": "enabled", "doh_domain": "domain",
		"doh_upstreams": "upstreams", "doh_max_qps": "max_qps",
		"doh_log_queries": "log_queries", "doh_max_body_bytes": "max_body_bytes",
	} {
		if v, ok := body[alias]; ok {
			if _, exists := body[short]; !exists {
				body[short] = v
			}
		}
	}
	ctx := r.Context()
	changes := map[string]string{}
	if v, ok := body["enabled"]; ok {
		b, ok := dohBoolValue(v)
		if !ok {
			writeError(w, http.StatusBadRequest, "enabled 只能是 true/false 或 0/1")
			return
		}
		if b {
			changes[settingDoHEnabled] = "1"
		} else {
			changes[settingDoHEnabled] = "0"
		}
	}
	if v, ok := body["domain"]; ok {
		d, err := normalizeDoHDomain(jsonStr(v))
		if err != nil {
			writeError(w, http.StatusBadRequest, "domain "+err.Error())
			return
		}
		changes[settingDoHDomain] = d
	}
	if v, ok := body["upstreams"]; ok {
		ups, err := dohUpstreamsValue(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "upstreams "+err.Error())
			return
		}
		changes[settingDoHUpstreams] = strings.Join(ups, ",")
	}
	if v, ok := body["max_qps"]; ok {
		n, ok := dohIntValue(v)
		if !ok || n < 1 || n > 10000 {
			writeError(w, http.StatusBadRequest, "max_qps 超出范围（1-10000）")
			return
		}
		changes[settingDoHMaxQPS] = strconv.Itoa(n)
	}
	if v, ok := body["log_queries"]; ok {
		b, ok := dohBoolValue(v)
		if !ok {
			writeError(w, http.StatusBadRequest, "log_queries 只能是 true/false 或 0/1")
			return
		}
		if b {
			changes[settingDoHLogQueries] = "1"
		} else {
			changes[settingDoHLogQueries] = "0"
		}
	}
	if v, ok := body["max_body_bytes"]; ok {
		n, ok := dohIntValue(v)
		if !ok || n < 512 || n > 65535 {
			writeError(w, http.StatusBadRequest, "max_body_bytes 超出范围（512-65535）")
			return
		}
		changes[settingDoHMaxBodyBytes] = strconv.Itoa(n)
	}
	for key, value := range changes {
		if err := h.saveSetting(ctx, key, value); err != nil {
			writeError(w, http.StatusInternalServerError, "保存 "+key+" 失败")
			return
		}
	}
	h.invalidateSettings()
	cfg := h.dohRuntime(ctx)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": cfg.view()})
}

type dohStatRow struct {
	UUID     string `json:"uuid"`
	Owner    string `json:"owner"`
	Email    string `json:"email"`
	Queries  int64  `json:"queries"`
	BytesIn  int64  `json:"bytes_in"`
	BytesOut int64  `json:"bytes_out"`
	LastUsed int64  `json:"last_used"`
}

// dohStats 返回按 UUID 聚合的 DoH 使用量（默认近 30 天）。
func (h *handlers) dohStats(w http.ResponseWriter, r *http.Request) {
	rangeKey := strings.TrimSpace(r.URL.Query().Get("range"))
	if rangeKey == "" {
		rangeKey = "30d"
	}
	start := statsRangeStart(rangeKey)
	query := `SELECT h.uuid, COALESCE(ui.owner,''), COALESCE(ui.email,''),
	      COALESCE(SUM(h.queries),0), COALESCE(SUM(h.bytes_in),0), COALESCE(SUM(h.bytes_out),0),
	      COALESCE(MAX(h.updated_at),0)
	    FROM doh_usage_hourly h
	    LEFT JOIN uuid_info ui ON ui.uuid = h.uuid`
	args := []any{}
	if start > 0 {
		query += ` WHERE h.hour_ts >= ?`
		args = append(args, start)
	}
	query += ` GROUP BY h.uuid, ui.owner, ui.email ORDER BY SUM(h.queries) DESC LIMIT 500`

	rows, err := h.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询 DoH 统计失败")
		return
	}
	defer rows.Close()
	data := []dohStatRow{}
	for rows.Next() {
		var row dohStatRow
		if rows.Scan(&row.UUID, &row.Owner, &row.Email, &row.Queries, &row.BytesIn, &row.BytesOut, &row.LastUsed) == nil {
			data = append(data, row)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "total": len(data), "data": data})
}

// portalDoH 返回登录用户自己的 DoH 信息（doh_url / enabled / 30 天统计 / 最后使用）。
func (h *handlers) portalDoH(w http.ResponseWriter, r *http.Request) {
	email, ok := h.portalSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "登录已过期，请重新登录")
		return
	}
	cfg := h.dohRuntime(r.Context())
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT uuid, COALESCE(status,'') FROM uuid_info WHERE LOWER(email) = ?
		 ORDER BY created_at DESC`, email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询 UUID 失败")
		return
	}
	defer rows.Close()
	var uuids []string
	selected := ""
	for rows.Next() {
		var uuid, status string
		if rows.Scan(&uuid, &status) != nil {
			continue
		}
		uuids = append(uuids, uuid)
		if selected == "" {
			st := strings.ToLower(strings.TrimSpace(status))
			if st != "inactive" && st != "suspended" {
				selected = uuid
			}
		}
	}
	out := map[string]any{
		"success": true, "uuid": selected, "uuids": uuids,
		"doh_url": "", "enabled": false, "queries_30d": int64(0), "last_used": int64(0),
	}
	if selected == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["doh_url"] = dohURLOf(cfg.Domain, selected)
	out["enabled"] = cfg.Enabled

	if len(uuids) > 0 {
		now := nowMillis()
		// 查询该 email 名下所有 UUID 的 30 天使用量；IN 参数按需构造。
		placeholders := make([]string, 0, len(uuids))
		args := make([]any, 0, len(uuids)+1)
		for _, u := range uuids {
			placeholders = append(placeholders, "?")
			args = append(args, u)
		}
		args = append(args, now-30*24*3_600_000)
		var queries, lastUsed int64
		err := h.db.QueryRowContext(r.Context(),
			`SELECT COALESCE(SUM(queries),0), COALESCE(MAX(updated_at),0)
			 FROM doh_usage_hourly WHERE uuid IN (`+strings.Join(placeholders, ",")+`) AND hour_ts >= ?`,
			args...).Scan(&queries, &lastUsed)
		if err == nil {
			out["queries_30d"] = queries
			out["last_used"] = lastUsed
		}
	}
	writeJSON(w, http.StatusOK, out)
}
