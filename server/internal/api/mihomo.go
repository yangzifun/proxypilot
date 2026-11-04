package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// mihomoLinkAllowed 按协议集合过滤链接；CDN 链接用 vless-ws-cdn 单独开关。
func mihomoLinkAllowed(item map[string]any, protoSet map[string]bool) bool {
	if len(protoSet) == 0 {
		return true
	}
	proto := mapStr(item, "protocol", "")
	isCDN := item["cdn"] == true
	switch {
	case proto == "vless-ws" && isCDN:
		return protoSet["vless-ws-cdn"]
	case proto == "vless-ws":
		return protoSet["vless-ws"]
	default:
		return protoSet[proto]
	}
}

type mihomoAdvanced struct {
	GroupType  string `json:"group_type"`
	TestURL    string `json:"test_url"`
	ExtraRules string `json:"extra_rules"`
}

// applyMihomoAdvanced 把绑定的高级项写进生成的 YAML。
func applyMihomoAdvanced(yamlText string, adv mihomoAdvanced) string {
	out := yamlText
	if adv.GroupType != "" {
		out = strings.Replace(out, "type: url-test", "type: "+adv.GroupType, 1)
		if adv.GroupType != "url-test" && adv.GroupType != "fallback" && adv.GroupType != "load-balance" {
			out = strings.Replace(out, "    url: http://www.gstatic.com/generate_204\n", "", 1)
			out = strings.Replace(out, "    interval: 300\n", "", 1)
			out = strings.Replace(out, "    tolerance: 50\n", "", 1)
		}
	}
	if strings.TrimSpace(adv.TestURL) != "" {
		out = strings.Replace(out, "url: http://www.gstatic.com/generate_204", "url: "+strings.TrimSpace(adv.TestURL), 1)
	}
	if strings.TrimSpace(adv.ExtraRules) != "" {
		lines := strings.Split(out, "\n")
		insertAt := -1
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "- MATCH,") {
				insertAt = i
				break
			}
		}
		extra := []string{}
		for _, l := range strings.Split(adv.ExtraRules, "\n") {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			if !strings.HasPrefix(l, "- ") {
				l = "- " + l
			}
			extra = append(extra, "  "+l)
		}
		if len(extra) > 0 {
			if insertAt >= 0 {
				out = strings.Join(append(append(append([]string{}, lines[:insertAt]...), extra...), lines[insertAt:]...), "\n")
			} else {
				out = strings.TrimRight(out, "\n") + "\n" + strings.Join(extra, "\n") + "\n"
			}
		}
	}
	return out
}

// ---------------- mihomo 模板管理 / 节点绑定 ----------------

type mihomoTemplateInput struct {
	Name       string   `json:"name"`
	Remark     string   `json:"remark"`
	Content    string   `json:"content"`
	Enabled    *bool    `json:"enabled"`
	Protocols  []string `json:"protocols"`
	Mode       string   `json:"mode"`
	ServerJSON string   `json:"server_json"`
}

