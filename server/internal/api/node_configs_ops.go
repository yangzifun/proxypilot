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

// POST /api/node-configs/preview —— 渲染模板（可带 server_id 变量）
func (h *handlers) nodeConfigPreview(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	tplID := jsonInt(body["template_id"])
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT * FROM node_templates WHERE id = ? LIMIT 1`, tplID)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	row := rows[0]
	content := mapStr(row, "content", "")
	vars := map[string]string{}
	if sid := jsonInt(body["server_id"]); sid > 0 {
		if srv, err := h.fetchAgentServer(r.Context(), sid); err == nil {
			cfg := &nodeDeployConfig{
				Name:        mapStr(body, "name", "preview"),
				Engine:      mapStr(row, "engine", "other"),
				TargetPath:  firstNonEmpty(mapStr(body, "target_path", ""), mapStr(row, "target_path", "")),
				ServiceName: firstNonEmpty(mapStr(body, "service_name", ""), mapStr(row, "service_name", "")),
			}
			vars = h.nodeConfigVars(cfg, srv)
		}
	}
	rendered := renderNodeContent(content, vars)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "content": rendered, "vars": vars,
		"engine": mapStr(row, "engine", "other"), "target_path": mapStr(row, "target_path", ""),
	})
}

// POST /api/node-configs/push {config_id} 或 {config_ids:[...]}
func (h *handlers) nodeConfigPush(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	ids := []int64{}
	if id := jsonInt(body["config_id"]); id > 0 {
		ids = append(ids, id)
	}
	for _, v := range jsonSlice(body["config_ids"]) {
		if id := jsonInt(v); id > 0 && len(ids) < 100 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "请选择要下发的配置")
		return
	}
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		cfg, err := h.loadNodeConfig(r.Context(), id)
		if err != nil {
			out = append(out, map[string]any{"id": id, "ok": false, "error": "配置不存在"})
			continue
		}
		srv, err := h.fetchAgentServer(r.Context(), cfg.ServerID)
		if err != nil {
			out = append(out, map[string]any{"id": id, "ok": false, "error": "服务器不存在"})
			continue
		}
		payload := h.agentBuildDeployPayload(r.Context(), cfg, srv)
		taskID, dup, msg := h.agentQueueTask(r.Context(), srv, "deploy", cfg.ID, payload, "admin")
		out = append(out, map[string]any{
			"id": id, "name": cfg.Name, "ok": msg == "", "task_id": taskID,
			"duplicate": dup, "error": msg,
		})
	}
	okCount := 0
	for _, x := range out {
		if ok, _ := x["ok"].(bool); ok {
			okCount++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": okCount > 0, "data": out, "ok_count": okCount})
}
