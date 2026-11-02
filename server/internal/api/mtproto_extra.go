package api

import (
	"net/http"
	"strings"
)

// GET /api/mtproto/templates/{id}
func (h *handlers) mtprotoTemplateGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT t.*,
	      (SELECT COUNT(*) FROM mtproto_nodes n WHERE n.template_id = t.id) AS node_count
	    FROM mtproto_templates t WHERE t.id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": mtprotoTemplateJSON(rows[0])})
}

// POST /api/mtproto/templates/{id}/apply —— 按模板下发所有绑定节点
func (h *handlers) mtprotoTemplateApply(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	if _, _, err := h.loadMtprotoTemplate(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	nodes, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT id, name FROM mtproto_nodes WHERE template_id = ? ORDER BY id ASC`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点失败")
		return
	}
	// 模板下多个节点时转后台任务（逐节点 apply + 排队下发）
	if len(nodes) > 1 {
		jobID, n, dup, msg := h.batchCreateJob(r.Context(), "mtproto.template_apply",
			map[string]any{"template_id": id}, "admin")
		if msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		if !dup {
			h.startBatchJob(jobID, "admin")
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "async": true, "job_id": jobID, "total": n, "duplicate": dup,
			"message": "已创建后台任务，进度见右上角「任务」",
		})
		return
	}
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		nodeID := mapInt(n, "id", 0)
		changed, queued, msg := h.applyMtprotoToNode(r.Context(), nodeID)
		if msg != "" {
			out = append(out, map[string]any{"id": nodeID, "name": mapStr(n, "name", ""), "ok": false, "error": msg})
			continue
		}
		out = append(out, map[string]any{
			"id": nodeID, "name": mapStr(n, "name", ""), "ok": true,
			"changed": changed, "queued": queued,
		})
	}
	okCount := 0
	for _, x := range out {
		if ok, _ := x["ok"].(bool); ok {
			okCount++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out, "ok_count": okCount, "total": len(out)})
}

// POST /api/mtproto/templates/{id}/preview —— 模板级预览（不落库）
func (h *handlers) mtprotoTemplatePreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	tpl, _, err := h.loadMtprotoTemplate(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	var body map[string]any
	_ = decodeJSON(w, r, &body)
	domain := tpl.Domain
	if body != nil {
		if v := strings.TrimSpace(mapStr(body, "domain", "")); v != "" {
			domain = mtprotoDomainOf(v)
		}
	}
	node := &mtprotoNode{
		Name: "preview", TemplateID: id, Domain: domain,
		BootstrapSecret: mtprotoGenerateSecret(domain),
	}
	eff := mtprotoEffective(tpl, node)
	if eff.SecretMode == "single" && eff.BootstrapSecret == "" {
		eff.BootstrapSecret = mtprotoGenerateSecret(domain)
	}
	content, ports, mode, msg := mtprotoRenderConfig(&eff, nil)
	if msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "content": content, "ports": ports, "mode": mode,
		"domain": eff.Domain,
	})
}

func (h *handlers) portalClientFor(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	email, ok := h.portalSession(r)
	if !ok || email == "" {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return nil, false
	}
	var uuid string
	_ = h.db.QueryRowContext(r.Context(), `SELECT COALESCE(uuid,'') FROM uuid_info WHERE LOWER(email) = ? LIMIT 1`, email).Scan(&uuid)
	if uuid == "" {
		writeError(w, http.StatusNotFound, "当前邮箱未绑定 UUID")
		return nil, false
	}
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "客户端不存在")
		return nil, false
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT * FROM mtproto_clients WHERE id = ? AND uuid = ? LIMIT 1`, id, uuid)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "客户端不存在")
		return nil, false
	}
	return rows[0], true
}

// POST /api/portal/mtproto/{id}/send-mail —— 校验归属后复用管理员发信逻辑
func (h *handlers) portalMtprotoSendMail(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.portalClientFor(w, r); !ok {
		return
	}
	h.mtprotoClientSendMail(w, r)
}

// POST /api/portal/mtproto/{id}/deliver —— 用户自助获取链接
func (h *handlers) portalMtprotoDeliver(w http.ResponseWriter, r *http.Request) {
	client, ok := h.portalClientFor(w, r)
	if !ok {
		return
	}
	nodeRows, err := h.mtprotoNodesWithMeta(r.Context(), "WHERE n.id = ?", mapInt(client, "node_id", 0))
	if err != nil || len(nodeRows) == 0 {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	tpl, _, err := h.loadMtprotoTemplate(r.Context(), mapInt(nodeRows[0], "template_id", 0))
	if err != nil {
		writeError(w, http.StatusNotFound, "协议模板不存在")
		return
	}
	eff := mtprotoEffective(tpl, mtprotoNodeFromRow(nodeRows[0]))
	srv := mtprotoServer{
		LastIP4: mapStr(nodeRows[0], "server_ip4", ""),
		LastIP:  mapStr(nodeRows[0], "server_host", ""),
		Host:    mapStr(nodeRows[0], "server_host", ""),
	}
	link := mtprotoClientLink(&eff, srv, &mtprotoClient{ID: mapInt(client, "id", 0), Secret: mapStr(client, "secret", "")})
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": link})
}
