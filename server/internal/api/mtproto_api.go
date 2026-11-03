package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
)

const (
	mtprotoReloadCmd = "systemctl enable mtg >/dev/null 2>&1; systemctl reload mtg 2>/dev/null || systemctl restart mtg"
	mtprotoCheckCmd  = "mtg-multi version"
)

func mtprotoTemplateFromRow(row map[string]any) *mtprotoTemplate {
	return &mtprotoTemplate{
		ID: mapInt(row, "id", 0), Name: mapStr(row, "name", ""),
		Domain: mapStr(row, "domain", ""), Port: mapInt(row, "port", mtprotoDefaultPort),
		APIPort: mapInt(row, "api_port", 0), SecretMode: mapStr(row, "secret_mode", "multi"),
		Concurrency: mapInt(row, "concurrency", 8192), PreferIP: mapStr(row, "prefer_ip", "prefer-ipv4"),
		PublicIPv4: mapStr(row, "public_ipv4", ""), Blocklist: mapInt(row, "blocklist", 0),
		ThrottleMax: mapInt(row, "throttle_max", 0), ConfigPath: mapStr(row, "config_path", "/etc/mtg/config.toml"),
		ServiceName: mapStr(row, "service_name", "mtg"), Enabled: mapInt(row, "enabled", 1),
	}
}

func mtprotoNodeFromRow(row map[string]any) *mtprotoNode {
	block := mapInt(row, "blocklist", -1)
	throttle := mapInt(row, "throttle_max", -1)
	enabled := mapInt(row, "enabled", 1)
	return &mtprotoNode{
		ID: mapInt(row, "id", 0), Name: mapStr(row, "name", ""),
		TemplateID: mapInt(row, "template_id", 0), Domain: mapStr(row, "domain", ""),
		Host: mapStr(row, "host", ""), Port: mapInt(row, "port", 0), APIPort: mapInt(row, "api_port", 0),
		SecretMode: mapStr(row, "secret_mode", ""), Concurrency: mapInt(row, "concurrency", 0),
		PreferIP: mapStr(row, "prefer_ip", ""), Blocklist: &block, ThrottleMax: &throttle,
		ConfigPath: mapStr(row, "config_path", ""), ServiceName: mapStr(row, "service_name", ""),
		BootstrapSecret: mapStr(row, "bootstrap_secret", ""), PublicIPv4: mapStr(row, "public_ipv4", ""),
		Enabled: &enabled,
	}
}

func mtprotoTemplateJSON(row map[string]any) map[string]any {
	t := mtprotoTemplateFromRow(row)
	return map[string]any{
		"id": t.ID, "name": t.Name, "remark": mapStr(row, "remark", ""),
		"domain": t.Domain, "port": t.Port, "api_port": t.APIPort, "secret_mode": t.SecretMode,
		"concurrency": t.Concurrency, "prefer_ip": t.PreferIP, "public_ipv4": t.PublicIPv4,
		"blocklist": t.Blocklist, "throttle_max": t.ThrottleMax, "config_path": t.ConfigPath,
		"service_name": t.ServiceName, "enabled": t.Enabled,
		"node_count": mapInt(row, "node_count", 0),
		"created_at": mapInt(row, "created_at", 0), "updated_at": mapInt(row, "updated_at", 0),
	}
}

