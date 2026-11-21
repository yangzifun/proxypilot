package api

import (
	"net/http"
)

// GET /api/node-configs/logs?config_id=&server_id=&limit=
func (h *handlers) nodeConfigLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	where := []string{"1=1"}
	args := []any{}
	if cid := intOrJS(q.Get("config_id"), 0, 0, 1<<40); cid > 0 {
		where = append(where, "config_id = ?")
		args = append(args, cid)
	}
	if sid := intOrJS(q.Get("server_id"), 0, 0, 1<<40); sid > 0 {
		where = append(where, "server_id = ?")
		args = append(args, sid)
	}
	limit := clampInt(int64(intOrJS(q.Get("limit"), 100, 1, 500)), 1, 500)
	args = append(args, limit)
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT * FROM node_push_logs WHERE `+joinAnd(where)+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询日志失败")
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, map[string]any{
			"id": mapInt(row, "id", 0), "config_id": mapInt(row, "config_id", 0),
			"server_id": mapInt(row, "server_id", 0), "action": mapStr(row, "action", ""),
			"status": mapStr(row, "status", ""), "exit_code": mapInt(row, "exit_code", 0),
			"message": mapStr(row, "message", ""), "output": mapStr(row, "output", ""),
			"actor": mapStr(row, "actor", ""), "created_at": mapInt(row, "created_at", 0),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "total": len(data), "data": data})
}
