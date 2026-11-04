package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// renderMihomoBinding 生成单个绑定的完整 mihomo YAML（与预览逻辑一致）。
func (h *handlers) renderMihomoBinding(ctx context.Context, id int64) (name, serverName, tplName, yamlText, msg string) {
	var (
		serverID   int64
		templateID int64
		uuidRaw    string
		protoRaw   string
		tplContent string
		tplEnabled int
	)
	err := h.db.QueryRowContext(ctx,
		`SELECT n.name, n.server_id, n.template_id, COALESCE(n.uuid_list,'[]'), COALESCE(n.protocols,'[]'),
		        COALESCE(t.name,''), COALESCE(t.content,''), COALESCE(s.name,''), COALESCE(t.enabled,1)
		   FROM mihomo_nodes n
		   LEFT JOIN mihomo_templates t ON t.id = n.template_id
		   LEFT JOIN servers s ON s.id = n.server_id
		  WHERE n.id = ? LIMIT 1`, id).
		Scan(&name, &serverID, &templateID, &uuidRaw, &protoRaw, &tplName, &tplContent, &serverName, &tplEnabled)
	if err != nil {
		return "", "", "", "", "绑定不存在"
	}
	if strings.TrimSpace(tplContent) == "" {
		return name, serverName, tplName, "", "模板内容为空"
	}
	if tplEnabled == 0 {
		return name, serverName, tplName, "", "模板已停用"
	}
	var sbNodeID int64
	if h.db.QueryRowContext(ctx,
		`SELECT id FROM singbox_nodes WHERE server_id = ? ORDER BY enabled DESC, id ASC LIMIT 1`, serverID).Scan(&sbNodeID) != nil || sbNodeID == 0 {
		return name, serverName, tplName, "", "该服务器还没有 sing-box 节点"
	}
	tpl, node, _, _, m := h.loadSingboxNodeFull(ctx, sbNodeID)
	if m != "" {
		return name, serverName, tplName, "", m
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
		return name, serverName, tplName, "", "没有可用的 UUID"
	}
	rawLinks := singboxClientLinks(tpl, singboxNode{Name: node.Name, Domain: node.Domain, CDNDomain: node.CDNDomain}, users)
	protoSet := map[string]bool{}
	for _, p := range mihomoParseProtocols(protoRaw) {
		protoSet[p] = true
	}
	links := make([]string, 0, len(rawLinks))
	for _, item := range rawLinks {
		if !mihomoLinkAllowed(item, protoSet) {
			continue
		}
		if s := mapStr(item, "link", ""); s != "" {
			links = append(links, s)
		}
	}
	if len(links) == 0 {
		return name, serverName, tplName, "", "未能生成节点链接"
	}
	proxies, _, _, _ := h.buildClashProxies(ctx, links)
	if len(proxies) <= 1 {
		return name, serverName, tplName, "", "未能生成有效节点"
	}
	final := injectClashProxies(tplContent, proxies)
	if strings.Contains(final, "{{PROXY_NAMES}}") {
		names := make([]string, 0, len(proxies))
		for _, p := range proxies {
			if fmt.Sprint(p["type"]) == "direct" {
				continue
			}
			names = append(names, "      - "+strconv.Quote(fmt.Sprint(p["name"])))
		}
		if len(names) == 0 {
			names = append(names, "      - DIRECT")
		}
		final = strings.ReplaceAll(final, "{{PROXY_NAMES}}", strings.Join(names, "\n"))
	}
	_, _ = h.db.ExecContext(ctx, `UPDATE mihomo_nodes SET last_preview_at = ? WHERE id = ?`, nowMillis(), id)
	return name, serverName, tplName, final, ""
}

// POST /api/mihomo/nodes/bulk  {ids:[], enabled:bool}
func (h *handlers) mihomoNodesBulk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs     []int64 `json:"ids"`
		Enabled *bool   `json:"enabled"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if len(body.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "请选择绑定")
		return
	}
	enabled := 1
	if body.Enabled != nil && !*body.Enabled {
		enabled = 0
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(body.IDs)), ",")
	args := make([]any, 0, len(body.IDs)+2)
	args = append(args, enabled, nowMillis())
	for _, id := range body.IDs {
		args = append(args, id)
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE mihomo_nodes SET enabled = ?, updated_at = ? WHERE id IN (`+ph+`)`, args...); err != nil {
		writeError(w, http.StatusInternalServerError, "批量更新失败")
		return
	}
	h.mihomoAudit(r, "mihomo-bind-bulk", 0, fmt.Sprintf("批量启停 %d 个绑定 enabled=%d", len(body.IDs), enabled))
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "count": len(body.IDs)})
}