func mtprotoNodeJSON(row map[string]any, tpl *mtprotoTemplate) map[string]any {
	node := mtprotoNodeFromRow(row)
	eff := mtprotoEffective(tpl, node)
	hostAuto := eff.Host == ""
	return map[string]any{
		"id": node.ID, "server_id": mapInt(row, "server_id", 0),
		"server_name": mapStr(row, "server_name", ""), "server_host": mapStr(row, "server_host", ""),
		"server_ip4": mapStr(row, "server_ip4", ""),
		"name":       node.Name, "template_id": node.TemplateID,
		"template_name": mapStr(row, "template_name", ""),
		"domain":        eff.Domain, "host": eff.Host, "host_auto": hostAuto,
		"port": eff.Port, "api_port": eff.APIPort, "secret_mode": eff.SecretMode,
		"concurrency": eff.Concurrency, "prefer_ip": eff.PreferIP, "public_ipv4": eff.PublicIPv4,
		"blocklist": eff.Blocklist, "throttle_max": eff.ThrottleMax,
		"config_path": eff.ConfigPath, "service_name": eff.ServiceName,
		"engine":       mapStr(row, "engine", "mtg-multi"),
		"enabled":      mapInt(row, "enabled", 1),
		"client_count": mapInt(row, "client_count", 0),
		"service_state": func() string {
			return mtprotoServiceState(mapStr(row, "server_metrics", ""))
		}(),
		"last_apply_at":    mapInt(row, "last_apply_at", 0),
		"last_render_hash": mapStr(row, "last_render_hash", ""),
		"remark":           mapStr(row, "remark", ""),
		"created_at":       mapInt(row, "created_at", 0), "updated_at": mapInt(row, "updated_at", 0),
	}
}

func mtprotoServiceState(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var m struct {
		Services map[string]string `json:"services"`
	}
	if json.Unmarshal([]byte(raw), &m) != nil {
		return ""
	}
	return m.Services["mtg"]
}

func (h *handlers) loadMtprotoTemplate(ctx context.Context, id int64) (*mtprotoTemplate, map[string]any, error) {
	rows, err := queryRowsAsMaps(ctx, h.db, `SELECT * FROM mtproto_templates WHERE id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		return nil, nil, sql.ErrNoRows
	}
	return mtprotoTemplateFromRow(rows[0]), rows[0], nil
}

func (h *handlers) mtprotoNodesWithMeta(ctx context.Context, where string, args ...any) ([]map[string]any, error) {
	return queryRowsAsMaps(ctx, h.db, `SELECT n.*, s.name AS server_name, s.host AS server_host,
	      COALESCE(s.last_ip4, s.last_ip, '') AS server_ip4, s.last_metrics AS server_metrics,
	      t.name AS template_name,
	      (SELECT COUNT(*) FROM mtproto_clients c WHERE c.node_id = n.id AND c.status = 'active') AS client_count
	    FROM mtproto_nodes n
	    LEFT JOIN servers s ON s.id = n.server_id
	    LEFT JOIN mtproto_templates t ON t.id = n.template_id
	    `+where+` ORDER BY n.id ASC`, args...)
}

// GET /api/mtproto/templates
func (h *handlers) mtprotoTemplatesList(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT t.*,
	      (SELECT COUNT(*) FROM mtproto_nodes n WHERE n.template_id = t.id) AS node_count
	    FROM mtproto_templates t ORDER BY t.id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询模板失败")
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, mtprotoTemplateJSON(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

func normalizeMtprotoTemplateInput(body map[string]any) (map[string]any, string) {
	name := strings.TrimSpace(mapStr(body, "name", ""))
	if name == "" {
		return nil, "模板名称不能为空"
	}
	domain := mtprotoDomainOf(mapStr(body, "domain", ""))
	if domain == "" {
		return nil, "伪装域名无效：请填写形如 storage.googleapis.com 的域名"
	}
	port := intOrJS(body["port"], mtprotoDefaultPort, 1, 65535)
	apiPort := intOrJS(body["api_port"], mtprotoDefaultAPIPort, 1, 65535)
	if apiPort == port {
		return nil, "统计端口不能与代理端口相同"
	}
	mode := "multi"
	if strings.EqualFold(mapStr(body, "secret_mode", "multi"), "single") {
		mode = "single"
	}
	prefer := mapStr(body, "prefer_ip", "prefer-ipv4")
	if !containsString(mtprotoPreferIPs, prefer) {
		prefer = "prefer-ipv4"
	}
	publicIPv4 := ""
	if v := strings.TrimSpace(mapStr(body, "public_ipv4", "")); v != "" {
		publicIPv4 = mtprotoIsIPv4(v)
		if publicIPv4 == "" {
			return nil, "public_ipv4 必须是合法 IPv4"
		}
	}
	return map[string]any{
		"name": name, "remark": mapStr(body, "remark", ""), "domain": domain,
		"port": port, "api_port": apiPort, "secret_mode": mode,
		"concurrency": intOrJS(body["concurrency"], 8192, 1, 500000),
		"prefer_ip":   prefer, "public_ipv4": publicIPv4,
		"blocklist":    singboxBool(body["blocklist"], false),
		"throttle_max": intOrJS(body["throttle_max"], 0, 0, 500000),
		"config_path":  firstNonEmpty(strings.TrimSpace(mapStr(body, "config_path", "")), "/etc/mtg/config.toml"),
		"service_name": firstNonEmpty(strings.TrimSpace(mapStr(body, "service_name", "")), "mtg"),
		"enabled":      singboxBool(body["enabled"], true),
	}, ""
}

func (h *handlers) mtprotoTemplateCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	input, msg := normalizeMtprotoTemplateInput(body)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var dup int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM mtproto_templates WHERE name = ?`, input["name"]).Scan(&dup)
	if dup > 0 {
		writeError(w, http.StatusConflict, "模板名称已存在")
		return
	}
	id, err := insertDynamic(r.Context(), h.db, "mtproto_templates", input, nowMillis())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建失败")
		return
	}
	h.writeMtprotoTemplate(w, r, id)
}

