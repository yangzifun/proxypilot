package api

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cdnPrefer 是「CDN 优选地址」的一条记录。
type cdnPrefer struct {
	ID          int64  `json:"id"`
	Group       string `json:"group_name"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	Remark      string `json:"remark"`
	Enabled     int    `json:"enabled"`
	Sort        int    `json:"sort"`
	LastCheckAt int64  `json:"last_check_at"`
	LastCheckOK int    `json:"last_check_ok"`
	Latency     int    `json:"last_latency_ms"`
}

var (
	cdnPreferMu       sync.RWMutex
	cdnPreferCache    []cdnPrefer
	cdnPreferGlobalOn bool
)

// cdnPreferEnabled 返回全局「订阅替换 CDN 优选地址」开关。
func cdnPreferEnabled() bool {
	cdnPreferMu.RLock()
	defer cdnPreferMu.RUnlock()
	return cdnPreferGlobalOn
}

// ReloadCDNPreferred 把启用中的优选地址载入内存（订阅生成时高频读取）。
func ReloadCDNPreferred(ctx context.Context, db *sql.DB) {
	if db == nil {
		return
	}
	rows, err := db.QueryContext(ctx,
		"SELECT id, group_name, address, port, COALESCE(remark,'') AS remark, enabled, sort FROM cdn_preferred_addresses WHERE enabled = 1 ORDER BY sort ASC, id ASC")
	if err != nil {
		return
	}
	defer rows.Close()
	list := []cdnPrefer{}
	for rows.Next() {
		var p cdnPrefer
		if rows.Scan(&p.ID, &p.Group, &p.Address, &p.Port, &p.Remark, &p.Enabled, &p.Sort) == nil {
			list = append(list, p)
		}
	}
	var flag string
	_ = db.QueryRowContext(ctx, "SELECT value FROM app_settings WHERE `key` = 'cdn_prefer_enabled' LIMIT 1").Scan(&flag)
	cdnPreferMu.Lock()
	cdnPreferCache = list
	cdnPreferGlobalOn = flag == "1" || strings.EqualFold(flag, "true")
	cdnPreferMu.Unlock()
}

// cdnPreferredForGroup 返回指定分组的优选地址；group 为空时用 default。
func cdnPreferredForGroup(group string) []cdnPrefer {
	group = strings.TrimSpace(group)
	if group == "" {
		group = "default"
	}
	cdnPreferMu.RLock()
	defer cdnPreferMu.RUnlock()
	out := make([]cdnPrefer, 0, len(cdnPreferCache))
	for _, p := range cdnPreferCache {
		if p.Group == group {
			out = append(out, p)
		}
	}
	return out
}

// ---------------- HTTP API ----------------

func (h *handlers) cdnPreferredList(w http.ResponseWriter, r *http.Request) {
	group := strings.TrimSpace(r.URL.Query().Get("group"))
	query := "SELECT id, group_name, address, port, COALESCE(remark,'') AS remark, enabled, sort, COALESCE(last_check_at,0) AS last_check_at, COALESCE(last_check_ok,0) AS last_check_ok, COALESCE(last_latency_ms,0) AS last_latency_ms FROM cdn_preferred_addresses"
	args := []any{}
	if group != "" {
		query += " WHERE group_name = ?"
		args = append(args, group)
	}
	query += " ORDER BY sort ASC, id ASC"
	rows, err := queryRowsAsMaps(r.Context(), h.db, query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": rows})
}

func (h *handlers) cdnPreferredGroups(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		"SELECT group_name, COUNT(1) AS total, SUM(enabled=1) AS enabled FROM cdn_preferred_addresses GROUP BY group_name ORDER BY group_name")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": rows})
}

func (h *handlers) cdnPreferredCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Group   string `json:"group_name"`
		Address string `json:"address"`
		Port    int    `json:"port"`
		Remark  string `json:"remark"`
		Enabled *bool  `json:"enabled"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	addr := strings.TrimSpace(body.Address)
	if addr == "" {
		writeError(w, http.StatusBadRequest, "地址不能为空")
		return
	}
	if body.Port <= 0 {
		body.Port = 443
	}
	group := strings.TrimSpace(body.Group)
	if group == "" {
		group = "default"
	}
	enabled := 1
	if body.Enabled != nil && !*body.Enabled {
		enabled = 0
	}
	now := nowMillis()
	if _, err := h.db.ExecContext(r.Context(),
		"INSERT INTO cdn_preferred_addresses (group_name, address, port, remark, enabled, sort, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, 0, ?, ?) ON DUPLICATE KEY UPDATE remark = VALUES(remark), enabled = VALUES(enabled), updated_at = VALUES(updated_at)",
		group, addr, body.Port, strings.TrimSpace(body.Remark), enabled, now, now); err != nil {
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	ReloadCDNPreferred(r.Context(), h.db)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (h *handlers) cdnPreferredUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "记录不存在")
		return
	}
	var body struct {
		Group   *string `json:"group_name"`
		Address *string `json:"address"`
		Port    *int    `json:"port"`
		Remark  *string `json:"remark"`
		Enabled *bool   `json:"enabled"`
		Sort    *int    `json:"sort"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	var sets []string
	var args []any
	if body.Group != nil {
		sets = append(sets, "group_name = ?")
		args = append(args, strings.TrimSpace(*body.Group))
	}
	if body.Address != nil {
		sets = append(sets, "address = ?")
		args = append(args, strings.TrimSpace(*body.Address))
	}
	if body.Port != nil {
		sets = append(sets, "port = ?")
		args = append(args, *body.Port)
	}
	if body.Remark != nil {
		sets = append(sets, "remark = ?")
		args = append(args, strings.TrimSpace(*body.Remark))
	}
	if body.Enabled != nil {
		if *body.Enabled {
			sets = append(sets, "enabled = 1")
		} else {
			sets = append(sets, "enabled = 0")
		}
	}
	if body.Sort != nil {
		sets = append(sets, "sort = ?")
		args = append(args, *body.Sort)
	}
	if len(sets) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, nowMillis(), id)
	if _, err := h.db.ExecContext(r.Context(),
		"UPDATE cdn_preferred_addresses SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...); err != nil {
		writeError(w, http.StatusInternalServerError, "更新失败")
		return
	}
	ReloadCDNPreferred(r.Context(), h.db)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (h *handlers) cdnPreferredDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "记录不存在")
		return
	}
	if _, err := h.db.ExecContext(r.Context(), "DELETE FROM cdn_preferred_addresses WHERE id = ?", id); err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	ReloadCDNPreferred(r.Context(), h.db)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// cdnPreferredImport 批量导入：每行 "host:port#备注"，# 之后整体作为备注。
func (h *handlers) cdnPreferredImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Group string `json:"group_name"`
		Text  string `json:"text"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	group := strings.TrimSpace(body.Group)
	if group == "" {
		group = "default"
	}
	added, skipped := 0, 0
	now := nowMillis()
	for _, line := range strings.Split(body.Text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		remark := ""
		if i := strings.Index(line, "#"); i >= 0 {
			remark = strings.TrimSpace(line[i+1:])
			line = strings.TrimSpace(line[:i])
		}
		host, port := line, 443
		if h, p, err := net.SplitHostPort(line); err == nil {
			host = h
			if n, e := strconv.Atoi(p); e == nil && n > 0 {
				port = n
			}
		}
		host = strings.TrimSpace(host)
		if host == "" {
			skipped++
			continue
		}
		res, err := h.db.ExecContext(r.Context(),
			"INSERT INTO cdn_preferred_addresses (group_name, address, port, remark, enabled, sort, created_at, updated_at) "+
				"VALUES (?, ?, ?, ?, 1, 0, ?, ?) ON DUPLICATE KEY UPDATE remark = VALUES(remark), updated_at = VALUES(updated_at)",
			group, host, port, remark, now, now)
		if err != nil {
			skipped++
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		} else {
			skipped++
		}
	}
	ReloadCDNPreferred(r.Context(), h.db)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "added": added, "skipped": skipped})
}

