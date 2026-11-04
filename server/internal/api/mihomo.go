package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// ---------------- mihomo 模板管理 / 节点绑定 ----------------

type mihomoTemplateInput struct {
	Name    string `json:"name"`
	Remark  string `json:"remark"`
	Content string `json:"content"`
}

type mihomoNodeInput struct {
	Name       string   `json:"name"`
	ServerID   int64    `json:"server_id"`
	TemplateID int64    `json:"template_id"`
	UUIDList   []string `json:"uuid_list"`
	Enabled    *bool    `json:"enabled"`
	Remark     string   `json:"remark"`
}

func mihomoParseUUIDs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	seen := map[string]bool{}
	for _, u := range list {
		u = strings.ToLower(strings.TrimSpace(u))
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

// GET /api/mihomo/templates
func (h *handlers) mihomoTemplatesList(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT id, name, COALESCE(remark,'') AS remark, CHAR_LENGTH(COALESCE(content,'')) AS content_len,
		        created_at, updated_at
		   FROM mihomo_templates ORDER BY id DESC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询 mihomo 模板失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": rows})
}

// GET /api/mihomo/templates/{id}
func (h *handlers) mihomoTemplateGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	var name, remark, content string
	var createdAt, updatedAt int64
	err := h.db.QueryRowContext(r.Context(),
		`SELECT name, COALESCE(remark,''), COALESCE(content,''), created_at, updated_at FROM mihomo_templates WHERE id = ? LIMIT 1`,
		id).Scan(&name, &remark, &content, &createdAt, &updatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"id": id, "name": name, "remark": remark, "content": content,
		"created_at": createdAt, "updated_at": updatedAt,
	}})
}

