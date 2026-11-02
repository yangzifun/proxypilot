package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

func singboxBool(v any, dflt bool) int64 {
	if v == nil {
		if dflt {
			return 1
		}
		return 0
	}
	switch x := v.(type) {
	case bool:
		if x {
			return 1
		}
		return 0
	case float64:
		if x != 0 {
			return 1
		}
		return 0
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		if s == "" {
			if dflt {
				return 1
			}
			return 0
		}
		if s == "1" || s == "true" || s == "yes" || s == "on" {
			return 1
		}
		return 0
	}
	if dflt {
		return 1
	}
	return 0
}

func randomB64Key() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return base64.StdEncoding.EncodeToString(buf)
}

func randomRealityKey() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

var hostnameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$`)

func normalizeDomain(raw string) string {
	s := strings.TrimSpace(raw)
	s = regexp.MustCompile(`(?i)^https?://`).ReplaceAllString(s, "")
	s = strings.SplitN(s, "/", 2)[0]
	if s == "" || len(s) > 253 || !strings.Contains(s, ".") || strings.Contains(s, "..") || !hostnameRe.MatchString(s) {
		return ""
	}
	return s
}

type sbTemplateInput map[string]any

func normalizeSingboxTemplateInput(body map[string]any) (sbTemplateInput, string) {
	name := mapStr(body, "name", "")
	if name == "" {
		return nil, "模板名称不能为空"
	}
	var rawUsers []sbUser
	switch v := body["users_json"].(type) {
	case string:
		var arr []sbUser
		_ = json.Unmarshal([]byte(v), &arr)
		rawUsers = arr
	case []any:
		b, _ := json.Marshal(v)
		var arr []sbUser
		_ = json.Unmarshal(b, &arr)
		rawUsers = arr
	}
	users := singboxParseUsers(rawUsers)
	if len(users) == 0 {
		return nil, "请至少填写一个用户（users_json，如 [{\"name\":\"u1\",\"uuid\":\"...\",\"ss_password\":\"...\"}]）"
	}
	usersJSON, _ := json.Marshal(users)

	opts := map[string]any{}
	switch v := body["protocol_options"].(type) {
	case string:
		_ = json.Unmarshal([]byte(v), &opts)
	case map[string]any:
		opts = v
	}
	optsJSON, _ := json.Marshal(opts)

	ssKey := mapStr(body, "ss_server_key", "")
	if raw, ok := decodeAnyBase64(ssKey); !ok || len(raw) != 32 {
		ssKey = randomB64Key()
	}
	realityKey := strings.TrimSpace(mapStr(body, "reality_private_key", ""))
	realityKey = strings.TrimRight(realityKey, "=")
	if len(realityKey) != 43 {
		realityKey = randomRealityKey()
	}
	realityPub := strings.TrimSpace(mapStr(body, "reality_public_key", ""))
	if realityPub == "" {
		realityPub = x25519PublicKeyFromPrivate(realityKey)
	}

	return sbTemplateInput{
		"name":                 name,
		"remark":               mapStr(body, "remark", ""),
		"users_json":           string(usersJSON),
		"ss_method":            firstNonEmpty(strings.TrimSpace(mapStr(body, "ss_method", "")), singboxDefaultSSMethod),
		"ss_server_key":        ssKey,
		"reality_private_key":  realityKey,
		"reality_public_key":   realityPub,
		"reality_short_id":     mapStr(body, "reality_short_id", ""),
		"reality_dest":         firstNonEmpty(mapStr(body, "reality_dest", ""), "www.microsoft.com:443"),
		"reality_sni":          firstNonEmpty(mapStr(body, "reality_sni", ""), "www.microsoft.com"),
		"ws_sni":               firstNonEmpty(mapStr(body, "ws_sni", ""), "www.bing.com"),
		"disguise_url":         firstNonEmpty(mapStr(body, "disguise_url", ""), "https://www.bing.com"),
		"cert_path":            firstNonEmpty(mapStr(body, "cert_path", ""), "/etc/sing-box/cert.crt"),
		"key_path":             firstNonEmpty(mapStr(body, "key_path", ""), "/etc/sing-box/private.key"),
		"protocol_options":     string(optsJSON),
		"port_base":            intOrJS(body["port_base"], 13700, 1, 65000),
		"warp_base":            intOrJS(body["warp_base"], 13800, 1, 65000),
		"enable_ss":            singboxBool(body["enable_ss"], true),
		"enable_anytls":        singboxBool(body["enable_anytls"], true),
		"enable_hy2":           singboxBool(body["enable_hy2"], true),
		"enable_reality":       singboxBool(body["enable_reality"], true),
		"enable_vlws":          singboxBool(body["enable_vlws"], true),
		"enable_vmess":         singboxBool(body["enable_vmess"], true),
		"enable_masque":        singboxBool(body["enable_masque"], false),
		"singbox_version":      strings.TrimSpace(mapStr(body, "singbox_version", "")),
		"enable_singbox":       singboxBool(body["enable_singbox"], true),
		"warp_engines":         normalizeWarpEngines(mapStr(body, "warp_engines", "singbox")),
		"warp_enabled":         singboxBool(body["warp_enabled"], false),
		"warp_private_key":     mapStr(body, "warp_private_key", ""),
		"warp_address":         mapStr(body, "warp_address", ""),
		"warp_peer_public_key": mapStr(body, "warp_peer_public_key", ""),
		"enabled":              singboxBool(body["enabled"], true),
	}, ""
}

