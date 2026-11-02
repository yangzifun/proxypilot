package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
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
		linkNode := singboxNode{Name: node.Name, Domain: node.Domain, CDNDomain: node.CDNDomain}
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

		existing := map[string]map[string]any{}
		if rows2, err := queryRowsAsMaps(r.Context(), h.db,
			`SELECT id, uuid, config_data FROM configs WHERE singbox_node_id = ?`, nodeID); err == nil {
			for _, e := range rows2 {
				existing[mapStr(e, "uuid", "")+"\x00"+mapStr(e, "config_data", "")] = e
			}
		}
		wanted := map[string]bool{}
		for _, l := range desired {
			wanted[mapStr(l, "user", "")+"\x00"+mapStr(l, "link", "")] = true
		}

		removed := int64(0)
		for key, e := range existing {
			if wanted[key] {
				continue
			}
			if _, err := h.db.ExecContext(r.Context(), `DELETE FROM configs WHERE id = ?`, mapInt(e, "id", 0)); err == nil {
				removed++
			}
		}

		added := int64(0)
		now := time.Now().UnixMilli()
		for _, l := range desired {
			link := mapStr(l, "link", "")
			uuid := mapStr(l, "user", "")
			// l.user 是用户名，不是 UUID；从 users 里按名字反查
			uuid = ""
			for _, u := range users {
				if u.Name == mapStr(l, "user", "") {
					uuid = u.UUID
					break
				}
			}
			if uuid == "" {
				continue
			}
			key := uuid + "\x00" + link
			if existing[key] != nil {
				continue
			}
			var dup int64
			_ = h.db.QueryRowContext(r.Context(),
				`SELECT COUNT(*) FROM configs WHERE uuid = ? AND config_data = ?`, uuid, link).Scan(&dup)
			if dup > 0 {
				continue
			}
			remark := nodeName + "-" + mapStr(l, "protocol", "")
			if idx := strings.Index(link, "#"); idx >= 0 {
				if decoded, err := url.QueryUnescape(link[idx+1:]); err == nil && decoded != "" {
					remark = decoded
				}
			}
			if _, err := h.db.ExecContext(r.Context(), `INSERT INTO configs
			  (uuid, config_data, protocol, remark, domain_hosting, singbox_node_id, created_at, updated_at)
			  VALUES (?, ?, ?, ?, 'Cloudflare', ?, ?, ?)`,
				uuid, link, singboxProtocolOf(mapStr(l, "protocol", "")), truncateRunes(remark, 500), nodeID, now, now); err == nil {
				added++
			}
		}

		_, _ = h.db.ExecContext(r.Context(),
			`UPDATE singbox_nodes SET last_link_hash = ?, updated_at = ? WHERE id = ?`, digest, now, nodeID)
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