// cdnPreferredCheck 对地址做 TCP 443 连通性探测。
func (h *handlers) cdnPreferredCheck(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db, "SELECT id, address, port FROM cdn_preferred_addresses LIMIT 500")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	type result struct {
		ID      int64  `json:"id"`
		OK      bool   `json:"ok"`
		Latency int    `json:"latency_ms"`
		Error   string `json:"error,omitempty"`
	}
	results := make([]result, 0, len(rows))
	for _, row := range rows {
		id := mapInt(row, "id", 0)
		addr := mapStr(row, "address", "")
		port := mapInt(row, "port", 443)
		start := time.Now()
		conn, derr := net.DialTimeout("tcp", net.JoinHostPort(addr, strconv.FormatInt(port, 10)), 3*time.Second)
		lat := int(time.Since(start).Milliseconds())
		ok := derr == nil
		msg := ""
		if conn != nil {
			_ = conn.Close()
		}
		if derr != nil {
			msg = derr.Error()
		}
		okv := 0
		if ok {
			okv = 1
		}
		_, _ = h.db.ExecContext(r.Context(),
			"UPDATE cdn_preferred_addresses SET last_check_at = ?, last_check_ok = ?, last_latency_ms = ? WHERE id = ?",
			nowMillis(), okv, lat, id)
		results = append(results, result{ID: id, OK: ok, Latency: lat, Error: msg})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": results})
}

// GET /api/cdn-preferred/enabled  读取全局开关
func (h *handlers) cdnPreferredEnabledGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "enabled": cdnPreferEnabled()})
}

// PUT /api/cdn-preferred/enabled  切换全局开关
func (h *handlers) cdnPreferredEnabledSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	val := "0"
	if body.Enabled {
		val = "1"
	}
	if _, err := h.db.ExecContext(r.Context(),
		"INSERT INTO app_settings (`key`, value, updated_at) VALUES ('cdn_prefer_enabled', ?, ?) "+
			"ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = VALUES(updated_at)",
		val, nowMillis()); err != nil {
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	ReloadCDNPreferred(r.Context(), h.db)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "enabled": body.Enabled})
}