type mihomoNodeInput struct {
	Name       string          `json:"name"`
	ServerID   int64           `json:"server_id"`
	TemplateID int64           `json:"template_id"`
	UUIDList   []string        `json:"uuid_list"`
	Protocols  []string        `json:"protocols"`
	Advanced   *mihomoAdvanced `json:"advanced"`
	Enabled    *bool           `json:"enabled"`
	Remark     string          `json:"remark"`
	PortBase   int64           `json:"port_base"`
	Domain     string          `json:"domain"`
	ConfigPath string          `json:"config_path"`
	Service    string          `json:"service"`
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
		        COALESCE(enabled,1) AS enabled, COALESCE(protocols,'[]') AS protocols, COALESCE(mode,'subscription') AS mode,
		        (SELECT COUNT(*) FROM mihomo_nodes n WHERE n.template_id = mihomo_templates.id) AS bind_count,
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
	var protocolsRaw, mode, serverJSON string
	var enabled int
	var createdAt, updatedAt int64
	err := h.db.QueryRowContext(r.Context(),
		`SELECT name, COALESCE(remark,''), COALESCE(content,''), COALESCE(enabled,1), COALESCE(protocols,'[]'),
		        COALESCE(mode,'subscription'), COALESCE(server_json,''), created_at, updated_at
		   FROM mihomo_templates WHERE id = ? LIMIT 1`,
		id).Scan(&name, &remark, &content, &enabled, &protocolsRaw, &mode, &serverJSON, &createdAt, &updatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"id": id, "name": name, "remark": remark, "content": content, "enabled": enabled,
		"protocols":   mihomoParseProtocols(protocolsRaw),
		"mode":        mode,
		"server_json": serverJSON,
		"created_at":  createdAt, "updated_at": updatedAt,
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
	isServer := strings.EqualFold(strings.TrimSpace(in.Mode), "server")
	if !isServer && strings.TrimSpace(in.Content) == "" {
		writeError(w, http.StatusBadRequest, "请填写 mihomo YAML 模板内容")
		return
	}
	if !isServer {
		if msg := validateMihomoYAML(in.Content); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	}
	now := nowMillis()
	enabled := 1
	if in.Enabled != nil && !*in.Enabled {
		enabled = 0
	}
	protoRaw, _ := json.Marshal(in.Protocols)
	mode := strings.TrimSpace(in.Mode)
	if mode == "" {
		mode = "subscription"
	}
	res, err := h.db.ExecContext(r.Context(),
		`INSERT INTO mihomo_templates (name, remark, content, enabled, protocols, mode, server_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, in.Remark, in.Content, enabled, string(protoRaw), mode, in.ServerJSON, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "1062") {
			writeError(w, http.StatusBadRequest, "模板名称已存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "创建模板失败")
		return
	}
	id, _ := res.LastInsertId()
	h.mihomoSaveVersion(r, id, name, in.Remark, in.Content)
	h.mihomoAudit(r, "mihomo-template-create", 0, "新建模板："+name)
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
	isServer := strings.EqualFold(strings.TrimSpace(in.Mode), "server")
	if !isServer && strings.TrimSpace(in.Content) == "" {
		writeError(w, http.StatusBadRequest, "请填写 mihomo YAML 模板内容")
		return
	}
	if !isServer {
		if msg := validateMihomoYAML(in.Content); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	}
	enabled := 1
	if in.Enabled != nil && !*in.Enabled {
		enabled = 0
	}
	protoRaw, _ := json.Marshal(in.Protocols)
	mode := strings.TrimSpace(in.Mode)
	if mode == "" {
		mode = "subscription"
	}
	_, err := h.db.ExecContext(r.Context(),
		`UPDATE mihomo_templates SET name = ?, remark = ?, content = ?, enabled = ?, protocols = ?, mode = ?, server_json = ?, updated_at = ? WHERE id = ?`,
		name, in.Remark, in.Content, enabled, string(protoRaw), mode, in.ServerJSON, nowMillis(), id)
	if err != nil {
		if strings.Contains(err.Error(), "1062") {
			writeError(w, http.StatusBadRequest, "模板名称已存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "保存模板失败")
		return
	}
	h.mihomoSaveVersion(r, id, name, in.Remark, in.Content)
	h.mihomoAudit(r, "mihomo-template-update", 0, "保存模板："+name)
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
	h.mihomoAudit(r, "mihomo-template-delete", 0, fmt.Sprintf("删除模板 %d", id))
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// GET /api/mihomo/nodes
func (h *handlers) mihomoNodesList(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT n.id, n.name, n.server_id, n.template_id, COALESCE(n.uuid_list,'[]') AS uuid_list,
		        COALESCE(n.protocols,'[]') AS protocols, COALESCE(n.advanced_json,'{}') AS advanced_json,
		        COALESCE(n.last_preview_at,0) AS last_preview_at,
		        COALESCE(n.port_base,0) AS port_base, COALESCE(n.domain,'') AS domain,
		        COALESCE(n.config_path,'') AS config_path, COALESCE(n.service,'') AS service,
		        COALESCE(n.last_apply_at,0) AS last_apply_at,
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
	nowT := time.Now()
	loc := nowT.Location()
	dayStart := time.Date(nowT.Year(), nowT.Month(), nowT.Day(), 0, 0, 0, 0, loc).UnixMilli()
	weekStart := dayStart - 6*86400000
	onlineSince := nowT.Add(-5 * time.Minute).UnixMilli()
	for _, row := range rows {
		list := mihomoParseUUIDs(mapStr(row, "uuid_list", "[]"))
		row["uuid_list"] = list
		row["uuid_count"] = len(list)
		row["protocols"] = mihomoParseProtocols(mapStr(row, "protocols", "[]"))
		adv := mihomoAdvanced{}
		_ = json.Unmarshal([]byte(mapStr(row, "advanced_json", "{}")), &adv)
		row["advanced"] = adv
		var today, week, total int64
		var online int
		if len(list) > 0 {
			ph := strings.TrimSuffix(strings.Repeat("?,", len(list)), ",")
			args := make([]any, 0, len(list))
			for _, u := range list {
				args = append(args, u)
			}
			_ = h.db.QueryRowContext(r.Context(),
				`SELECT COALESCE(SUM(up+down),0), COALESCE(SUM(CASE WHEN hour_ts >= ? THEN up+down ELSE 0 END),0)
				   FROM user_traffic_hourly WHERE uuid IN (`+ph+`) AND hour_ts >= ?`,
				append([]any{dayStart}, append(args, weekStart)...)...).Scan(&today, &week)
			_ = h.db.QueryRowContext(r.Context(),
				`SELECT COALESCE(SUM(up_total+down_total),0),
				        COALESCE(SUM(CASE WHEN last_active_at >= ? THEN 1 ELSE 0 END),0)
				   FROM user_traffic WHERE uuid IN (`+ph+`)`,
				append([]any{onlineSince}, args...)...).Scan(&total, &online)
		}
		if len(list) == 0 {
			var nodeID int64
			if h.db.QueryRowContext(r.Context(),
				`SELECT id FROM singbox_nodes WHERE server_id = ? ORDER BY enabled DESC, id ASC LIMIT 1`,
				mapInt(row, "server_id", 0)).Scan(&nodeID) == nil && nodeID > 0 {
				_ = h.db.QueryRowContext(r.Context(),
					`SELECT COALESCE(SUM(up+down),0),
					        COALESCE(SUM(CASE WHEN hour_ts >= ? THEN up+down ELSE 0 END),0),
					        COALESCE(SUM(CASE WHEN hour_ts >= ? THEN up+down ELSE 0 END),0)
					   FROM user_traffic_hourly WHERE node_id = ?`,
					dayStart, weekStart, nodeID).Scan(&total, &today, &week)
				_ = h.db.QueryRowContext(r.Context(),
					`SELECT COUNT(DISTINCT uuid) FROM user_traffic_hourly WHERE node_id = ? AND hour_ts >= ?`,
					nodeID, onlineSince).Scan(&online)
			}
		}
		row["traffic_today"] = today
		row["traffic_week"] = week
		row["traffic_total"] = total
		row["online_count"] = online
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
	protoRaw, _ := json.Marshal(in.Protocols)
	advRaw, _ := json.Marshal(in.Advanced)
	if in.Advanced == nil {
		advRaw = []byte("{}")
	}
	now := nowMillis()
	cfgPath := strings.TrimSpace(in.ConfigPath)
	if cfgPath == "" {
		cfgPath = "/etc/mihomo/config.yaml"
	}
	svc := strings.TrimSpace(in.Service)
	if svc == "" {
		svc = "mihomo"
	}
	res, err := h.db.ExecContext(r.Context(),
		`INSERT INTO mihomo_nodes (name, server_id, template_id, uuid_list, protocols, advanced_json, enabled, remark,
		   port_base, domain, config_path, service, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(in.Name), in.ServerID, in.TemplateID, string(raw), string(protoRaw), string(advRaw), enabled, in.Remark,
		in.PortBase, strings.TrimSpace(in.Domain), cfgPath, svc, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "1062") {
			writeError(w, http.StatusBadRequest, "名称或该服务器的绑定已存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "创建绑定失败")
		return
	}
	id, _ := res.LastInsertId()
	h.mihomoAudit(r, "mihomo-bind-create", in.ServerID, "新建绑定："+strings.TrimSpace(in.Name))
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
	protoRaw, _ := json.Marshal(in.Protocols)
	advRaw, _ := json.Marshal(in.Advanced)
	if in.Advanced == nil {
		advRaw = []byte("{}")
	}
	cfgPath := strings.TrimSpace(in.ConfigPath)
	if cfgPath == "" {
		cfgPath = "/etc/mihomo/config.yaml"
	}
	svc := strings.TrimSpace(in.Service)
	if svc == "" {
		svc = "mihomo"
	}
	_, err := h.db.ExecContext(r.Context(),
		`UPDATE mihomo_nodes SET name = ?, server_id = ?, template_id = ?, uuid_list = ?, protocols = ?, advanced_json = ?,
		   enabled = ?, remark = ?, port_base = ?, domain = ?, config_path = ?, service = ?, updated_at = ?
		  WHERE id = ?`,
		strings.TrimSpace(in.Name), in.ServerID, in.TemplateID, string(raw), string(protoRaw), string(advRaw),
		enabled, in.Remark, in.PortBase, strings.TrimSpace(in.Domain), cfgPath, svc, nowMillis(), id)
	if err != nil {
		if strings.Contains(err.Error(), "1062") {
			writeError(w, http.StatusBadRequest, "名称或该服务器的绑定已存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "保存绑定失败")
		return
	}
	h.mihomoAudit(r, "mihomo-bind-update", in.ServerID, "保存绑定："+strings.TrimSpace(in.Name))
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
	h.mihomoAudit(r, "mihomo-bind-delete", 0, fmt.Sprintf("删除绑定 %d", id))
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
	if len(in.UUIDList) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(in.UUIDList)), ",")
		args := make([]any, 0, len(in.UUIDList))
		for _, u := range in.UUIDList {
			args = append(args, strings.ToLower(strings.TrimSpace(u)))
		}
		var known int
		_ = h.db.QueryRowContext(ctx,
			`SELECT COUNT(DISTINCT uuid) FROM (SELECT uuid FROM uuid_info UNION SELECT uuid FROM configs) t WHERE uuid IN (`+ph+`)`,
			args...).Scan(&known)
		if known != len(in.UUIDList) {
			return "存在未授权/不存在的 UUID，请从已授权列表中选择"
		}
		var nodeUUIDRaw string
		if h.db.QueryRowContext(ctx,
			`SELECT COALESCE(uuid_list,'[]') FROM singbox_nodes WHERE server_id = ? LIMIT 1`, in.ServerID).
			Scan(&nodeUUIDRaw) == nil {
			allowed := mihomoParseUUIDs(nodeUUIDRaw)
			if len(allowed) > 0 {
				allowedSet := map[string]bool{}
				for _, u := range allowed {
					allowedSet[strings.ToLower(u)] = true
				}
				for _, u := range in.UUIDList {
					if !allowedSet[strings.ToLower(strings.TrimSpace(u))] {
						return "UUID " + shortUUID(u) + " 未授权该服务器"
					}
				}
			}
		}
	}
	return ""
}

func shortUUID(u string) string {
	u = strings.TrimSpace(u)
	if len(u) <= 12 {
		return u
	}
	return u[:8] + "…" + u[len(u)-4:]
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
		protoRaw   string
		advRaw     string
		tplProto   string
		tplEnabled int
		tplName    string
		tplContent string
		serverName string
	)
	err := h.db.QueryRowContext(r.Context(),
		`SELECT n.name, n.server_id, n.template_id, COALESCE(n.uuid_list,'[]'), COALESCE(n.protocols,'[]'), COALESCE(n.advanced_json,'{}'),
		        COALESCE(t.name,''), COALESCE(t.content,''), COALESCE(s.name,''), COALESCE(t.enabled,1), COALESCE(t.protocols,'[]')
		   FROM mihomo_nodes n
		   LEFT JOIN mihomo_templates t ON t.id = n.template_id
		   LEFT JOIN servers s ON s.id = n.server_id
		  WHERE n.id = ? LIMIT 1`, id).
		Scan(&name, &serverID, &templateID, &uuidRaw, &protoRaw, &advRaw, &tplName, &tplContent, &serverName, &tplEnabled, &tplProto)
	if err != nil {
		writeError(w, http.StatusNotFound, "绑定不存在")
		return
	}
	if strings.TrimSpace(tplContent) == "" {
		writeError(w, http.StatusBadRequest, "模板内容为空，请先完善模板")
		return
	}
	if tplEnabled == 0 {
		writeError(w, http.StatusBadRequest, "模板已停用，请先启用后再生成")
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
	/* 绑定协议为空时继承模板默认协议（与 sing-box 模板级协议开关对齐） */
	effective := mihomoParseProtocols(protoRaw)
	if len(effective) == 0 {
		effective = mihomoParseProtocols(tplProto)
	}
	protoSet := map[string]bool{}
	for _, p := range effective {
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
		writeError(w, http.StatusBadRequest, "未能生成节点链接")
		return
	}
	proxies, skippedBy, protocols, reasons := h.buildClashProxies(r.Context(), links)
	if len(proxies) <= 1 {
		writeError(w, http.StatusBadRequest, "未能生成有效的 mihomo 节点")
		return
	}
	finalConfig := injectClashProxies(tplContent, proxies)
	/* 把分组里的 {{PROXY_NAMES}} 替换为实际节点名（6 空格缩进，匹配默认模板） */
	if strings.Contains(finalConfig, "{{PROXY_NAMES}}") {
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
		finalConfig = strings.ReplaceAll(finalConfig, "{{PROXY_NAMES}}", strings.Join(names, "\n"))
	}
	skipped := 0
	for _, n := range skippedBy {
		skipped += n
	}
	details := make([]map[string]any, 0, len(skippedBy))
	for proto, count := range skippedBy {
		details = append(details, map[string]any{"protocol": proto, "count": count, "reason": reasons[proto]})
	}
	adv := mihomoAdvanced{}
	_ = json.Unmarshal([]byte(advRaw), &adv)
	finalConfig = applyMihomoAdvanced(finalConfig, adv)
	_, _ = h.db.ExecContext(r.Context(), `UPDATE mihomo_nodes SET last_preview_at = ? WHERE id = ?`, nowMillis(), id)
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

// 默认 mihomo 模板：proxies 下留空行用于注入节点，proxy-groups 用 {{PROXY_NAMES}} 占位
const mihomoDefaultTemplate = `mixed-port: 7890
allow-lan: false
mode: rule
log-level: info
ipv6: false
unified-delay: true
tcp-concurrent: true
external-controller: 127.0.0.1:9090

dns:
  enable: true
  ipv6: false
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  nameserver:
    - https://223.5.5.5/dns-query
    - https://1.1.1.1/dns-query
  fallback:
    - https://8.8.8.8/dns-query

proxies:

proxy-groups:
  - name: Proxy
    type: select
    proxies:
      - Auto
      - DIRECT
  - name: Auto
    type: url-test
    url: http://www.gstatic.com/generate_204
    interval: 300
    tolerance: 50
    proxies:
      - {{PROXY_NAMES}}

rules:
  - GEOIP,LAN,DIRECT,no-resolve
  - GEOIP,CN,DIRECT
  - MATCH,Proxy
`

// ensureMihomoDefaultTemplate 确保存在名为「默认配置」的模板（幂等）。
func (h *handlers) ensureMihomoDefaultTemplate(ctx context.Context) {
	var n int
	if err := h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mihomo_templates WHERE name = '默认配置'`).Scan(&n); err != nil || n > 0 {
		return
	}
	now := nowMillis()
	_, _ = h.db.ExecContext(ctx,
		`INSERT INTO mihomo_templates (name, remark, content, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		"默认配置", "开箱即用：fake-ip DNS、自动测速分组、国内直连", mihomoDefaultTemplate, now, now)
}

// validateMihomoYAML 校验模板 YAML 语法与必备结构。
func validateMihomoYAML(content string) string {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return "YAML 语法错误：" + err.Error()
	}
	if doc == nil {
		return "模板内容为空或不是 YAML 对象"
	}
	if _, ok := doc["proxies"]; !ok {
		return "模板缺少 proxies: 节点注入位置"
	}
	if _, ok := doc["proxy-groups"]; !ok {
		if _, ok2 := doc["rules"]; !ok2 {
			return "模板至少需要 proxy-groups 或 rules"
		}
	}
	return ""
}

func (h *handlers) mihomoAudit(r *http.Request, action string, serverID int64, msg string) {
	actor := ""
	if c, ok := claimsFrom(r); ok {
		actor = c.Sub
	}
	_, _ = h.db.ExecContext(r.Context(),
		`INSERT INTO node_push_logs (config_id, server_id, action, status, exit_code, message, output, duration_ms, actor, created_at)
		 VALUES (0, ?, ?, 'success', 0, ?, '', 0, ?, ?)`,
		serverID, action, truncateRunes(msg, 2000), actor, nowMillis())
}

func (h *handlers) mihomoSaveVersion(r *http.Request, templateID int64, name, remark, content string) {
	actor := ""
	if c, ok := claimsFrom(r); ok {
		actor = c.Sub
	}
	_, _ = h.db.ExecContext(r.Context(),
		`INSERT INTO mihomo_template_versions (template_id, name, remark, content, actor, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		templateID, name, remark, content, actor, nowMillis())
}

// GET /api/mihomo/templates/{id}/versions
func (h *handlers) mihomoTemplateVersions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT id, name, CHAR_LENGTH(COALESCE(content,'')) AS content_len, COALESCE(actor,'') AS actor, created_at
		   FROM mihomo_template_versions WHERE template_id = ? ORDER BY id DESC LIMIT 100`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询版本失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": rows})
}

// GET /api/mihomo/templates/{id}/versions/{vid}
func (h *handlers) mihomoTemplateVersionGet(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	vid, err := strconv.ParseInt(pathParam(r, "vid"), 10, 64)
	if err != nil || vid <= 0 {
		writeError(w, http.StatusNotFound, "版本不存在")
		return
	}
	var name, remark, content string
	var createdAt int64
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT name, COALESCE(remark,''), COALESCE(content,''), created_at FROM mihomo_template_versions
		  WHERE id = ? AND template_id = ? LIMIT 1`, vid, id).Scan(&name, &remark, &content, &createdAt); err != nil {
		writeError(w, http.StatusNotFound, "版本不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"id": vid, "template_id": id, "name": name, "remark": remark, "content": content, "created_at": createdAt,
	}})
}

// POST /api/mihomo/templates/{id}/versions/{vid}/restore
func (h *handlers) mihomoTemplateVersionRestore(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	vid, err := strconv.ParseInt(pathParam(r, "vid"), 10, 64)
	if err != nil || vid <= 0 {
		writeError(w, http.StatusNotFound, "版本不存在")
		return
	}
	var name, remark, content string
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT name, COALESCE(remark,''), COALESCE(content,'') FROM mihomo_template_versions
		  WHERE id = ? AND template_id = ? LIMIT 1`, vid, id).Scan(&name, &remark, &content); err != nil {
		writeError(w, http.StatusNotFound, "版本不存在")
		return
	}
	if msg := validateMihomoYAML(content); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE mihomo_templates SET name = ?, remark = ?, content = ?, updated_at = ? WHERE id = ?`,
		name, remark, content, nowMillis(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "回滚失败")
		return
	}
	h.mihomoSaveVersion(r, id, name, remark, content)
	h.mihomoAudit(r, "mihomo-template-restore", 0, fmt.Sprintf("回滚模板 %d 到版本 %d", id, vid))
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// POST /api/mihomo/templates/{id}/enabled
func (h *handlers) mihomoTemplateToggle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	enabled := 1
	if body.Enabled != nil && !*body.Enabled {
		enabled = 0
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE mihomo_templates SET enabled = ?, updated_at = ? WHERE id = ?`, enabled, nowMillis(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	h.mihomoAudit(r, "mihomo-template-toggle", 0, fmt.Sprintf("模板 %d 启用=%d", id, enabled))
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "enabled": enabled})
}
