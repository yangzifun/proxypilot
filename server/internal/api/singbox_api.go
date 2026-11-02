package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
)

func queryRowsAsMaps(ctx context.Context, db *sql.DB, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			switch v := vals[i].(type) {
			case []byte:
				m[c] = string(v)
			default:
				m[c] = v
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func mapStr(m map[string]any, key, fallback string) string {
	if v, ok := m[key]; ok && v != nil {
		if s, ok := v.(string); ok {
			if strings.TrimSpace(s) != "" {
				return s
			}
		} else {
			return jsonStr(v)
		}
	}
	return fallback
}

func mapInt(m map[string]any, key string, fallback int64) int64 {
	if v, ok := m[key]; ok && v != nil {
		return jsonInt(v)
	}
	return fallback
}

func maskSecret(v string) string {
	s := strings.TrimSpace(v)
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "******"
	}
	return s[:4] + "******" + s[len(s)-4:]
}

// singboxTemplateToJSON 对应 singboxTemplateRowToJson。
func singboxTemplateToJSON(row map[string]any, reveal bool) map[string]any {
	usersJSON := mapStr(row, "users_json", "[]")
	var rawUsers []sbUser
	_ = json.Unmarshal([]byte(usersJSON), &rawUsers)
	users := singboxParseUsers(rawUsers)

	ssKey := mapStr(row, "ss_server_key", "")
	realityPriv := mapStr(row, "reality_private_key", "")
	if !reveal {
		ssKey = maskSecret(ssKey)
		realityPriv = maskSecret(realityPriv)
	}

	opts := map[string]any{}
	if raw := mapStr(row, "protocol_options", "{}"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &opts)
	}
	masquePath := "/.well-known/masque/ip/{target}/{ipproto}/"
	masqueVersions := any([]any{1, 2, 3})
	if mq, ok := opts["masque"].(map[string]any); ok {
		if p := jsonStr(mq["path"]); p != "" {
			masquePath = p
		}
		if v, ok := mq["versions"].([]any); ok && len(v) > 0 {
			masqueVersions = v
		}
	}

	enableSingbox := int64(1)
	if v, ok := row["enable_singbox"]; ok && v != nil {
		enableSingbox = jsonInt(v)
	}

	tpl := map[string]any{
		"id":   mapInt(row, "id", 0),
		"name": mapStr(row, "name", ""), "remark": mapStr(row, "remark", ""),
		"users_json": usersJSON, "user_count": len(users),
		"ss_method":     firstNonEmpty(mapStr(row, "ss_method", ""), singboxDefaultSSMethod),
		"ss_server_key": ssKey, "reality_private_key": realityPriv,
		"reality_public_key": mapStr(row, "reality_public_key", ""),
		"reality_short_id":   mapStr(row, "reality_short_id", ""),
		"reality_dest":       mapStr(row, "reality_dest", ""), "reality_sni": mapStr(row, "reality_sni", ""),
		"ws_sni": mapStr(row, "ws_sni", ""), "disguise_url": mapStr(row, "disguise_url", ""),
		"cert_path": mapStr(row, "cert_path", ""), "key_path": mapStr(row, "key_path", ""),
		"singbox_version":      mapStr(row, "singbox_version", ""),
		"port_base":            mapInt(row, "port_base", 13700),
		"warp_base":            mapInt(row, "warp_base", 13800),
		"enable_ss":            mapInt(row, "enable_ss", 0),
		"enable_anytls":        mapInt(row, "enable_anytls", 0),
		"enable_hy2":           mapInt(row, "enable_hy2", 0),
		"enable_reality":       mapInt(row, "enable_reality", 0),
		"enable_vlws":          mapInt(row, "enable_vlws", 0),
		"enable_vmess":         mapInt(row, "enable_vmess", 0),
		"enable_singbox":       enableSingbox,
		"enable_masque":        mapInt(row, "enable_masque", 0),
		"masque_json":          "",
		"masque_path":          mapStr(row, "masque_path", "/etc/sing-box/masque.json"),
		"service_masque":       mapStr(row, "service_masque", "masque"),
		"masque_uri_path":      masquePath,
		"masque_versions":      masqueVersions,
		"warp_engines":         mapStr(row, "warp_engines", "singbox"),
		"protocol_options":     mapStr(row, "protocol_options", "{}"),
		"warp_enabled":         mapInt(row, "warp_enabled", 0),
		"warp_address":         mapStr(row, "warp_address", ""),
		"warp_peer_public_key": mapStr(row, "warp_peer_public_key", ""),
		"warp_private_key": func() string {
			v := mapStr(row, "warp_private_key", "")
			if reveal {
				return v
			}
			return maskSecret(v)
		}(),
		"enabled": func() int64 {
			if v, ok := row["enabled"]; ok && v != nil {
				return jsonInt(v)
			}
			return 1
		}(),
		"created_at": mapInt(row, "created_at", 0),
		"updated_at": mapInt(row, "updated_at", 0),
	}
	protocols := []string{}
	for _, p := range []struct {
		key, name string
	}{{"enable_ss", "ss"}, {"enable_anytls", "anytls"}, {"enable_hy2", "hy2"}, {"enable_reality", "reality"}, {"enable_vlws", "vlws"}, {"enable_vmess", "tuic"}, {"enable_masque", "masque"}} {
		if mapInt(row, p.key, 0) != 0 {
			protocols = append(protocols, p.name)
		}
	}
	tpl["protocols"] = protocols
	return tpl
}

