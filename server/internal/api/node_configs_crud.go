package api

import (
	"database/sql"
	"net/http"
	"strings"
)

// GET /api/node-templates/{id}
func (h *handlers) nodeTemplateGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT t.*,
	      (SELECT COUNT(*) FROM node_configs c WHERE c.template_id = t.id) AS usage_count
	    FROM node_templates t WHERE t.id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "模板不存在")
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

func normalizeNodeConfigInput(body map[string]any) (map[string]any, string) {
	name := strings.TrimSpace(mapStr(body, "name", ""))
	if name == "" {
		return nil, "配置名称不能为空"
	}
	serverID := jsonInt(body["server_id"])
	if serverID <= 0 {
		return nil, "请选择目标服务器"
	}
	templateID := jsonInt(body["template_id"])
	content := mapStr(body, "content", "")
	if strings.TrimSpace(content) == "" && templateID == 0 {
		return nil, "请填写配置内容或选择一个节点配置模板"
	}
	target := strings.TrimSpace(mapStr(body, "target_path", ""))
	if target == "" && templateID == 0 {
		return nil, "目标路径不能为空（选了模板也可留空以继承模板）"
	}
	return map[string]any{
		"name": name, "server_id": serverID, "template_id": templateID,
		"engine":            firstNonEmpty(strings.TrimSpace(mapStr(body, "engine", "")), "other"),
		"target_path":       target,
		"service_name":      mapStr(body, "service_name", ""),
		"restart_cmd":       mapStr(body, "restart_cmd", ""),
		"check_cmd":         mapStr(body, "check_cmd", ""),
		"content":           content,
		"backup":            singboxBool(body["backup"], true),
		"enabled":           singboxBool(body["enabled"], true),
		"auto_push":         singboxBool(body["auto_push"], false),
		"push_interval_min": intOrJS(body["push_interval_min"], 0, 0, 100000),
		"uuid":              strings.TrimSpace(mapStr(body, "uuid", "")),
		"install_versions":  firstNonEmpty(strings.TrimSpace(mapStr(body, "install_versions", "")), "{}"),
	}, ""
}

// POST /api/node-configs
func (h *handlers) nodeConfigCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	input, msg := normalizeNodeConfigInput(body)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var dup int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM node_configs WHERE name = ?`, input["name"]).Scan(&dup)
	if dup > 0 {
		writeError(w, http.StatusConflict, "配置名称已存在")
		return
	}
	var serverExists int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM servers WHERE id = ?`, input["server_id"]).Scan(&serverExists)
	if serverExists == 0 {
		writeError(w, http.StatusNotFound, "目标服务器不存在")
		return
	}
	if tid, _ := input["template_id"].(int64); tid > 0 {
		var tplExists int64
		_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM node_templates WHERE id = ?`, tid).Scan(&tplExists)
		if tplExists == 0 {
			writeError(w, http.StatusNotFound, "节点配置模板不存在")
			return
		}
	}
	input["version"] = int64(1)
	content := mapStr(input, "content", "")
	// content_hash 固定为「原文哈希」口径，见 nodeConfigContentHash 的说明。
	// 模板继承 / 占位符配置的「下发内容」哈希由 agentBuildDeployPayload 渲染后另算。
	input["content_hash"] = nodeConfigContentHash(content)
	id, err := insertDynamic(r.Context(), h.db, "node_configs", input, nowMillis())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建失败")
		return
	}
	h.writeNodeConfig(w, r, id)
}

func (h *handlers) writeNodeConfig(w http.ResponseWriter, r *http.Request, id int64) {
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT c.*, s.name AS server_name, t.name AS template_name
	    FROM node_configs c LEFT JOIN servers s ON s.id = c.server_id
	    LEFT JOIN node_templates t ON t.id = c.template_id WHERE c.id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusInternalServerError, "读取配置失败")
		return
	}
	row := rows[0]
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"id": mapInt(row, "id", 0), "name": mapStr(row, "name", ""),
		"server_id": mapInt(row, "server_id", 0), "server_name": mapStr(row, "server_name", ""),
		"template_id": mapInt(row, "template_id", 0), "template_name": mapStr(row, "template_name", ""),
		"engine": mapStr(row, "engine", "other"), "target_path": mapStr(row, "target_path", ""),
		"service_name": mapStr(row, "service_name", ""), "restart_cmd": mapStr(row, "restart_cmd", ""),
		"check_cmd": mapStr(row, "check_cmd", ""), "content": mapStr(row, "content", ""),
		"backup": mapInt(row, "backup", 1), "enabled": mapInt(row, "enabled", 1),
		"auto_push": mapInt(row, "auto_push", 0), "push_interval_min": mapInt(row, "push_interval_min", 0),
		"version": mapInt(row, "version", 0), "applied_version": mapInt(row, "applied_version", 0),
		"install_versions": mapStr(row, "install_versions", ""),
		"uuid":             mapStr(row, "uuid", ""),
		"last_push_at":     mapInt(row, "last_push_at", 0), "last_push_status": mapStr(row, "last_push_status", ""),
		"last_push_message": mapStr(row, "last_push_message", ""),
		"created_at":        mapInt(row, "created_at", 0), "updated_at": mapInt(row, "updated_at", 0),
	}})
}

// GET/PUT/DELETE /api/node-configs/{id}
func (h *handlers) nodeConfigItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "配置不存在")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.writeNodeConfig(w, r, id)
	case http.MethodPut:
		var body map[string]any
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "无效 JSON")
			return
		}
		input, msg := normalizeNodeConfigInput(body)
		if msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		var dup int64
		_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM node_configs WHERE name = ? AND id <> ?`, input["name"], id).Scan(&dup)
		if dup > 0 {
			writeError(w, http.StatusConflict, "配置名称已存在")
			return
		}
		content := mapStr(input, "content", "")
		// 同创建：content_hash 只表示 content 原文（口径见 nodeConfigContentHash）。
		input["content_hash"] = nodeConfigContentHash(content)
		if err := updateDynamic(r.Context(), h.db, "node_configs", id, input, nowMillis()); err != nil {
			writeError(w, http.StatusInternalServerError, "更新失败")
			return
		}
		// 内容/路径变化时版本 +1（与 Worker 语义一致）
		_, _ = h.db.ExecContext(r.Context(),
			`UPDATE node_configs SET version = COALESCE(version,0) + 1 WHERE id = ?`, id)
		h.writeNodeConfig(w, r, id)
	case http.MethodDelete:
		var exists int64
		if err := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM node_configs WHERE id = ?`, id).Scan(&exists); err != nil || exists == 0 {
			writeError(w, http.StatusNotFound, "配置不存在")
			return
		}
		_, _ = h.db.ExecContext(r.Context(), `DELETE FROM node_configs WHERE id = ?`, id)
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

// POST /api/node-configs/{id}/push
func (h *handlers) nodeConfigPushOne(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "配置不存在")
		return
	}
	cfg, err := h.loadNodeConfig(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "配置不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "读取配置失败")
		return
	}
	srv, err := h.fetchAgentServer(r.Context(), cfg.ServerID)
	if err != nil {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}
	payload := h.agentBuildDeployPayload(r.Context(), cfg, srv)
	taskID, dup, msg := h.agentQueueTask(r.Context(), srv, "deploy", cfg.ID, payload, "admin")
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "task_id": taskID, "duplicate": dup})
}
