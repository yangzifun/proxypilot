package api

import (
	"net/http"
)

// GET /api/node-configs?server_id=
func (h *handlers) nodeConfigsList(w http.ResponseWriter, r *http.Request) {
	where := ""
	args := []any{}
	if sid := intOrJS(r.URL.Query().Get("server_id"), 0, 0, 1<<40); sid > 0 {
		where = " WHERE c.server_id = ?"
		args = append(args, sid)
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT c.id, c.name, c.server_id, c.engine,
	      c.target_path, c.service_name, c.version, c.applied_version, c.enabled, c.auto_push,
	      c.last_push_at, c.last_push_status, c.last_push_message, c.updated_at,
	      s.name AS server_name, t.name AS template_name,
	      LENGTH(c.content) AS content_bytes
	    FROM node_configs c
	    LEFT JOIN servers s ON s.id = c.server_id
	    LEFT JOIN node_templates t ON t.id = c.template_id`+where+` ORDER BY c.id ASC`, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询配置失败")
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, map[string]any{
			"id": mapInt(row, "id", 0), "name": mapStr(row, "name", ""),
			"server_id": mapInt(row, "server_id", 0), "server_name": mapStr(row, "server_name", ""),
			"template_name": mapStr(row, "template_name", ""),
			"engine":        mapStr(row, "engine", "other"), "target_path": mapStr(row, "target_path", ""),
			"service_name": mapStr(row, "service_name", ""), "version": mapInt(row, "version", 0),
			"applied_version": mapInt(row, "applied_version", 0), "enabled": mapInt(row, "enabled", 1),
			"auto_push": mapInt(row, "auto_push", 0), "last_push_at": mapInt(row, "last_push_at", 0),
			"last_push_status": mapStr(row, "last_push_status", ""), "last_push_message": mapStr(row, "last_push_message", ""),
			"content_bytes": mapInt(row, "content_bytes", 0), "updated_at": mapInt(row, "updated_at", 0),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}
