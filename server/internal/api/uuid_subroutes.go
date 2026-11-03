package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// GET/PUT/DELETE /api/uuids/{uuid}
func (h *handlers) uuidDetail(w http.ResponseWriter, r *http.Request) {
	uuid := pathParam(r, "uuid")
	if !uuidRe.MatchString(strings.ToLower(uuid)) {
		writeError(w, http.StatusBadRequest, "UUID格式无效")
		return
	}
	switch r.Method {
	case http.MethodGet:
		rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT * FROM uuid_info WHERE uuid = ? LIMIT 1`, uuid)
		var info map[string]any
		if err == nil && len(rows) > 0 {
			info = rows[0]
		}
		var count int64
		_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM configs WHERE uuid = ?`, uuid).Scan(&count)
		if info == nil && count == 0 {
			writeError(w, http.StatusNotFound, "UUID Not Found")
			return
		}
		body := map[string]any{
			"success": true, "uuid": uuid, "uuid_info": info, "count": count, "node_count": count,
		}
		if info != nil {
			body["owner"] = mapStr(info, "owner", "")
			body["email"] = mapStr(info, "email", "")
			body["contact_info"] = mapStr(info, "contact_info", "")
			body["status"] = normalizeUuidStatus(mapStr(info, "status", ""))
			body["remark"] = mapStr(info, "remark", "")
			body["access_count"] = mapInt(info, "access_count", 0)
			body["last_accessed"] = mapInt(info, "last_accessed", 0)
		} else {
			body["status"] = "active"
		}
		if v := r.URL.Query().Get("include_configs"); v == "1" || strings.EqualFold(v, "true") {
			cs, err := queryRowsAsMaps(r.Context(), h.db,
				`SELECT id, uuid, config_data, protocol, remark, created_at, updated_at
				   FROM configs WHERE uuid = ? AND config_data IS NOT NULL AND config_data != '' ORDER BY id ASC`, uuid)
			if err == nil {
				body["configs"] = cs
			}
		}
		if v := r.URL.Query().Get("include_sub_sources"); v == "1" || strings.EqualFold(v, "true") {
			ss, _ := queryRowsAsMaps(r.Context(), h.db,
				`SELECT id, url, remark, enabled FROM uuid_sub_sources WHERE uuid = ? ORDER BY id ASC`, uuid)
			body["sub_sources"] = ss
		}
		writeJSON(w, http.StatusOK, body)

	case http.MethodPut:
		var body map[string]any
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "无效 JSON")
			return
		}
		now := nowMillis()
		_, err := h.db.ExecContext(r.Context(), `INSERT INTO uuid_info
		  (uuid, owner, email, contact_info, status, remark, access_count, last_accessed, created_at, updated_at)
		  VALUES (?, ?, ?, ?, ?, ?, 0, 0, ?, ?)
		  ON DUPLICATE KEY UPDATE owner = VALUES(owner), email = VALUES(email),
		    contact_info = VALUES(contact_info), status = VALUES(status), remark = VALUES(remark), updated_at = VALUES(updated_at)`,
			uuid, mapStr(body, "owner", ""), normalizeEmail(mapStr(body, "email", "")),
			mapStr(body, "contact_info", ""), normalizeUuidStatus(mapStr(body, "status", "")),
			mapStr(body, "remark", ""), now, now)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "保存失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "uuid": uuid})

	case http.MethodDelete:
		scope := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope")))
		if scope == "" {
			scope = "all"
		}
		if scope != "info" && scope != "configs" && scope != "all" {
			writeError(w, http.StatusBadRequest, "scope 仅支持 info / configs / all")
			return
		}
		var deletedConfigs, deletedInfo, deletedSub int64
		if scope != "info" {
			if res, err := h.db.ExecContext(r.Context(), `DELETE FROM configs WHERE uuid = ?`, uuid); err == nil {
				deletedConfigs, _ = res.RowsAffected()
			}
		}
		if scope != "configs" {
			if res, err := h.db.ExecContext(r.Context(), `DELETE FROM uuid_info WHERE uuid = ?`, uuid); err == nil {
				deletedInfo, _ = res.RowsAffected()
			}
		}
		if res, err := h.db.ExecContext(r.Context(), `DELETE FROM uuid_sub_sources WHERE uuid = ?`, uuid); err == nil {
			deletedSub, _ = res.RowsAffected()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "uuid": uuid, "scope": scope,
			"deleted_configs": deletedConfigs, "deleted_info": deletedInfo, "deleted_sub_sources": deletedSub,
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

// GET/PUT /api/uuids/{uuid}/nodes —— 查看/调整该 UUID 的 sing-box 节点授权
func (h *handlers) uuidNodes(w http.ResponseWriter, r *http.Request) {
	uuid := strings.ToLower(strings.TrimSpace(pathParam(r, "uuid")))
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID格式无效")
		return
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT n.id, n.server_id, n.name, COALESCE(n.uuid_list,'[]') AS uuid_list,
	      s.name AS server_name, t.name AS template_name
	    FROM singbox_nodes n LEFT JOIN servers s ON s.id = n.server_id
	    LEFT JOIN singbox_templates t ON t.id = n.template_id ORDER BY n.id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	if r.Method == http.MethodGet {
		data := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			list := parseUUIDList(mapStr(row, "uuid_list", "[]"))
			data = append(data, map[string]any{
				"id": mapInt(row, "id", 0), "name": mapStr(row, "name", ""),
				"server_name":   firstNonEmpty(mapStr(row, "server_name", ""), "#"+strconv.FormatInt(mapInt(row, "id", 0), 10)),
				"template_name": mapStr(row, "template_name", ""),
				"users":         len(list), "authorized": containsUUIDFold(list, uuid),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "uuid": uuid, "data": data})
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		return
	}
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	want := map[int64]bool{}
	for _, v := range jsonSlice(body["node_ids"]) {
		if id := jsonInt(v); id > 0 {
			want[id] = true
		}
	}
	results := []map[string]any{}
	affected := []int64{}
	nextByNode := map[int64][]string{}
	for _, row := range rows {
		id := mapInt(row, "id", 0)
		cur := parseUUIDList(mapStr(row, "uuid_list", "[]"))
		has := containsUUIDFold(cur, uuid)
		should := want[id]
		if has == should {
			continue
		}
		next := cur
		if should {
			next = append(next, uuid)
		} else {
			filtered := make([]string, 0, len(cur))
			for _, u := range cur {
				if u != uuid {
					filtered = append(filtered, u)
				}
			}
			next = filtered
		}
		raw, _ := json.Marshal(next)
		if _, err := h.db.ExecContext(r.Context(),
			`UPDATE singbox_nodes SET uuid_list = ?, updated_at = ? WHERE id = ?`, raw, nowMillis(), id); err == nil {
			results = append(results, map[string]any{"node_id": id, "authorized": should, "ok": true})
			affected = append(affected, id)
			nextByNode[id] = next
		} else {
			results = append(results, map[string]any{"node_id": id, "authorized": has, "ok": false, "error": "写入授权失败"})
		}
	}
	// 授权变化后立即重新渲染并排队下发（多个授权/解除一次性处理，失败的节点回传原因）
	// 同时按最新用户列表重建该节点在 configs 里的订阅配置：授权即增加，解除即回收。
	applyErr := map[int64]string{}
	for _, id := range affected {
		if _, _, _, _, msg := h.applySingboxNode(r.Context(), id, nil, false); msg != "" {
			applyErr[id] = msg
		}
		// 即使下发失败，也先按最新授权回收/重建订阅配置，避免用户侧留下失效配置
		if tpl, node, _, _, msg := h.loadSingboxNodeFull(r.Context(), id); msg == "" {
			users := singboxUsersFromUUIDs(tpl, nextByNode[id])
			_, _, _ = h.syncSingboxConfigsForUsers(r.Context(), id, node.Name, tpl, node, users)
		}
	}
	// 授权动作完成后异步复核机器侧配置，确保「授权 / 订阅配置 / 节点运行配置」一致
	for _, id := range affected {
		h.verifyNodeConsistencyAsync(id)
	}
	if len(applyErr) > 0 {
		for _, item := range results {
			if id, ok := item["node_id"].(int64); ok {
				if msg, bad := applyErr[id]; bad {
					item["ok"] = false
					item["error"] = msg
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "uuid": uuid, "data": results, "results": results,
		"changed": len(affected), "affected": len(affected),
	})
}

// GET/POST /api/uuids/{uuid}/configs
func (h *handlers) uuidConfigsRest(w http.ResponseWriter, r *http.Request) {
	uuid := pathParam(r, "uuid")
	if !uuidRe.MatchString(strings.ToLower(uuid)) {
		writeError(w, http.StatusBadRequest, "UUID格式无效")
		return
	}
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		all := q.Get("all") == "1" || strings.EqualFold(q.Get("all"), "true")
		page := atoiDefault(q.Get("page"), 1)
		size := atoiDefault(q.Get("size"), 10)
		if size < 1 {
			size = 10
		}
		if size > 2000 {
			size = 2000
		}
		protocol := strings.TrimSpace(q.Get("protocol"))
		search := strings.TrimSpace(q.Get("q"))
		where := []string{"uuid = ?", "config_data IS NOT NULL", "config_data != ''"}
		args := []any{uuid}
		if protocol != "" && protocol != "all" {
			where = append(where, "protocol = ?")
			args = append(args, protocol)
		}
		if search != "" {
			where = append(where, "(config_data LIKE ? OR IFNULL(remark,'') LIKE ?)")
			args = append(args, "%"+search+"%", "%"+search+"%")
		}
		query := `SELECT * FROM configs WHERE ` + joinAnd(where) + ` ORDER BY id ASC`
		countArgs := append([]any{}, args...)
		if !all {
			query += ` LIMIT ? OFFSET ?`
			args = append(args, size, (page-1)*size)
		}
		rows, err := queryRowsAsMaps(r.Context(), h.db, query, args...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "查询失败")
			return
		}
		var total int64
		_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM configs WHERE `+joinAnd(where), countArgs...).Scan(&total)
		if all {
			total = int64(len(rows))
		}
		mtpConfigs := h.mtprotoConfigsForUUID(r.Context(), uuid)
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "uuid": uuid, "total": total, "mtproto": mtpConfigs,
			"page": page, "size": func() int {
				if all {
					return len(rows)
				}
				return size
			}(), "data": rows,
		})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		return
	}
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	raw := body["config_data"]
	if raw == nil {
		raw = body["configs"]
	}
	if raw == nil {
		raw = body["data"]
	}
	lines := []string{}
	switch v := raw.(type) {
	case []any:
		for _, it := range v {
			if s := strings.TrimSpace(jsonStr(it)); s != "" {
				lines = append(lines, s)
			}
		}
	case string:
		for _, s := range strings.Split(v, "\n") {
			if s = strings.TrimSpace(s); s != "" {
				lines = append(lines, s)
			}
		}
	}
	if len(lines) == 0 {
		writeError(w, http.StatusBadRequest, "字段缺失")
		return
	}
	existing := map[string]bool{}
	if rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT config_data FROM configs WHERE uuid = ?`, uuid); err == nil {
		for _, row := range rows {
			existing[mapStr(row, "config_data", "")] = true
		}
	}
	added, skipped := 0, 0
	for _, line := range lines {
		if existing[line] {
			skipped++
			continue
		}
		protocol := getProtocol(line)
		if protocol == "unknown" {
			skipped++
			continue
		}
		remark := extractRemark(line, protocol)
		if _, err := h.db.ExecContext(r.Context(), `INSERT INTO configs
		  (uuid, config_data, protocol, remark, domain_hosting, created_at, updated_at)
		  VALUES (?, ?, ?, ?, 'Cloudflare', ?, ?)`,
			uuid, line, protocol, remark, nowMillis(), nowMillis()); err == nil {
			added++
			existing[line] = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "uuid": uuid, "added": added, "skipped": skipped})
}

// GET/PUT/DELETE /api/uuids/{uuid}/configs/{id}
func (h *handlers) uuidConfigItem(w http.ResponseWriter, r *http.Request) {
	uuid := pathParam(r, "uuid")
	id := jsonInt(pathParam(r, "id"))
	if !uuidRe.MatchString(strings.ToLower(uuid)) {
		writeError(w, http.StatusBadRequest, "UUID格式无效")
		return
	}
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "ID Required")
		return
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT * FROM configs WHERE id = ? AND uuid = ? LIMIT 1`, id, uuid)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "配置不存在，或不属于当前UUID，禁止编辑")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "uuid": uuid, "data": rows[0]})
	case http.MethodPut:
		var body map[string]any
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "无效 JSON")
			return
		}
		line := strings.TrimSpace(mapStr(body, "config_data", ""))
		if line == "" {
			writeError(w, http.StatusBadRequest, "字段缺失")
			return
		}
		protocol := getProtocol(line)
		if protocol == "unknown" {
			writeError(w, http.StatusBadRequest, "无法识别的配置协议")
			return
		}
		remark := mapStr(body, "remark", "")
		if _, ok := body["remark"]; !ok {
			remark = extractRemark(line, protocol)
		}
		if _, err := h.db.ExecContext(r.Context(),
			`UPDATE configs SET config_data = ?, protocol = ?, remark = ?, updated_at = ? WHERE id = ? AND uuid = ?`,
			line, protocol, remark, nowMillis(), id, uuid); err != nil {
			writeError(w, http.StatusInternalServerError, "更新失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": id, "uuid": uuid, "protocol": protocol, "remark": remark})
	case http.MethodDelete:
		res, err := h.db.ExecContext(r.Context(), `DELETE FROM configs WHERE id = ? AND uuid = ?`, id, uuid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "删除失败")
			return
		}
		n, _ := res.RowsAffected()
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": id, "uuid": uuid, "deleted": n})
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

func pathParam(r *http.Request, name string) string {
	return r.PathValue(name)
}

func containsUUIDFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), v) {
			return true
		}
	}
	return false
}

func parseUUIDList(raw string) []string {
	out := []string{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	var arr []any
	if json.Unmarshal([]byte(raw), &arr) != nil {
		return out
	}
	for _, v := range arr {
		if s := strings.TrimSpace(jsonStr(v)); s != "" {
			out = append(out, s)
		}
	}
	return out
}
