package api

import (
	"net/http"
)

// GET /api/mtproto/uuids
func (h *handlers) mtprotoUUIDs(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT u.uuid AS uuid, COUNT(c.id) AS node_count,
	      MAX(i.owner) AS owner, MAX(i.remark) AS remark, MAX(i.email) AS email, MAX(i.status) AS status
	    FROM (SELECT uuid FROM uuid_info WHERE uuid IS NOT NULL AND uuid != ''
	          UNION SELECT uuid FROM configs WHERE uuid IS NOT NULL AND uuid != '') u
	    LEFT JOIN configs c ON c.uuid = u.uuid
	    LEFT JOIN uuid_info i ON i.uuid = u.uuid
	    GROUP BY u.uuid ORDER BY node_count DESC, u.uuid ASC LIMIT 500`)
	if err != nil {
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

// POST /api/mtproto/nodes/{id}/preview
func (h *handlers) mtprotoNodePreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	rows, err := h.mtprotoNodesWithMeta(r.Context(), "WHERE n.id = ?", id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	tpl, _, err := h.loadMtprotoTemplate(r.Context(), mapInt(rows[0], "template_id", 0))
	if err != nil {
		writeError(w, http.StatusNotFound, "协议模板不存在")
		return
	}
	node := mtprotoNodeFromRow(rows[0])
	eff := mtprotoEffective(tpl, node)
	var clients []mtprotoClient
	if cr, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT * FROM mtproto_clients WHERE node_id = ? ORDER BY id ASC`, id); err == nil {
		for _, c := range cr {
			clients = append(clients, mtprotoClient{
				ID: mapInt(c, "id", 0), UUID: mapStr(c, "uuid", ""), Email: mapStr(c, "email", ""),
				Label: mapStr(c, "label", ""), Secret: mapStr(c, "secret", ""),
				Status: firstNonEmpty(mapStr(c, "status", ""), "active"),
			})
		}
	}
	content, ports, mode, msg := mtprotoRenderConfig(&eff, clients)
	if msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "content": content, "ports": ports, "mode": mode,
		"clients": func() []map[string]any {
			out := make([]map[string]any, 0, len(clients))
			for _, c := range clients {
				out = append(out, map[string]any{"id": c.ID, "uuid": c.UUID, "email": c.Email, "label": c.Label})
			}
			return out
		}(),
	})
}

// GET /api/mtproto/clients/{id}/link
func (h *handlers) mtprotoClientLinkHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "客户端不存在")
		return
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT * FROM mtproto_clients WHERE id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "客户端不存在")
		return
	}
	clientRow := rows[0]
	nodeRows, err := h.mtprotoNodesWithMeta(r.Context(), "WHERE n.id = ?", mapInt(clientRow, "node_id", 0))
	if err != nil || len(nodeRows) == 0 {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	tpl, _, err := h.loadMtprotoTemplate(r.Context(), mapInt(nodeRows[0], "template_id", 0))
	if err != nil {
		writeError(w, http.StatusNotFound, "协议模板不存在")
		return
	}
	node := mtprotoNodeFromRow(nodeRows[0])
	eff := mtprotoEffective(tpl, node)
	client := &mtprotoClient{
		ID: mapInt(clientRow, "id", 0), UUID: mapStr(clientRow, "uuid", ""),
		Email: mapStr(clientRow, "email", ""), Label: mapStr(clientRow, "label", ""),
		Secret: mapStr(clientRow, "secret", ""), Status: firstNonEmpty(mapStr(clientRow, "status", ""), "active"),
	}
	srv := mtprotoServer{
		LastIP4: mapStr(nodeRows[0], "server_ip4", ""),
		LastIP:  mapStr(nodeRows[0], "server_host", ""),
		Host:    mapStr(nodeRows[0], "server_host", ""),
	}
	link := mtprotoClientLink(&eff, srv, client)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": link})
}

func (h *handlers) mtprotoClientSetStatus(w http.ResponseWriter, r *http.Request, status string) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "客户端不存在")
		return
	}
	revokedAt := int64(0)
	if status == "revoked" {
		revokedAt = nowMillis()
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE mtproto_clients SET status = ?, revoked_at = ?, updated_at = ? WHERE id = ?`,
		status, revokedAt, nowMillis(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "更新失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "status": status})
}

// POST /api/mtproto/clients/{id}/revoke
func (h *handlers) mtprotoClientRevoke(w http.ResponseWriter, r *http.Request) {
	h.mtprotoClientSetStatus(w, r, "revoked")
}

// POST /api/mtproto/clients/{id}/restore
func (h *handlers) mtprotoClientRestore(w http.ResponseWriter, r *http.Request) {
	h.mtprotoClientSetStatus(w, r, "active")
}

// POST /api/mtproto/clients/{id}/rotate
func (h *handlers) mtprotoClientRotate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "客户端不存在")
		return
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT * FROM mtproto_clients WHERE id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "客户端不存在")
		return
	}
	domain := mtprotoSecretDomain(mapStr(rows[0], "secret", ""))
	if domain == "" {
		var nodeDomain string
		_ = h.db.QueryRowContext(r.Context(),
			`SELECT COALESCE(n.domain,'') FROM mtproto_nodes n WHERE n.id = ?`, mapInt(rows[0], "node_id", 0)).Scan(&nodeDomain)
		domain = nodeDomain
	}
	secret := mtprotoGenerateSecret(domain)
	if secret == "" {
		writeError(w, http.StatusBadRequest, "生成 secret 失败：缺少有效伪装域名")
		return
	}
	_, _ = h.db.ExecContext(r.Context(),
		`UPDATE mtproto_clients SET secret = ?, updated_at = ? WHERE id = ?`, secret, nowMillis(), id)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"id": id, "secret": secret, "secret_domain": mtprotoSecretDomain(secret),
	}})
}