func singboxNodeToJSON(row map[string]any) map[string]any {
	rawWarp := int64(-1)
	if v, ok := row["warp_enabled"]; ok && v != nil {
		rawWarp = jsonInt(v)
	}
	warpEffective := int64(0)
	if rawWarp == 0 || rawWarp == 1 {
		warpEffective = rawWarp
	} else if mapInt(row, "template_warp", 0) == 1 {
		warpEffective = 1
	}
	var uuidList []any
	if raw := mapStr(row, "uuid_list", "[]"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &uuidList)
	}
	filtered := make([]any, 0, len(uuidList))
	for _, v := range uuidList {
		if s := jsonStr(v); s != "" {
			filtered = append(filtered, s)
		}
	}
	return map[string]any{
		"id": mapInt(row, "id", 0), "name": mapStr(row, "name", ""),
		"server_id": mapInt(row, "server_id", 0), "template_id": mapInt(row, "template_id", 0),
		"server_name": mapStr(row, "server_name", ""), "server_host": mapStr(row, "server_host", ""),
		"template_name": mapStr(row, "template_name", ""),
		"domain":        mapStr(row, "domain", ""), "cdn_domain": mapStr(row, "cdn_domain", ""),
		"port_base": mapInt(row, "port_base", 0), "warp_base": mapInt(row, "warp_base", 0),
		"sb_path": mapStr(row, "sb_path", ""), "caddy_path": mapStr(row, "caddy_path", ""),
		"service_sb": mapStr(row, "service_sb", "sing-box"), "service_caddy": mapStr(row, "service_caddy", "caddy-naive"),
		"enabled": func() int64 {
			if v, ok := row["enabled"]; ok && v != nil {
				return jsonInt(v)
			}
			return 1
		}(),
		"last_apply_at": mapInt(row, "last_apply_at", 0), "last_render_hash": mapStr(row, "last_render_hash", ""),
		"warp_enabled": rawWarp, "warp_effective": warpEffective,
		"uuid_list": filtered, "remark": mapStr(row, "remark", ""),
		"created_at": mapInt(row, "created_at", 0), "updated_at": mapInt(row, "updated_at", 0),
	}
}

// GET /api/singbox/templates
func (h *handlers) singboxTemplatesList(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT * FROM singbox_templates ORDER BY id ASC`)
	if err != nil {
		h.log.Error("查询 sing-box 模板失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询模板失败")
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, singboxTemplateToJSON(row, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

// GET /api/singbox/templates/{id}
func (h *handlers) singboxTemplateGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT * FROM singbox_templates WHERE id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": singboxTemplateToJSON(rows[0], true)})
}

// GET /api/singbox/nodes
func (h *handlers) singboxNodesList(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT n.*, s.name AS server_name, s.last_ip AS server_host,
	      t.name AS template_name, t.warp_enabled AS template_warp
	    FROM singbox_nodes n
	    LEFT JOIN servers s ON s.id = n.server_id
	    LEFT JOIN singbox_templates t ON t.id = n.template_id
	    ORDER BY n.id ASC`)
	if err != nil {
		h.log.Error("查询 sing-box 节点失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询节点失败")
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, singboxNodeToJSON(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

// GET /api/singbox/uuids
func (h *handlers) singboxUUIDs(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT u.uuid AS uuid, COUNT(c.id) AS node_count,
	      MAX(i.owner) AS owner, MAX(i.remark) AS remark, MAX(i.email) AS email, MAX(i.status) AS status
	    FROM (SELECT uuid FROM uuid_info WHERE uuid IS NOT NULL AND uuid != ''
	          UNION SELECT uuid FROM configs WHERE uuid IS NOT NULL AND uuid != '') u
	    LEFT JOIN configs c ON c.uuid = u.uuid
	    LEFT JOIN uuid_info i ON i.uuid = u.uuid
	    GROUP BY u.uuid ORDER BY node_count DESC, u.uuid ASC LIMIT 500`)
	if err != nil {
		h.log.Error("查询可用 UUID 失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, map[string]any{
			"uuid": mapStr(row, "uuid", ""), "node_count": mapInt(row, "node_count", 0),
			"owner": mapStr(row, "owner", ""), "remark": mapStr(row, "remark", ""),
			"email": mapStr(row, "email", ""), "status": mapStr(row, "status", "active"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}
