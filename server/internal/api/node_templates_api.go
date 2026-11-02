package api

import (
	"net/http"
	"strings"
)

func (h *handlers) nodeTemplatesList(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT t.*,
	      (SELECT COUNT(*) FROM node_configs c WHERE c.template_id = t.id) AS usage_count
	    FROM node_templates t ORDER BY t.id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询模板失败")
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, map[string]any{
			"id": mapInt(row, "id", 0), "name": mapStr(row, "name", ""),
			"engine": mapStr(row, "engine", "other"), "target_path": mapStr(row, "target_path", ""),
			"service_name": mapStr(row, "service_name", ""), "restart_cmd": mapStr(row, "restart_cmd", ""),
			"check_cmd": mapStr(row, "check_cmd", ""), "content": mapStr(row, "content", ""),
			"backup": mapInt(row, "backup", 1), "remark": mapStr(row, "remark", ""),
			"enabled": mapInt(row, "enabled", 1), "usage_count": mapInt(row, "usage_count", 0),
			"created_at": mapInt(row, "created_at", 0), "updated_at": mapInt(row, "updated_at", 0),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

func normalizeNodeTemplateInput(body map[string]any) (map[string]any, string) {
	name := strings.TrimSpace(mapStr(body, "name", ""))
	if name == "" {
		return nil, "模板名称不能为空"
	}
	target := strings.TrimSpace(mapStr(body, "target_path", ""))
	if target == "" {
		return nil, "目标路径不能为空"
	}
	return map[string]any{
		"name": name, "engine": firstNonEmpty(strings.TrimSpace(mapStr(body, "engine", "")), "other"),
		"target_path": target, "service_name": mapStr(body, "service_name", ""),
		"restart_cmd": mapStr(body, "restart_cmd", ""), "check_cmd": mapStr(body, "check_cmd", ""),
		"content": mapStr(body, "content", ""), "backup": singboxBool(body["backup"], true),
		"remark": mapStr(body, "remark", ""), "enabled": singboxBool(body["enabled"], true),
	}, ""
}

func (h *handlers) nodeTemplateCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	input, msg := normalizeNodeTemplateInput(body)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var dup int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM node_templates WHERE name = ?`, input["name"]).Scan(&dup)
	if dup > 0 {
		writeError(w, http.StatusConflict, "模板名称已存在")
		return
	}
	id, err := insertDynamic(r.Context(), h.db, "node_templates", input, nowMillis())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建失败")
		return
	}
	h.writeNodeTemplate(w, r, id)
}

func (h *handlers) writeNodeTemplate(w http.ResponseWriter, r *http.Request, id int64) {
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT t.*,
	      (SELECT COUNT(*) FROM node_configs c WHERE c.template_id = t.id) AS usage_count
	    FROM node_templates t WHERE t.id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusInternalServerError, "读取模板失败")
		return
	}
	row := rows[0]
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"id": mapInt(row, "id", 0), "name": mapStr(row, "name", ""),
		"engine": mapStr(row, "engine", "other"), "target_path": mapStr(row, "target_path", ""),
		"service_name": mapStr(row, "service_name", ""), "restart_cmd": mapStr(row, "restart_cmd", ""),
		"check_cmd": mapStr(row, "check_cmd", ""), "content": mapStr(row, "content", ""),
		"backup": mapInt(row, "backup", 1), "remark": mapStr(row, "remark", ""),
		"enabled": mapInt(row, "enabled", 1), "usage_count": mapInt(row, "usage_count", 0),
		"created_at": mapInt(row, "created_at", 0), "updated_at": mapInt(row, "updated_at", 0),
	}})
}

func (h *handlers) nodeTemplateUpdate(w http.ResponseWriter, r *http.Request) {
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
	input, msg := normalizeNodeTemplateInput(body)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var dup int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM node_templates WHERE name = ? AND id <> ?`, input["name"], id).Scan(&dup)
	if dup > 0 {
		writeError(w, http.StatusConflict, "模板名称已存在")
		return
	}
	if err := updateDynamic(r.Context(), h.db, "node_templates", id, input, nowMillis()); err != nil {
		writeError(w, http.StatusInternalServerError, "更新失败")
		return
	}
	h.writeNodeTemplate(w, r, id)
}

func (h *handlers) nodeTemplateDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	var used int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM node_configs WHERE template_id = ?`, id).Scan(&used)
	if used > 0 {
		writeError(w, http.StatusConflict, "该模板已被配置引用，请先解绑")
		return
	}
	_, _ = h.db.ExecContext(r.Context(), `DELETE FROM node_templates WHERE id = ?`, id)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

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