// normalizeWarpEngines V15.6 起只保留 sing-box（Xray 已下线）。
func normalizeWarpEngines(raw string) string {
	return "singbox"
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// insertDynamic 以白名单 map 动态插入（列名来自代码内部，不接受用户输入）。
func insertDynamic(ctx context.Context, db *sql.DB, table string, input map[string]any, now int64) (int64, error) {
	cols := make([]string, 0, len(input)+2)
	vals := make([]any, 0, len(input)+2)
	for k, v := range input {
		cols = append(cols, "`"+k+"`")
		vals = append(vals, v)
	}
	cols = append(cols, "created_at", "updated_at")
	vals = append(vals, now, now)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")
	res, err := db.ExecContext(ctx, "INSERT INTO `"+table+"` ("+strings.Join(cols, ", ")+") VALUES ("+placeholders+")", vals...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func updateDynamic(ctx context.Context, db *sql.DB, table string, id int64, input map[string]any, now int64) error {
	sets := make([]string, 0, len(input)+1)
	vals := make([]any, 0, len(input)+1)
	for k, v := range input {
		sets = append(sets, "`"+k+"` = ?")
		vals = append(vals, v)
	}
	sets = append(sets, "updated_at = ?")
	vals = append(vals, now, id)
	_, err := db.ExecContext(ctx, "UPDATE `"+table+"` SET "+strings.Join(sets, ", ")+" WHERE id = ?", vals...)
	return err
}

func (h *handlers) singboxTemplateCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	input, msg := normalizeSingboxTemplateInput(body)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var dup int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM singbox_templates WHERE name = ?`, input["name"]).Scan(&dup)
	if dup > 0 {
		writeError(w, http.StatusConflict, "模板名称已存在")
		return
	}
	id, err := insertDynamic(r.Context(), h.db, "singbox_templates", input, nowMillis())
	if err != nil {
		h.log.Error("创建 sing-box 模板失败", "err", err)
		writeError(w, http.StatusInternalServerError, "创建失败")
		return
	}
	h.writeSingboxTemplate(w, r, id, "创建失败")
}

func (h *handlers) singboxTemplateUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	input, msg := normalizeSingboxTemplateInput(body)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var dup int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM singbox_templates WHERE name = ? AND id <> ?`, input["name"], id).Scan(&dup)
	if dup > 0 {
		writeError(w, http.StatusConflict, "模板名称已存在")
		return
	}
	if err := updateDynamic(r.Context(), h.db, "singbox_templates", id, input, nowMillis()); err != nil {
		writeError(w, http.StatusInternalServerError, "更新失败")
		return
	}
	h.writeSingboxTemplate(w, r, id, "更新失败")
}

func (h *handlers) writeSingboxTemplate(w http.ResponseWriter, r *http.Request, id int64, errMsg string) {
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT * FROM singbox_templates WHERE id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusInternalServerError, errMsg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": singboxTemplateToJSON(rows[0], false)})
}

func (h *handlers) singboxTemplateDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	var used int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM singbox_nodes WHERE template_id = ?`, id).Scan(&used)
	if used > 0 {
		writeError(w, http.StatusConflict, "该模板已被 "+strconv.FormatInt(used, 10)+" 个节点绑定，请先解绑")
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `DELETE FROM singbox_templates WHERE id = ?`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func normalizeSingboxNodeInput(body map[string]any) (map[string]any, string) {
	name := mapStr(body, "name", "")
	if name == "" {
		return nil, "节点名称不能为空"
	}
	serverID := jsonInt(body["server_id"])
	if serverID <= 0 {
		return nil, "请选择目标服务器"
	}
	templateID := jsonInt(body["template_id"])
	cdnRaw := strings.TrimSpace(mapStr(body, "cdn_domain", ""))
	cdn := ""
	if cdnRaw != "" {
		cdn = normalizeDomain(cdnRaw)
		if cdn == "" {
			return nil, "CDN 域名无效：只填域名（如 cdn.example.com），不要带协议、路径或 IP"
		}
	}
	warp := int64(-1)
	if v, ok := body["warp_enabled"]; ok && v != nil {
		w := jsonInt(v)
		if w == -1 || w == 0 || w == 1 {
			warp = w
		}
	}
	return map[string]any{
		"name": name, "server_id": serverID, "template_id": templateID,
		"domain":        strings.TrimSpace(mapStr(body, "domain", "")),
		"cdn_domain":    cdn,
		"port_base":     intOrJS(body["port_base"], 0, 0, 65000),
		"warp_base":     intOrJS(body["warp_base"], 0, 0, 65000),
		"sb_path":       firstNonEmpty(mapStr(body, "sb_path", ""), "/etc/sing-box/sb.json"),
		"caddy_path":    firstNonEmpty(mapStr(body, "caddy_path", ""), "/etc/sing-box/Caddyfile"),
		"service_sb":    firstNonEmpty(mapStr(body, "service_sb", ""), "sing-box"),
		"service_caddy": firstNonEmpty(mapStr(body, "service_caddy", ""), "caddy-naive"),
		"enabled":       singboxBool(body["enabled"], true),
		"warp_enabled":  warp,
		"remark":        strings.TrimSpace(mapStr(body, "remark", "")),
	}, ""
}

func (h *handlers) singboxNodeCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	input, msg := normalizeSingboxNodeInput(body)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var dup int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM singbox_nodes WHERE name = ?`, input["name"]).Scan(&dup)
	if dup > 0 {
		writeError(w, http.StatusConflict, "节点名称已存在")
		return
	}
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM singbox_nodes WHERE server_id = ?`, input["server_id"]).Scan(&dup)
	if dup > 0 {
		writeError(w, http.StatusConflict, "该服务器已绑定过一个 sing-box 节点")
		return
	}
	if tid, _ := input["template_id"].(int64); tid <= 0 {
		var firstID int64
		if err := h.db.QueryRowContext(r.Context(),
			`SELECT id FROM singbox_templates WHERE enabled = 1 ORDER BY id ASC LIMIT 1`).Scan(&firstID); err == nil {
			input["template_id"] = firstID
		}
	}
	var exists int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM singbox_templates WHERE id = ?`, input["template_id"]).Scan(&exists)
	if exists == 0 {
		writeError(w, http.StatusNotFound, "协议模板不存在")
		return
	}
	var serverHost, serverDomain string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(host,''), COALESCE(singbox_domain,'') FROM servers WHERE id = ? LIMIT 1`,
		input["server_id"]).Scan(&serverHost, &serverDomain)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "目标服务器不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询服务器失败")
		return
	}
	if s, _ := input["domain"].(string); strings.TrimSpace(s) == "" {
		input["domain"] = firstNonEmpty(strings.TrimSpace(serverDomain), strings.TrimSpace(serverHost))
	}
	id, err := insertDynamic(r.Context(), h.db, "singbox_nodes", input, nowMillis())
	if err != nil {
		h.log.Error("创建 sing-box 节点失败", "err", err)
		writeError(w, http.StatusInternalServerError, "创建失败")
		return
	}
	h.writeSingboxNode(w, r, id)
}

func (h *handlers) singboxNodeUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	input, msg := normalizeSingboxNodeInput(body)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var dup int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM singbox_nodes WHERE name = ? AND id <> ?`, input["name"], id).Scan(&dup)
	if dup > 0 {
		writeError(w, http.StatusConflict, "节点名称已存在")
		return
	}
	if err := updateDynamic(r.Context(), h.db, "singbox_nodes", id, input, nowMillis()); err != nil {
		writeError(w, http.StatusInternalServerError, "更新失败")
		return
	}
	h.writeSingboxNode(w, r, id)
}

func (h *handlers) writeSingboxNode(w http.ResponseWriter, r *http.Request, id int64) {
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT n.*, s.name AS server_name, s.last_ip AS server_host,
	      t.name AS template_name, t.warp_enabled AS template_warp
	    FROM singbox_nodes n LEFT JOIN servers s ON s.id = n.server_id
	    LEFT JOIN singbox_templates t ON t.id = n.template_id WHERE n.id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusInternalServerError, "读取节点失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": singboxNodeToJSON(rows[0])})
}

func (h *handlers) singboxNodeDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `DELETE FROM singbox_nodes WHERE id = ?`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
