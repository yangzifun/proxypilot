package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

func singboxProtocolOf(linkProtocol string) string {
	p := strings.ToLower(strings.TrimSpace(linkProtocol))
	switch {
	case p == "shadowsocks", p == "anytls", p == "hysteria2":
		return p
	case p == "vmess-ws":
		return "vmess"
	case strings.HasPrefix(p, "vless"):
		return "vless"
	}
	if p == "" {
		return "other"
	}
	return p
}

// POST /api/singbox/reconcile
func (h *handlers) singboxReconcile(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = decodeJSON(w, r, &body)
	if body == nil {
		body = map[string]any{}
	}
	force, _ := body["force"].(bool)
	onlyNode := jsonInt(body["node_id"])

	sqlText := `SELECT n.* FROM singbox_nodes n WHERE n.enabled = 1`
	args := []any{}
	if onlyNode > 0 {
		sqlText = `SELECT n.* FROM singbox_nodes n WHERE n.id = ?`
		args = append(args, onlyNode)
	}
	sqlText += ` ORDER BY n.id ASC`
	rows, err := queryRowsAsMaps(r.Context(), h.db, sqlText, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点失败")
		return
	}

	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		nodeID := mapInt(row, "id", 0)
		nodeName := mapStr(row, "name", "")
		tpl, node, _, _, msg := h.loadSingboxNodeFull(r.Context(), nodeID)
		if msg != "" {
			out = append(out, map[string]any{"id": nodeID, "name": nodeName, "ok": false, "error": msg})
			continue
		}
		linkNode := singboxNodeFromRender(node)
		users := singboxParseUsers(currentTemplateUsers(tpl))
		var uuidList []string
		if raw := mapStr(row, "uuid_list", ""); raw != "" {
			var arr []string
			if json.Unmarshal([]byte(raw), &arr) == nil {
				uuidList = arr
			}
		}
		if len(uuidList) > 0 {
			users = singboxUsersFromUUIDs(tpl, uuidList)
		}

		desired := []map[string]any{}
		for _, u := range users {
			desired = append(desired, singboxClientLinks(tpl, linkNode, []sbUser{u})...)
		}
		digestParts := make([]string, 0, len(desired))
		for _, l := range desired {
			digestParts = append(digestParts, mapStr(l, "link", ""))
		}
		digest := sha256Hex(strings.Join(digestParts, "\n"))

		if !force && mapStr(row, "last_link_hash", "") == digest && len(desired) > 0 {
			out = append(out, map[string]any{"id": nodeID, "name": nodeName, "ok": true, "skipped": true, "added": 0, "removed": 0, "digest": digest})
			continue
		}

		// 统一走 syncSingboxConfigsForUsers：内部用 UUID 映射做 wanted 集合，
		// 避免旧实现用“用户名+链接”去和“UUID+链接”比较导致误删全部配置。
		added, removed, err := h.syncSingboxConfigsForUsers(r.Context(), nodeID, nodeName, tpl, node, users)
		if err != nil {
			out = append(out, map[string]any{"id": nodeID, "name": nodeName, "ok": false, "error": err.Error()})
			continue
		}
		out = append(out, map[string]any{
			"id": nodeID, "name": nodeName, "ok": true, "users": len(users),
			"added": added, "removed": removed, "digest": digest,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

// POST /api/singbox/nodes/{id}/deploy —— 单节点立即下发并排队
func (h *handlers) singboxNodeDeploy(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	var body map[string]any
	_ = decodeJSON(w, r, &body)
	force := false
	if body != nil {
		if v, ok := body["force"].(bool); ok {
			force = v
		}
	}
	files, ports, users, changed, msg := h.applySingboxNode(r.Context(), id, nil, force)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	queued := h.queueSingboxDeploy(r.Context(), files, "deploy")
	okQueued := 0
	for _, q := range queued {
		if ok, _ := q["ok"].(bool); ok {
			okQueued++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "changed": changed, "queued": okQueued,
		"ports": ports, "users": users, "files": files, "tasks": queued,
	})
}
