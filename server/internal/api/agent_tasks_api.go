package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// GET /api/agent/tasks?server_id=&status=&limit=
func (h *handlers) agentTasksList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := clampInt(int64(intOrJS(q.Get("limit"), 100, 1, 200)), 1, 200)
	where := []string{"1=1"}
	args := []any{}
	if sid := intOrJS(q.Get("server_id"), 0, 0, 1<<40); sid > 0 {
		where = append(where, "t.server_id = ?")
		args = append(args, sid)
	}
	if status := q.Get("status"); status != "" {
		where = append(where, "t.status = ?")
		args = append(args, status)
	}
	query := `SELECT t.*, s.name AS server_name, c.name AS config_name
	    FROM agent_tasks t
	    LEFT JOIN servers s ON s.id = t.server_id
	    LEFT JOIN node_configs c ON c.id = t.config_id
	    WHERE ` + joinAnd(where) + ` ORDER BY t.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := queryRowsAsMaps(r.Context(), h.db, query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询任务失败")
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, agentTaskJSON(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "total": len(data), "data": data})
}

func joinAnd(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " AND "
		}
		out += p
	}
	return out
}

func agentTaskJSON(row map[string]any) map[string]any {
	var payload any = map[string]any{}
	if raw := mapStr(row, "payload", ""); raw != "" {
		_ = json.Unmarshal([]byte(raw), &payload)
	}
	return map[string]any{
		"id": mapInt(row, "id", 0), "server_id": mapInt(row, "server_id", 0),
		"server_name": mapStr(row, "server_name", ""),
		"config_id":   mapInt(row, "config_id", 0), "config_name": mapStr(row, "config_name", ""),
		"type": mapStr(row, "type", ""), "status": mapStr(row, "status", ""),
		"attempts": mapInt(row, "attempts", 0), "exit_code": mapInt(row, "exit_code", 0),
		"output": truncateRunes(mapStr(row, "output", ""), 2000),
		"actor":  mapStr(row, "actor", ""),
		"payload": func() map[string]any {
			p, _ := payload.(map[string]any)
			if p == nil {
				return map[string]any{}
			}
			return map[string]any{
				"name": mapStr(p, "name", ""), "path": mapStr(p, "path", ""),
				"version": mapInt(p, "version", 0), "sha256": mapStr(p, "sha256", ""),
				"backup": p["backup"], "service_name": mapStr(p, "service_name", ""),
				"restart_cmd": mapStr(p, "restart_cmd", ""), "check_cmd": mapStr(p, "check_cmd", ""),
				"command": mapStr(p, "command", ""),
				"bytes":   len(mapStr(p, "content", "")),
			}
		}(),
		"created_at": mapInt(row, "created_at", 0), "sent_at": mapInt(row, "sent_at", 0),
		"finished_at": mapInt(row, "finished_at", 0),
	}
}

// POST /api/agent/tasks/{id}/cancel
func (h *handlers) agentTaskCancel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}
	var status string
	if err := h.db.QueryRowContext(r.Context(), `SELECT COALESCE(status,'') FROM agent_tasks WHERE id = ?`, id).Scan(&status); err != nil {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}
	switch status {
	case "done", "failed", "expired", "skipped":
		writeError(w, http.StatusBadRequest, "任务已结束")
		return
	}
	_, _ = h.db.ExecContext(r.Context(),
		`UPDATE agent_tasks SET status = 'failed', output = ?, finished_at = ? WHERE id = ?`,
		"后台取消", nowMillis(), id)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "任务已取消"})
}

// POST /api/agent/tasks {server_id, type, service_name?, command?, timeout?}
// 服务器监控里的「重启服务」等固定动作；exec 仍受 allow_exec 限制。
func (h *handlers) agentTaskCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	srvID := jsonInt(body["server_id"])
	if srvID <= 0 {
		writeError(w, http.StatusBadRequest, "缺少 server_id")
		return
	}
	typ := strings.ToLower(strings.TrimSpace(mapStr(body, "type", "")))
	srv, err := h.fetchAgentServer(r.Context(), srvID)
	if err != nil {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}
	if !strings.EqualFold(srv.LinkMode, "agent") && !strings.EqualFold(srv.LinkMode, "local") {
		writeError(w, http.StatusBadRequest, "仅支持 Agent / 本地接入方式")
		return
	}
	var payload map[string]any
	var taskType string
	message := ""
	switch typ {
	case "restart":
		svc := strings.TrimSpace(mapStr(body, "service_name", ""))
		if svc == "" {
			writeError(w, http.StatusBadRequest, "缺少 service_name")
			return
		}
		if !validServiceName(svc) {
			writeError(w, http.StatusBadRequest, "服务名不合法")
			return
		}
		payload = map[string]any{"service_name": svc, "restart_cmd": "systemctl restart " + svc}
		taskType = "restart"
		message = "已排队重启 " + svc
	case "exec":
		if srv.AllowExec != 1 {
			writeError(w, http.StatusForbidden, "该服务器未开启「允许执行命令」（allow_exec）")
			return
		}
		cmd := strings.TrimSpace(mapStr(body, "command", ""))
		if cmd == "" {
			writeError(w, http.StatusBadRequest, "命令为空")
			return
		}
		timeout := jsonInt(body["timeout"])
		if timeout <= 0 {
			timeout = 30
		}
		payload = map[string]any{"command": cmd, "timeout": timeout}
		taskType = "exec"
		message = "已排队执行命令"
	default:
		writeError(w, http.StatusBadRequest, "不支持的任务类型")
		return
	}
	taskID, dup, msg := h.agentQueueTask(r.Context(), srv, taskType, 0, payload, "admin")
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	h.recordNodeLog(r.Context(), 0, srvID, "agent-"+taskType, "success", 0, message, "", "admin")
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "queued": true, "task_id": taskID, "duplicate": dup, "message": message,
	})
}

// validServiceName 只允许 systemd 服务名常见字符，避免命令注入。
func validServiceName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '@' || c == '.' || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}