// GET /api/mihomo/export?ids=1,2,3
func (h *handlers) mihomoExport(w http.ResponseWriter, r *http.Request) {
	idsRaw := strings.TrimSpace(r.URL.Query().Get("ids"))
	if idsRaw == "" {
		writeError(w, http.StatusBadRequest, "缺少 ids 参数")
		return
	}
	parts := strings.Split(idsRaw, ",")
	var docs []string
	var failed []string
	for _, s := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		name, server, tpl, yamlText, msg := h.renderMihomoBinding(r.Context(), id)
		if msg != "" {
			failed = append(failed, fmt.Sprintf("%s: %s", name, msg))
			continue
		}
		docs = append(docs, fmt.Sprintf("# ===== %s · %s · %s =====\n%s", name, server, tpl, yamlText))
	}
	if len(docs) == 0 {
		writeError(w, http.StatusBadRequest, "没有可导出的绑定："+strings.Join(failed, "；"))
		return
	}
	w.Header().Set("Content-Type", "text/yaml;charset=UTF-8")
	w.Header().Set("Content-Disposition", `attachment; filename="mihomo-bindings.yaml"`)
	w.Header().Set("X-Exported", strconv.Itoa(len(docs)))
	_, _ = w.Write([]byte(strings.Join(docs, "\n---\n")))
}

// GET /api/mihomo/check
func (h *handlers) mihomoCheck(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT n.id, n.name, n.server_id, n.template_id, COALESCE(n.uuid_list,'[]') AS uuid_list,
		        COALESCE(s.name,'') AS server_name, COALESCE(t.name,'') AS template_name,
		        COALESCE(t.enabled,1) AS tpl_enabled, CHAR_LENGTH(COALESCE(t.content,'')) AS tpl_len
		   FROM mihomo_nodes n
		   LEFT JOIN servers s ON s.id = n.server_id
		   LEFT JOIN mihomo_templates t ON t.id = n.template_id
		  ORDER BY n.id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	type issue struct {
		BindingID int64  `json:"binding_id"`
		Name      string `json:"name"`
		Level     string `json:"level"`
		Message   string `json:"message"`
	}
	issues := []issue{}
	for _, row := range rows {
		id := mapInt(row, "id", 0)
		name := mapStr(row, "name", "")
		if mapStr(row, "server_name", "") == "" {
			issues = append(issues, issue{int64(id), name, "error", "绑定的服务器不存在"})
		}
		if mapStr(row, "template_name", "") == "" {
			issues = append(issues, issue{int64(id), name, "error", "绑定的模板不存在"})
			continue
		}
		if mapInt(row, "tpl_enabled", 1) == 0 {
			issues = append(issues, issue{int64(id), name, "warning", "模板已停用，订阅不会包含该绑定"})
		}
		if mapInt(row, "tpl_len", 0) == 0 {
			issues = append(issues, issue{int64(id), name, "error", "模板内容为空"})
		}
		var sbNodeID int64
		if h.db.QueryRowContext(r.Context(),
			`SELECT id FROM singbox_nodes WHERE server_id = ? ORDER BY enabled DESC, id ASC LIMIT 1`,
			mapInt(row, "server_id", 0)).Scan(&sbNodeID) != nil || sbNodeID == 0 {
			issues = append(issues, issue{int64(id), name, "error", "该服务器还没有 sing-box 节点"})
			continue
		}
		var nodeUUIDRaw string
		_ = h.db.QueryRowContext(r.Context(),
			`SELECT COALESCE(uuid_list,'[]') FROM singbox_nodes WHERE id = ?`, sbNodeID).Scan(&nodeUUIDRaw)
		allowed := map[string]bool{}
		for _, u := range mihomoParseUUIDs(nodeUUIDRaw) {
			allowed[strings.ToLower(u)] = true
		}
		list := mihomoParseUUIDs(mapStr(row, "uuid_list", "[]"))
		for _, u := range list {
			lu := strings.ToLower(u)
			if len(allowed) > 0 && !allowed[lu] {
				issues = append(issues, issue{int64(id), name, "error", "UUID " + shortUUID(u) + " 未授权该服务器"})
			}
			var cnt int
			_ = h.db.QueryRowContext(r.Context(),
				`SELECT COUNT(*) FROM (SELECT uuid FROM uuid_info UNION SELECT uuid FROM configs) t WHERE uuid = ?`, lu).Scan(&cnt)
			if cnt == 0 {
				var tplCnt int
				_ = h.db.QueryRowContext(r.Context(),
					`SELECT COUNT(*) FROM singbox_templates WHERE users_json LIKE CONCAT('%', ?, '%')`, lu).Scan(&tplCnt)
				if tplCnt == 0 {
					issues = append(issues, issue{int64(id), name, "warning", "UUID " + shortUUID(u) + " 不在用户库中"})
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    map[string]any{"checked": len(rows), "issues": issues, "issue_count": len(issues)},
	})
}

// GET /api/mihomo/stats
func (h *handlers) mihomoStats(w http.ResponseWriter, r *http.Request) {
	var templates, templatesOn, bindings, bindingsOn, servers int
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*), COALESCE(SUM(CASE WHEN COALESCE(enabled,1)=1 THEN 1 ELSE 0 END),0) FROM mihomo_templates`).Scan(&templates, &templatesOn)
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*), COALESCE(SUM(CASE WHEN enabled=1 THEN 1 ELSE 0 END),0) FROM mihomo_nodes`).Scan(&bindings, &bindingsOn)
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(DISTINCT server_id) FROM mihomo_nodes`).Scan(&servers)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"templates": templates, "templates_enabled": templatesOn,
			"bindings": bindings, "bindings_enabled": bindingsOn,
			"servers": servers,
		},
	})
}
