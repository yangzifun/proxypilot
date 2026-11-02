package api

import (
	"net/http"
)

// POST /api/mtproto/reconcile —— 检查 node_configs 与当前渲染是否漂移，漂移则修复
func (h *handlers) mtprotoReconcile(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT id, name FROM mtproto_nodes WHERE enabled = 1 ORDER BY id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点失败")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	drifted := 0
	for _, row := range rows {
		id := mapInt(row, "id", 0)
		name := mapStr(row, "name", "")
		changed, _, msg := h.applyMtprotoToNode(r.Context(), id)
		if msg != "" {
			out = append(out, map[string]any{"id": id, "name": name, "ok": false, "error": msg})
			continue
		}
		active := int64(0)
		_ = h.db.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM mtproto_clients WHERE node_id = ? AND status = 'active'`, id).Scan(&active)
		if changed {
			drifted++
		}
		out = append(out, map[string]any{
			"id": id, "name": name, "ok": true,
			"drift": changed, "fixed": changed, "clients": active,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "data": out, "drifted": drifted, "total": len(out),
	})
}

// GET /api/portal/mtproto —— 当前登录邮箱对应的 Telegram 代理链接
func (h *handlers) portalMtproto(w http.ResponseWriter, r *http.Request) {
	email, ok := h.portalSession(r)
	if !ok || email == "" {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	var uuid string
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(uuid,'') FROM uuid_info WHERE LOWER(email) = ? LIMIT 1`, email).Scan(&uuid)
	data := []map[string]any{}
	if uuid == "" {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "email": email, "data": data})
		return
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT c.*, n.name AS node_name, n.template_id AS node_template_id,
	      s.host AS server_host, s.last_ip AS server_last_ip, s.last_ip4 AS server_ip4
	    FROM mtproto_clients c
	    JOIN mtproto_nodes n ON n.id = c.node_id
	    LEFT JOIN servers s ON s.id = n.server_id
	    WHERE c.uuid = ? AND c.status = 'active' AND n.enabled = 1 ORDER BY c.id ASC`, uuid)
	if err != nil {
		h.log.Error("查询 Telegram 代理失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	for _, row := range rows {
		nodeID := mapInt(row, "node_id", 0)
		nodeRows, err := h.mtprotoNodesWithMeta(r.Context(), "WHERE n.id = ?", nodeID)
		if err != nil || len(nodeRows) == 0 {
			continue
		}
		tpl, _, err := h.loadMtprotoTemplate(r.Context(), mapInt(row, "node_template_id", 0))
		if err != nil {
			continue
		}
		eff := mtprotoEffective(tpl, mtprotoNodeFromRow(nodeRows[0]))
		client := &mtprotoClient{ID: mapInt(row, "id", 0), Secret: mapStr(row, "secret", "")}
		srv := mtprotoServer{
			LastIP4: mapStr(nodeRows[0], "server_ip4", ""),
			LastIP:  mapStr(nodeRows[0], "server_host", ""),
			Host:    mapStr(nodeRows[0], "server_host", ""),
		}
		link := mtprotoClientLink(&eff, srv, client)
		data = append(data, map[string]any{
			"id": mapInt(row, "id", 0), "node_id": nodeID, "node_name": mapStr(row, "node_name", ""),
			"host": mapStr(link, "host", ""), "port": mapInt(link, "port", 0),
			"tg": mapStr(link, "tg", ""), "tme": mapStr(link, "tme", ""),
			"secret":      mapStr(link, "secret", ""),
			"secret_hint": mtprotoMaskSecret(mapStr(row, "secret", "")),
			"created_at":  mapInt(row, "created_at", 0), "delivered_at": mapInt(row, "delivered_at", 0),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "email": email, "data": data})
}