func (h *handlers) writeMtprotoTemplate(w http.ResponseWriter, r *http.Request, id int64) {
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT t.*,
	      (SELECT COUNT(*) FROM mtproto_nodes n WHERE n.template_id = t.id) AS node_count
	    FROM mtproto_templates t WHERE t.id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusInternalServerError, "读取模板失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": mtprotoTemplateJSON(rows[0])})
}

func (h *handlers) mtprotoTemplateUpdate(w http.ResponseWriter, r *http.Request) {
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
	input, msg := normalizeMtprotoTemplateInput(body)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var dup int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM mtproto_templates WHERE name = ? AND id <> ?`, input["name"], id).Scan(&dup)
	if dup > 0 {
		writeError(w, http.StatusConflict, "模板名称已存在")
		return
	}
	if err := updateDynamic(r.Context(), h.db, "mtproto_templates", id, input, nowMillis()); err != nil {
		writeError(w, http.StatusInternalServerError, "更新失败")
		return
	}
	h.writeMtprotoTemplate(w, r, id)
}

func (h *handlers) mtprotoTemplateDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	var used int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM mtproto_nodes WHERE template_id = ?`, id).Scan(&used)
	if used > 0 {
		writeError(w, http.StatusConflict, "该模板已被节点绑定，请先解绑")
		return
	}
	_, _ = h.db.ExecContext(r.Context(), `DELETE FROM mtproto_templates WHERE id = ?`, id)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// GET /api/mtproto/nodes
