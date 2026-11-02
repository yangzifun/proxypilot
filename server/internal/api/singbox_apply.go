package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
)

func (h *handlers) loadSingboxNodeFull(ctx context.Context, id int64) (*singboxTemplate, singboxRenderNode, serverRow, map[string]any, string) {
	rows, err := queryRowsAsMaps(ctx, h.db, `SELECT n.*, s.name AS server_name, s.last_ip AS server_host,
	      t.name AS template_name, t.warp_enabled AS template_warp
	    FROM singbox_nodes n LEFT JOIN servers s ON s.id = n.server_id
	    LEFT JOIN singbox_templates t ON t.id = n.template_id WHERE n.id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		return nil, singboxRenderNode{}, serverRow{}, nil, "节点不存在"
	}
	nodeRow := rows[0]
	tplRows, err := queryRowsAsMaps(ctx, h.db, `SELECT * FROM singbox_templates WHERE id = ? LIMIT 1`,
		mapInt(nodeRow, "template_id", 0))
	if err != nil || len(tplRows) == 0 {
		return nil, singboxRenderNode{}, serverRow{}, nil, "协议模板不存在"
	}
	tpl := inputToTemplate(tplRows[0])
	server, err := h.fetchServerRow(ctx, mapInt(nodeRow, "server_id", 0))
	if err != nil {
		return nil, singboxRenderNode{}, serverRow{}, nil, "目标服务器不存在"
	}
	if tpl.UsersJSON == "" {
		tpl.UsersJSON = mapStr(tplRows[0], "users_json", "[]")
	}
	node := singboxRenderNode{
		Name:             mapStr(nodeRow, "name", ""),
		Domain:           mapStr(nodeRow, "domain", ""),
		CDNDomain:        mapStr(nodeRow, "cdn_domain", ""),
		SBPath:           mapStr(nodeRow, "sb_path", ""),
		CaddyPath:        mapStr(nodeRow, "caddy_path", ""),
		ServiceSBName:    mapStr(nodeRow, "service_sb", ""),
		ServiceCaddyName: mapStr(nodeRow, "service_caddy", ""),
	}
	// 统计：默认开启；secret 不存在时在首次渲染前生成并加密落库
	if nodeStatsEnabled(nodeRow) {
		node.StatsSecret = h.ensureNodeStatsSecret(ctx, mapInt(nodeRow, "id", 0))
	}
	return tpl, node, server, nodeRow, ""
}

// GET /api/singbox/nodes/{id}/preview
func (h *handlers) singboxNodePreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	tpl, node, server, _, msg := h.loadSingboxNodeFull(r.Context(), id)
	if msg != "" {
		writeError(w, http.StatusNotFound, msg)
		return
	}
	files, ports, users, msg := singboxRenderBundle(tpl, node, server)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	view := make([]map[string]any, 0, len(files))
	for _, f := range files {
		view = append(view, singboxFileView(f))
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "ports": ports, "users": users, "files": view})
}

// GET /api/singbox/nodes/{id}/links
func (h *handlers) singboxNodeLinks(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	tpl, node, _, _, msg := h.loadSingboxNodeFull(r.Context(), id)
	if msg != "" {
		writeError(w, http.StatusNotFound, msg)
		return
	}
	users := singboxParseUsers(currentTemplateUsers(tpl))
	links := singboxClientLinks(tpl, singboxNode{Name: node.Name, Domain: node.Domain, CDNDomain: node.CDNDomain}, users)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": links, "total": len(links)})
}

// GET /api/singbox/nodes/{id}
func (h *handlers) singboxNodeGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	tpl, node, server, nodeRow, msg := h.loadSingboxNodeFull(r.Context(), id)
	if msg != "" {
		writeError(w, http.StatusNotFound, msg)
		return
	}
	files, ports, users, renderMsg := singboxRenderBundle(tpl, node, server)
	preview := map[string]any{"ports": ports, "users": users, "error": renderMsg}
	if renderMsg == "" {
		view := make([]map[string]any, 0, len(files))
		for _, f := range files {
			view = append(view, singboxFileView(f))
		}
		preview["files"] = view
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "data": singboxNodeToJSON(nodeRow), "preview": preview,
	})
}

// POST /api/singbox/nodes/{id}/apply
func (h *handlers) singboxNodeApply(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	tpl, node, server, nodeRow, msg := h.loadSingboxNodeFull(r.Context(), id)
	if msg != "" {
		writeError(w, http.StatusNotFound, msg)
		return
	}
	files, ports, users, msg := singboxRenderBundle(tpl, node, server)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	now := nowMillis()
	type applied struct {
		NodeConfigID int64  `json:"node_config_id"`
		Name         string `json:"name"`
		Changed      bool   `json:"changed"`
		Version      int64  `json:"version"`
		Path         string `json:"path"`
		Key          string `json:"key"`
		Engine       string `json:"engine"`
		ServiceName  string `json:"service_name"`
		CheckCmd     string `json:"check_cmd"`
		Content      string `json:"content"`
	}
	results := make([]applied, 0, len(files))
	hashParts := make([]string, 0, len(files))

	for _, f := range files {
		key := mapStr(f, "key", "")
		cfgName := "sbx:" + node.Name + ":" + key
		content := mapStr(f, "content", "")
		path := mapStr(f, "path", "")
		service := mapStr(f, "service_name", "")
		checkCmd := mapStr(f, "check_cmd", "")
		engine := mapStr(f, "engine", "")
		hash := sha256Hex(content)
		restartCmd := singboxRestartCommand(service)
		hashParts = append(hashParts, key+"\x00"+content)

		var (
			existID      int64
			existHash    string
			existPath    string
			existRestart string
			existVersion int64
		)
		err := h.db.QueryRowContext(r.Context(), `SELECT id, COALESCE(content_hash,''), COALESCE(target_path,''),
		      COALESCE(restart_cmd,''), COALESCE(version,0) FROM node_configs WHERE name = ? LIMIT 1`, cfgName).
			Scan(&existID, &existHash, &existPath, &existRestart, &existVersion)
		if err == sql.ErrNoRows {
			res, err := h.db.ExecContext(r.Context(), `INSERT INTO node_configs
			  (name, server_id, engine, target_path, service_name, restart_cmd, check_cmd, content, backup,
			   enabled, auto_push, push_interval_min, template_id, uuid, install_versions, version, content_hash, created_at, updated_at)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, 1, 0, 0, 0, '', ?, 1, ?, ?, ?)`,
				cfgName, mapInt(nodeRow, "server_id", 0), engine, path, service, restartCmd, checkCmd, content, hash, singboxInstallVersions(tpl), now, now)
			if err != nil {
				h.log.Error("写入节点配置失败", "err", err)
				writeError(w, http.StatusInternalServerError, "下发失败")
				return
			}
			newID, _ := res.LastInsertId()
			results = append(results, applied{newID, cfgName, true, 1, path, key, engine, service, checkCmd, content})
			continue
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "查询节点配置失败")
			return
		}
		same := existHash == hash && existPath == path && existRestart == restartCmd
		if same {
			results = append(results, applied{existID, cfgName, false, existVersion, path, key, engine, service, checkCmd, content})
			continue
		}
		if _, err := h.db.ExecContext(r.Context(), `UPDATE node_configs SET server_id = ?, engine = ?, target_path = ?,
		      service_name = ?, restart_cmd = ?, check_cmd = ?, content = ?, content_hash = ?, install_versions = ?,
		      version = COALESCE(version,0) + 1, enabled = 1, updated_at = ? WHERE id = ?`,
			mapInt(nodeRow, "server_id", 0), engine, path, service, restartCmd, checkCmd, content, hash, singboxInstallVersions(tpl),
			now, existID); err != nil {
			writeError(w, http.StatusInternalServerError, "下发失败")
			return
		}
		results = append(results, applied{existID, cfgName, true, existVersion + 1, path, key, engine, service, checkCmd, content})
	}

	renderHash := sha256Hex(strings.Join(hashParts, "\x00"))
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE singbox_nodes SET last_apply_at = ?, last_render_hash = ?, updated_at = ? WHERE id = ?`,
		now, renderHash, now, id); err != nil {
		h.log.Warn("更新节点渲染指纹失败", "err", err)
	}

	changed := 0
	out := make([]map[string]any, 0, len(results))
	for _, f := range results {
		if f.Changed {
			changed++
		}
		out = append(out, map[string]any{
			"key": f.Key, "name": f.Name, "engine": f.Engine, "path": f.Path,
			"service_name": f.ServiceName, "check_cmd": f.CheckCmd,
			"content": f.Content, "node_config_id": f.NodeConfigID,
			"version": f.Version, "changed": f.Changed,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "files": out, "changed": changed,
		"ports": ports, "users": users, "render_hash": renderHash,
		"message": "已写入节点配置；Agent 下次 poll 会自动下发（内容未变则不升版本）",
	})
}

// singboxRestartCommand 生成重启命令。V15.6 起只承载 sing-box 单一引擎。
func singboxRestartCommand(service string) string {
	svc := strings.TrimSpace(service)
	if svc == "" {
		svc = "sing-box"
	}
	return "systemctl enable " + svc + " 2>/dev/null; systemctl restart " + svc
}