// POST /api/mihomo/templates
func (h *handlers) mihomoTemplateCreate(w http.ResponseWriter, r *http.Request) {
	var in mihomoTemplateInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "请填写模板名称")
		return
	}
	if strings.TrimSpace(in.Content) == "" {
		writeError(w, http.StatusBadRequest, "请填写 mihomo YAML 模板内容")
		return
	}
	now := nowMillis()
	res, err := h.db.ExecContext(r.Context(),
		`INSERT INTO mihomo_templates (name, remark, content, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		name, in.Remark, in.Content, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "1062") {
			writeError(w, http.StatusBadRequest, "模板名称已存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "创建模板失败")
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": id})
}

// PUT /api/mihomo/templates/{id}
func (h *handlers) mihomoTemplateUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	var in mihomoTemplateInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "请填写模板名称")
		return
	}
	if strings.TrimSpace(in.Content) == "" {
		writeError(w, http.StatusBadRequest, "请填写 mihomo YAML 模板内容")
		return
	}
	_, err := h.db.ExecContext(r.Context(),
		`UPDATE mihomo_templates SET name = ?, remark = ?, content = ?, updated_at = ? WHERE id = ?`,
		name, in.Remark, in.Content, nowMillis(), id)
	if err != nil {
		if strings.Contains(err.Error(), "1062") {
			writeError(w, http.StatusBadRequest, "模板名称已存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "保存模板失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// DELETE /api/mihomo/templates/{id}
func (h *handlers) mihomoTemplateDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	var used int
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM mihomo_nodes WHERE template_id = ?`, id).Scan(&used)
	if used > 0 {
		writeError(w, http.StatusBadRequest, "该模板仍被节点绑定使用，请先解除绑定")
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `DELETE FROM mihomo_templates WHERE id = ?`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "删除模板失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// GET /api/mihomo/nodes
func (h *handlers) mihomoNodesList(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT n.id, n.name, n.server_id, n.template_id, COALESCE(n.uuid_list,'[]') AS uuid_list,
		        n.enabled, COALESCE(n.remark,'') AS remark, n.created_at, n.updated_at,
		        COALESCE(s.name,'') AS server_name, COALESCE(s.host,'') AS server_host,
		        COALESCE(s.ip_country,'') AS server_country,
		        COALESCE(t.name,'') AS template_name
		   FROM mihomo_nodes n
		   LEFT JOIN servers s ON s.id = n.server_id
		   LEFT JOIN mihomo_templates t ON t.id = n.template_id
		  ORDER BY n.id DESC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询 mihomo 节点绑定失败")
		return
	}
	for _, row := range rows {
		row["uuid_list"] = mihomoParseUUIDs(mapStr(row, "uuid_list", "[]"))
		row["uuid_count"] = len(mihomoParseUUIDs(mapStr(row, "uuid_list", "[]")))
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": rows})
}

// POST /api/mihomo/nodes
func (h *handlers) mihomoNodeCreate(w http.ResponseWriter, r *http.Request) {
	var in mihomoNodeInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if msg := h.validateMihomoNode(r.Context(), &in, 0); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	enabled := 1
	if in.Enabled != nil && !*in.Enabled {
		enabled = 0
	}
	raw, _ := json.Marshal(in.UUIDList)
	now := nowMillis()
	res, err := h.db.ExecContext(r.Context(),
		`INSERT INTO mihomo_nodes (name, server_id, template_id, uuid_list, enabled, remark, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(in.Name), in.ServerID, in.TemplateID, string(raw), enabled, in.Remark, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "1062") {
			writeError(w, http.StatusBadRequest, "名称或该服务器的绑定已存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "创建绑定失败")
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": id})
}

// PUT /api/mihomo/nodes/{id}
func (h *handlers) mihomoNodeUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "绑定不存在")
		return
	}
	var in mihomoNodeInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if msg := h.validateMihomoNode(r.Context(), &in, id); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	enabled := 1
	if in.Enabled != nil && !*in.Enabled {
		enabled = 0
	}
	raw, _ := json.Marshal(in.UUIDList)
	_, err := h.db.ExecContext(r.Context(),
		`UPDATE mihomo_nodes SET name = ?, server_id = ?, template_id = ?, uuid_list = ?, enabled = ?, remark = ?, updated_at = ?
		  WHERE id = ?`,
		strings.TrimSpace(in.Name), in.ServerID, in.TemplateID, string(raw), enabled, in.Remark, nowMillis(), id)
	if err != nil {
		if strings.Contains(err.Error(), "1062") {
			writeError(w, http.StatusBadRequest, "名称或该服务器的绑定已存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "保存绑定失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// DELETE /api/mihomo/nodes/{id}
func (h *handlers) mihomoNodeDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "绑定不存在")
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `DELETE FROM mihomo_nodes WHERE id = ?`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "删除绑定失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (h *handlers) validateMihomoNode(ctx context.Context, in *mihomoNodeInput, selfID int64) string {
	if strings.TrimSpace(in.Name) == "" {
		return "请填写绑定名称"
	}
	if in.ServerID <= 0 {
		return "请选择服务器"
	}
	if in.TemplateID <= 0 {
		return "请选择 mihomo 模板"
	}
	var n int
	if err := h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM servers WHERE id = ?`, in.ServerID).Scan(&n); err != nil || n == 0 {
		return "服务器不存在"
	}
	if err := h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mihomo_templates WHERE id = ?`, in.TemplateID).Scan(&n); err != nil || n == 0 {
		return "模板不存在"
	}
	return ""
}

// GET /api/mihomo/nodes/{id}/preview
func (h *handlers) mihomoNodePreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "绑定不存在")
		return
	}
	var (
		name       string
		serverID   int64
		templateID int64
		uuidRaw    string
		tplName    string
		tplContent string
		serverName string
	)
	err := h.db.QueryRowContext(r.Context(),
		`SELECT n.name, n.server_id, n.template_id, COALESCE(n.uuid_list,'[]'),
		        COALESCE(t.name,''), COALESCE(t.content,''), COALESCE(s.name,'')
		   FROM mihomo_nodes n
		   LEFT JOIN mihomo_templates t ON t.id = n.template_id
		   LEFT JOIN servers s ON s.id = n.server_id
		  WHERE n.id = ? LIMIT 1`, id).
		Scan(&name, &serverID, &templateID, &uuidRaw, &tplName, &tplContent, &serverName)
	if err != nil {
		writeError(w, http.StatusNotFound, "绑定不存在")
		return
	}
	if strings.TrimSpace(tplContent) == "" {
		writeError(w, http.StatusBadRequest, "模板内容为空，请先完善模板")
		return
	}
	var sbNodeID int64
	err = h.db.QueryRowContext(r.Context(),
		`SELECT id FROM singbox_nodes WHERE server_id = ? ORDER BY enabled DESC, id ASC LIMIT 1`, serverID).Scan(&sbNodeID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "该服务器还没有 sing-box 节点，无法生成 mihomo 节点")
		return
	}
	tpl, node, _, _, msg := h.loadSingboxNodeFull(r.Context(), sbNodeID)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	users := singboxParseUsers(currentTemplateUsers(tpl))
	want := mihomoParseUUIDs(uuidRaw)
	if len(want) > 0 {
		set := map[string]bool{}
		for _, u := range want {
			set[strings.ToLower(u)] = true
		}
		filtered := users[:0]
		for _, u := range users {
			if set[strings.ToLower(strings.TrimSpace(u.UUID))] {
				filtered = append(filtered, u)
			}
		}
		users = filtered
	}
	if len(users) == 0 {
		writeError(w, http.StatusBadRequest, "绑定中没有可用的 UUID，请检查选择")
		return
	}
	rawLinks := singboxClientLinks(tpl, singboxNode{Name: node.Name, Domain: node.Domain, CDNDomain: node.CDNDomain}, users)
	links := make([]string, 0, len(rawLinks))
	for _, item := range rawLinks {
		if s := mapStr(item, "link", ""); s != "" {
			links = append(links, s)
		}
	}
	if len(links) == 0 {
		writeError(w, http.StatusBadRequest, "未能生成节点链接")
		return
	}
	proxies, skippedBy, protocols, reasons := h.buildClashProxies(r.Context(), links)
	if len(proxies) <= 1 {
		writeError(w, http.StatusBadRequest, "未能生成有效的 mihomo 节点")
		return
	}
	finalConfig := injectClashProxies(tplContent, proxies)
	skipped := 0
	for _, n := range skippedBy {
		skipped += n
	}
	details := make([]map[string]any, 0, len(skippedBy))
	for proto, count := range skippedBy {
		details = append(details, map[string]any{"protocol": proto, "count": count, "reason": reasons[proto]})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"name": name, "server": serverName, "template": tplName,
			"yaml": finalConfig, "uuid_count": len(users),
			"proxy_count": len(proxies) - 1, "skipped": skipped,
			"protocols": protocols, "skipped_details": details,
			"filename": strings.ReplaceAll(name, " ", "-") + ".yaml",
		},
	})
}