func (h *handlers) mtprotoNodesList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.mtprotoNodesWithMeta(r.Context(), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点失败")
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		tpl, _, _ := h.loadMtprotoTemplate(r.Context(), mapInt(row, "template_id", 0))
		data = append(data, mtprotoNodeJSON(row, tpl))
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

func (h *handlers) mtprotoNodeGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	rows, err := h.mtprotoNodesWithMeta(r.Context(), "WHERE n.id = ?", id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	tpl, _, _ := h.loadMtprotoTemplate(r.Context(), mapInt(rows[0], "template_id", 0))
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": mtprotoNodeJSON(rows[0], tpl)})
}

func normalizeMtprotoNodeInput(body map[string]any) (map[string]any, string) {
	name := strings.TrimSpace(mapStr(body, "name", ""))
	if name == "" {
		return nil, "节点名称不能为空"
	}
	serverID := jsonInt(body["server_id"])
	if serverID <= 0 {
		return nil, "请选择目标服务器"
	}
	domain := ""
	if v := strings.TrimSpace(mapStr(body, "domain", "")); v != "" {
		domain = mtprotoDomainOf(v)
		if domain == "" {
			return nil, "伪装域名无效"
		}
	}
	host := strings.TrimSpace(mapStr(body, "host", ""))
	if host != "" && mtprotoHostOf(host) == "" {
		return nil, "连接地址必须是域名或 IPv4"
	}
	block := int64(-1)
	if v, ok := body["blocklist"]; ok && v != nil {
		b := jsonInt(v)
		if b == 0 || b == 1 {
			block = b
		}
	}
	throttle := int64(-1)
	if v, ok := body["throttle_max"]; ok && v != nil {
		t := jsonInt(v)
		if t >= 0 {
			throttle = t
		}
	}
	return map[string]any{
		"name": name, "server_id": serverID, "template_id": jsonInt(body["template_id"]),
		"domain": domain, "host": host,
		"port":     intOrJS(body["port"], 0, 0, 65535),
		"api_port": intOrJS(body["api_port"], 0, 0, 65535),
		"secret_mode": func() string {
			s := mapStr(body, "secret_mode", "")
			if s == "single" {
				return "single"
			}
			if s == "multi" {
				return "multi"
			}
			return ""
		}(),
		"concurrency": intOrJS(body["concurrency"], 0, 0, 500000),
		"prefer_ip": func() string {
			s := mapStr(body, "prefer_ip", "")
			if containsString(mtprotoPreferIPs, s) {
				return s
			}
			return ""
		}(),
		"public_ipv4": mtprotoIsIPv4(mapStr(body, "public_ipv4", "")),
		"blocklist":   block, "throttle_max": throttle,
		"config_path": mapStr(body, "config_path", ""), "service_name": mapStr(body, "service_name", ""),
		"bootstrap_secret": mapStr(body, "bootstrap_secret", ""),
		"enabled":          singboxBool(body["enabled"], true),
		"remark":           mapStr(body, "remark", ""),
	}, ""
}

func (h *handlers) mtprotoNodeCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	input, msg := normalizeMtprotoNodeInput(body)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var dup int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM mtproto_nodes WHERE server_id = ?`, input["server_id"]).Scan(&dup)
	if dup > 0 {
		writeError(w, http.StatusConflict, "该服务器已绑定过 MTProto 节点")
		return
	}
	tpl, _, err := h.loadMtprotoTemplate(r.Context(), jsonInt(input["template_id"]))
	if err != nil {
		writeError(w, http.StatusNotFound, "协议模板不存在")
		return
	}
	if s, _ := input["domain"].(string); s == "" {
		input["domain"] = tpl.Domain
	}
	if s, _ := input["bootstrap_secret"].(string); s == "" {
		// 用节点实际的伪装域名生成占位 secret；只有节点没填时才回落到模板域名
		domain := ""
		if d, _ := input["domain"].(string); d != "" {
			domain = d
		}
		if domain == "" {
			domain = tpl.Domain
		}
		secret := mtprotoGenerateSecret(domain)
		if secret == "" {
			writeError(w, http.StatusBadRequest, "无法生成 bootstrap_secret：请先填写有效伪装域名（如 www.bing.com）")
			return
		}
		input["bootstrap_secret"] = secret
	}
	id, err := insertDynamic(r.Context(), h.db, "mtproto_nodes", input, nowMillis())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建失败")
		return
	}
	h.mtprotoWriteNode(w, r, id)
}

func (h *handlers) mtprotoWriteNode(w http.ResponseWriter, r *http.Request, id int64) {
	rows, err := h.mtprotoNodesWithMeta(r.Context(), "WHERE n.id = ?", id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusInternalServerError, "读取节点失败")
		return
	}
	tpl, _, _ := h.loadMtprotoTemplate(r.Context(), mapInt(rows[0], "template_id", 0))
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": mtprotoNodeJSON(rows[0], tpl)})
}

func (h *handlers) mtprotoNodeUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	input, msg := normalizeMtprotoNodeInput(body)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := updateDynamic(r.Context(), h.db, "mtproto_nodes", id, input, nowMillis()); err != nil {
		writeError(w, http.StatusInternalServerError, "更新失败")
		return
	}
	h.mtprotoWriteNode(w, r, id)
}

func (h *handlers) mtprotoNodeDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	_, _ = h.db.ExecContext(r.Context(), `DELETE FROM mtproto_clients WHERE node_id = ?`, id)
	_, _ = h.db.ExecContext(r.Context(), `DELETE FROM mtproto_nodes WHERE id = ?`, id)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// POST /api/mtproto/nodes/{id}/apply
func (h *handlers) mtprotoNodeApply(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	rows, err := h.mtprotoNodesWithMeta(r.Context(), "WHERE n.id = ?", id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	row := rows[0]
	serverID := mapInt(row, "server_id", 0)
	tpl, _, err := h.loadMtprotoTemplate(r.Context(), mapInt(row, "template_id", 0))
	if err != nil {
		writeError(w, http.StatusNotFound, "协议模板不存在")
		return
	}
	node := mtprotoNodeFromRow(row)
	eff := mtprotoEffective(tpl, node)

	clientRows, _ := queryRowsAsMaps(r.Context(), h.db,
		`SELECT * FROM mtproto_clients WHERE node_id = ? ORDER BY id ASC`, id)
	clients := make([]mtprotoClient, 0, len(clientRows))
	for _, c := range clientRows {
		clients = append(clients, mtprotoClient{
			ID: mapInt(c, "id", 0), UUID: mapStr(c, "uuid", ""), Email: mapStr(c, "email", ""),
			Label: mapStr(c, "label", ""), Secret: mapStr(c, "secret", ""),
			Status: firstNonEmpty(mapStr(c, "status", ""), "active"),
		})
	}
	content, ports, mode, msg := mtprotoRenderConfig(&eff, clients)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	hash := sha256Hex(content)
	cfgName := "mtpx:" + node.Name + ":mtg"
	var cfgID, version int64
	var existHash, existPath, existRestart string
	err = h.db.QueryRowContext(r.Context(),
		`SELECT id, COALESCE(content_hash,''), COALESCE(target_path,''), COALESCE(restart_cmd,'') FROM node_configs WHERE name = ? LIMIT 1`,
		cfgName).Scan(&cfgID, &existHash, &existPath, &existRestart)
	changed := false
	if err == sql.ErrNoRows {
		res, err := h.db.ExecContext(r.Context(), `INSERT INTO node_configs
		  (name, server_id, engine, target_path, service_name, restart_cmd, check_cmd, content, backup,
		   enabled, auto_push, push_interval_min, template_id, uuid, version, content_hash, created_at, updated_at)
		  VALUES (?, ?, 'mtproto', ?, ?, ?, ?, ?, 1, 1, 0, 0, 0, '', 1, ?, ?, ?)`,
			cfgName, serverID, eff.ConfigPath, eff.ServiceName, mtprotoReloadCmd, mtprotoCheckCmd, content, hash, nowMillis(), nowMillis())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "写入配置失败")
			return
		}
		cfgID, _ = res.LastInsertId()
		version, changed = 1, true
	} else if err == nil {
		same := existHash == hash && existPath == eff.ConfigPath && existRestart == mtprotoReloadCmd
		if same {
			_ = h.db.QueryRowContext(r.Context(), `SELECT COALESCE(version,0) FROM node_configs WHERE id = ?`, cfgID).Scan(&version)
		} else {
			_, _ = h.db.ExecContext(r.Context(), `UPDATE node_configs SET server_id = ?, engine = 'mtproto',
			  target_path = ?, service_name = ?, restart_cmd = ?, check_cmd = ?, content = ?, content_hash = ?,
			  version = COALESCE(version,0) + 1, enabled = 1, updated_at = ? WHERE id = ?`,
				serverID, eff.ConfigPath, eff.ServiceName, mtprotoReloadCmd, mtprotoCheckCmd, content, hash, nowMillis(), cfgID)
			_ = h.db.QueryRowContext(r.Context(), `SELECT COALESCE(version,0) FROM node_configs WHERE id = ?`, cfgID).Scan(&version)
			changed = true
		}
	}
	_, _ = h.db.ExecContext(r.Context(),
		`UPDATE mtproto_nodes SET last_apply_at = ?, last_render_hash = ?, updated_at = ? WHERE id = ?`,
		nowMillis(), hash, nowMillis(), id)

	queued := false
	if cfgID > 0 {
		if srv, err := h.fetchAgentServer(r.Context(), serverID); err == nil {
			if cfg, err := h.loadNodeConfig(r.Context(), cfgID); err == nil {
				payload := h.agentBuildDeployPayload(r.Context(), cfg, srv)
				_, _, qmsg := h.agentQueueTask(r.Context(), srv, "deploy", cfg.ID, payload, "mtproto")
				queued = qmsg == ""
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "changed": changed, "version": version, "node_config_id": cfgID,
		"path": eff.ConfigPath, "ports": ports, "mode": mode, "content": content,
		"queued":  queued,
		"message": "已写入配置；Agent 下次 poll 会自动下发",
	})
}

// GET /api/mtproto/nodes/{id}/clients
func (h *handlers) mtprotoClientsList(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	reveal := r.URL.Query().Get("reveal") == "1"
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT * FROM mtproto_clients WHERE node_id = ? ORDER BY id ASC`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询客户端失败")
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		secret := mapStr(row, "secret", "")
		if !reveal {
			secret = mtprotoMaskSecret(secret)
		}
		data = append(data, map[string]any{
			"id": mapInt(row, "id", 0), "node_id": mapInt(row, "node_id", 0),
			"uuid": mapStr(row, "uuid", ""), "email": mapStr(row, "email", ""),
			"label": mapStr(row, "label", ""), "secret": secret,
			"secret_domain": mtprotoSecretDomain(mapStr(row, "secret", "")),
			"status":        firstNonEmpty(mapStr(row, "status", ""), "active"),
			"delivered_at":  mapInt(row, "delivered_at", 0), "deliver_count": mapInt(row, "deliver_count", 0),
			"last_mail_at": mapInt(row, "last_mail_at", 0), "revoked_at": mapInt(row, "revoked_at", 0),
			"remark":     mapStr(row, "remark", ""),
			"created_at": mapInt(row, "created_at", 0), "updated_at": mapInt(row, "updated_at", 0),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

// POST /api/mtproto/nodes/{id}/clients
func (h *handlers) mtprotoClientCreate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	rows, err := h.mtprotoNodesWithMeta(r.Context(), "WHERE n.id = ?", id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	tpl, _, _ := h.loadMtprotoTemplate(r.Context(), mapInt(rows[0], "template_id", 0))
	node := mtprotoNodeFromRow(rows[0])
	eff := mtprotoEffective(tpl, node)
	domain := firstNonEmpty(strings.TrimSpace(mapStr(body, "domain", "")), eff.Domain)
	secret := mtprotoGenerateSecret(domain)
	if secret == "" {
		writeError(w, http.StatusBadRequest, "生成 secret 失败：请先填写有效伪装域名")
		return
	}
	uuid := strings.TrimSpace(mapStr(body, "uuid", ""))
	ins, err := h.db.ExecContext(r.Context(), `INSERT INTO mtproto_clients
	  (node_id, uuid, email, label, secret, status, remark, created_at, updated_at)
	  VALUES (?, ?, ?, ?, ?, 'active', ?, ?, ?)`,
		id, uuid, mapStr(body, "email", ""), mapStr(body, "label", ""), secret,
		mapStr(body, "remark", ""), nowMillis(), nowMillis())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建客户端失败")
		return
	}
	cid, _ := ins.LastInsertId()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"id": cid, "node_id": id, "uuid": uuid, "email": mapStr(body, "email", ""),
		"label": mapStr(body, "label", ""), "secret": secret,
		"secret_domain": mtprotoSecretDomain(secret), "status": "active",
	}})
}

// DELETE /api/mtproto/clients/{id}
func (h *handlers) mtprotoClientDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "客户端不存在")
		return
	}
	_, _ = h.db.ExecContext(r.Context(),
		`UPDATE mtproto_clients SET status = 'revoked', revoked_at = ?, updated_at = ? WHERE id = ?`,
		nowMillis(), nowMillis(), id)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
