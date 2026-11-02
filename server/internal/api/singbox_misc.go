package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b[:])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

// POST /api/singbox/templates/preview —— 模板级预览（不需要节点）
func (h *handlers) singboxTemplatePreview(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	input, msg := normalizeSingboxTemplateInput(body)
	if msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": msg})
		return
	}
	// 草稿预览允许占位 UUID：替换为随机 UUID，避免被守卫拒绝
	if usersJSON, _ := input["users_json"].(string); strings.Contains(usersJSON, singboxDemoUUID) {
		var users []sbUser
		if json.Unmarshal([]byte(usersJSON), &users) == nil {
			for i := range users {
				users[i].UUID = randomUUID()
			}
			if b, err := json.Marshal(users); err == nil {
				input["users_json"] = string(b)
			}
		}
	}
	tpl := inputToTemplate(input)
	node := singboxRenderNode{
		Name: "preview", Domain: firstNonEmpty(strings.TrimSpace(mapStr(body, "domain", "")), "preview.example.com"),
		SBPath: "/etc/sing-box/sb.json", CaddyPath: "/etc/sing-box/Caddyfile",
		ServiceSBName: "sing-box", ServiceCaddyName: "caddy-naive",
	}
	files, ports, _, msg := singboxRenderBundle(tpl, node, serverRow{})
	if msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": msg})
		return
	}
	view := make([]map[string]any, 0, len(files))
	for _, f := range files {
		view = append(view, singboxFileView(f))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ports": ports, "files": view})
}

// POST /api/singbox/nodes/{id}/warp —— -1 跟随模板 / 0 关 / 1 开
func (h *handlers) singboxNodeWarp(w http.ResponseWriter, r *http.Request) {
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
	wv := jsonInt(body["value"])
	if wv != -1 && wv != 0 && wv != 1 {
		writeError(w, http.StatusBadRequest, "value 只能是 -1（跟随模板）/ 0 / 1")
		return
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE singbox_nodes SET warp_enabled = ?, updated_at = ? WHERE id = ?`, wv, nowMillis(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT n.*, s.name AS server_name, s.last_ip AS server_host,
	      t.name AS template_name, t.warp_enabled AS template_warp
	    FROM singbox_nodes n LEFT JOIN servers s ON s.id = n.server_id
	    LEFT JOIN singbox_templates t ON t.id = n.template_id WHERE n.id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	row := rows[0]
	effective := wv
	if effective != 0 && effective != 1 {
		effective = 0
		if mapInt(row, "template_warp", 0) == 1 {
			effective = 1
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "data": singboxNodeToJSON(row), "warp_effective": effective,
		"note": "已保存；下次下发生效",
	})
}
