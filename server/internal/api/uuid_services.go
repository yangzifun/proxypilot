package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// GET/PUT /api/uuids/{uuid}/services
// 授权「服务」：同一台服务器上的 sing-box 节点与 MTProto 节点统一展示、统一授权。
func (h *handlers) uuidServices(w http.ResponseWriter, r *http.Request) {
	uuid := strings.ToLower(strings.TrimSpace(pathParam(r, "uuid")))
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID格式无效")
		return
	}
	if r.Method == http.MethodGet {
		rows, msg := h.uuidServiceRows(r.Context(), uuid)
		if msg != "" {
			writeError(w, http.StatusInternalServerError, msg)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "uuid": uuid, "data": rows})
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		return
	}
	var body struct {
		Services []struct {
			Kind string `json:"kind"`
			ID   int64  `json:"id"`
		} `json:"services"`
		Singbox []int64 `json:"singbox"`
		Mtproto []int64 `json:"mtproto"`
		Mihomo  []int64 `json:"mihomo"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	wantSingbox := map[int64]bool{}
	wantMtproto := map[int64]bool{}
	wantMihomo := map[int64]bool{}
	for _, id := range body.Singbox {
		if id > 0 {
			wantSingbox[id] = true
		}
	}
	for _, id := range body.Mtproto {
		if id > 0 {
			wantMtproto[id] = true
		}
	}
	for _, id := range body.Mihomo {
		if id > 0 {
			wantMihomo[id] = true
		}
	}
	for _, s := range body.Services {
		if s.ID <= 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(s.Kind)) {
		case "singbox", "sing-box", "sb":
			wantSingbox[s.ID] = true
		case "mtproto", "mtg":
			wantMtproto[s.ID] = true
		case "mihomo", "mihomo-node", "mh":
			wantMihomo[s.ID] = true
		}
	}
	sbChanged, sbResults, msg := h.applySingboxServiceAuth(r.Context(), uuid, wantSingbox)
	if msg != "" {
		writeError(w, http.StatusInternalServerError, msg)
		return
	}
	mtChanged, mtResults, msg := h.applyMtprotoServiceAuth(r.Context(), uuid, wantMtproto)
	if msg != "" {
		writeError(w, http.StatusInternalServerError, msg)
		return
	}
	mhChanged, mhResults, msg := h.applyMihomoServiceAuth(r.Context(), uuid, wantMihomo)
	if msg != "" {
		writeError(w, http.StatusInternalServerError, msg)
		return
	}
	results := append(append(sbResults, mtResults...), mhResults...)
	failed := 0
	for _, item := range results {
		if ok, _ := item["ok"].(bool); !ok {
			failed++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "uuid": uuid, "changed": sbChanged + mtChanged + mhChanged,
		"results": results, "failed": failed,
	})
}

// uuidServiceRows 汇总该 UUID 在 sing-box / MTProto 两类服务上的授权状态。
func (h *handlers) uuidServiceRows(ctx context.Context, uuid string) ([]map[string]any, string) {
	out := []map[string]any{}
	sbRows, err := queryRowsAsMaps(ctx, h.db, `SELECT n.id, n.name, COALESCE(n.uuid_list,'[]') AS uuid_list,
	      COALESCE(s.name,'') AS server_name, COALESCE(s.id,0) AS server_id, COALESCE(t.name,'') AS template_name
	    FROM singbox_nodes n LEFT JOIN servers s ON s.id = n.server_id
	    LEFT JOIN singbox_templates t ON t.id = n.template_id ORDER BY n.id ASC`)
	if err != nil {
		return nil, "查询 sing-box 节点失败"
	}
	for _, row := range sbRows {
		list := parseUUIDList(mapStr(row, "uuid_list", "[]"))
		out = append(out, map[string]any{
			"kind": "singbox", "id": mapInt(row, "id", 0), "name": mapStr(row, "name", ""),
			"server_id": mapInt(row, "server_id", 0), "server_name": mapStr(row, "server_name", ""),
			"template_name": mapStr(row, "template_name", ""), "users": len(list),
			"authorized": containsUUIDFold(list, uuid),
		})
	}
	mtRows, err := queryRowsAsMaps(ctx, h.db, `SELECT n.id, n.name, COALESCE(s.name,'') AS server_name,
	      COALESCE(s.id,0) AS server_id, COALESCE(t.name,'') AS template_name,
	      (SELECT COUNT(*) FROM mtproto_clients c WHERE c.node_id = n.id AND c.status = 'active') AS users,
	      (SELECT COUNT(*) FROM mtproto_clients c WHERE c.node_id = n.id AND c.status = 'active' AND LOWER(c.uuid) = ?) AS mine
	    FROM mtproto_nodes n LEFT JOIN servers s ON s.id = n.server_id
	    LEFT JOIN mtproto_templates t ON t.id = n.template_id ORDER BY n.id ASC`, uuid)
	if err != nil {
		return nil, "查询 MTProto 节点失败"
	}
	for _, row := range mtRows {
		out = append(out, map[string]any{
			"kind": "mtproto", "id": mapInt(row, "id", 0), "name": mapStr(row, "name", ""),
			"server_id": mapInt(row, "server_id", 0), "server_name": mapStr(row, "server_name", ""),
			"template_name": mapStr(row, "template_name", ""), "users": mapInt(row, "users", 0),
			"authorized": mapInt(row, "mine", 0) > 0,
		})
	}
	mhRows, err := queryRowsAsMaps(ctx, h.db, "SELECT n.id, n.name, COALESCE(n.uuid_list,'[]') AS uuid_list, "+
		"COALESCE(s.name,'') AS server_name, COALESCE(s.id,0) AS server_id, COALESCE(t.name,'') AS template_name, "+
		"COALESCE(n.port_base,0) AS port_base "+
		"FROM mihomo_nodes n JOIN mihomo_templates t ON t.id = n.template_id AND COALESCE(t.mode,'subscription') = 'server' "+
		"LEFT JOIN servers s ON s.id = n.server_id ORDER BY n.id ASC")
	if err != nil {
		return nil, "查询 mihomo 节点失败"
	}
	for _, row := range mhRows {
		list := parseUUIDList(mapStr(row, "uuid_list", "[]"))
		out = append(out, map[string]any{
			"kind": "mihomo", "id": mapInt(row, "id", 0), "name": mapStr(row, "name", ""),
			"server_id": mapInt(row, "server_id", 0), "server_name": mapStr(row, "server_name", ""),
			"template_name": mapStr(row, "template_name", ""), "users": len(list),
			"authorized": containsUUIDFold(list, uuid),
		})
	}
	return out, ""
}

// applyMihomoServiceAuth 按目标集合增删 mihomo 节点授权（uuid_list）。
func (h *handlers) applyMihomoServiceAuth(ctx context.Context, uuid string, want map[int64]bool) (int, []map[string]any, string) {
	rows, err := queryRowsAsMaps(ctx, h.db,
		"SELECT n.id, COALESCE(n.uuid_list,'[]') AS uuid_list FROM mihomo_nodes n "+
			"JOIN mihomo_templates t ON t.id = n.template_id AND COALESCE(t.mode,'subscription') = 'server' ORDER BY n.id ASC")
	if err != nil {
		return 0, nil, "查询 mihomo 节点失败"
	}
	results := []map[string]any{}
	changed := 0
	for _, row := range rows {
		id := mapInt(row, "id", 0)
		cur := parseUUIDList(mapStr(row, "uuid_list", "[]"))
		has := containsUUIDFold(cur, uuid)
		should := want[id]
		if has == should {
			continue
		}
		next := make([]string, 0, len(cur)+1)
		for _, u := range cur {
			if !strings.EqualFold(strings.TrimSpace(u), uuid) {
				next = append(next, u)
			}
		}
		if should {
			next = append(next, uuid)
		}
		raw, err := json.Marshal(next)
		if err != nil {
			continue
		}
		if _, err := h.db.ExecContext(ctx,
			"UPDATE mihomo_nodes SET uuid_list = ?, updated_at = ? WHERE id = ?", string(raw), nowMillis(), id); err != nil {
			results = append(results, map[string]any{"kind": "mihomo", "id": id, "ok": false, "error": "写入授权失败"})
			continue
		}
		changed++
		results = append(results, map[string]any{"kind": "mihomo", "id": id, "authorized": should, "ok": true})
	}
	return changed, results, ""
}

// applySingboxServiceAuth 按目标集合增删 sing-box 授权。
func (h *handlers) applySingboxServiceAuth(ctx context.Context, uuid string, want map[int64]bool) (int, []map[string]any, string) {
	rows, err := queryRowsAsMaps(ctx, h.db, `SELECT id, COALESCE(uuid_list,'[]') AS uuid_list FROM singbox_nodes ORDER BY id ASC`)
	if err != nil {
		return 0, nil, "查询 sing-box 节点失败"
	}
	results := []map[string]any{}
	changed := 0
	nextByNode := map[int64][]string{}
	affected := []int64{}
	for _, row := range rows {
		id := mapInt(row, "id", 0)
		cur := parseUUIDList(mapStr(row, "uuid_list", "[]"))
		has := containsUUIDFold(cur, uuid)
		should := want[id]
		if has == should {
			continue
		}
		next := make([]string, 0, len(cur)+1)
		for _, u := range cur {
			if !strings.EqualFold(strings.TrimSpace(u), uuid) {
				next = append(next, u)
			}
		}
		if should {
			next = append(next, uuid)
		}
		raw, err := json.Marshal(next)
		if err != nil {
			continue
		}
		if _, err := h.db.ExecContext(ctx,
			`UPDATE singbox_nodes SET uuid_list = ?, updated_at = ? WHERE id = ?`, raw, nowMillis(), id); err != nil {
			results = append(results, map[string]any{"kind": "singbox", "id": id, "ok": false, "error": "写入授权失败"})
			continue
		}
		changed++
		affected = append(affected, id)
		nextByNode[id] = next
		results = append(results, map[string]any{"kind": "singbox", "id": id, "authorized": should, "ok": true})
	}
	for _, id := range affected {
		applied := true
		if _, _, _, _, msg := h.applySingboxNode(ctx, id, nil, false); msg != "" {
			applied = false
			results = append(results, map[string]any{"kind": "singbox", "id": id, "ok": false, "error": msg})
		}
		if tpl, node, _, _, msg := h.loadSingboxNodeFull(ctx, id); msg == "" {
			users := singboxUsersFromUUIDs(tpl, nextByNode[id])
			_, _, _ = h.syncSingboxConfigsForUsers(ctx, id, node.Name, tpl, node, users)
		}
		if applied {
			h.verifyNodeConsistencyAsync(id)
		}
	}
	return changed, results, ""
}

// applyMtprotoServiceAuth 按目标集合启用/撤销 MTProto 客户端并重渲染下发。
func (h *handlers) applyMtprotoServiceAuth(ctx context.Context, uuid string, want map[int64]bool) (int, []map[string]any, string) {
	rows, err := queryRowsAsMaps(ctx, h.db, `SELECT id FROM mtproto_nodes ORDER BY id ASC`)
	if err != nil {
		return 0, nil, "查询 MTProto 节点失败"
	}
	results := []map[string]any{}
	changed := 0
	affected := []int64{}
	for _, row := range rows {
		id := mapInt(row, "id", 0)
		var active int64
		_ = h.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM mtproto_clients WHERE node_id = ? AND status = 'active' AND LOWER(uuid) = ?`, id, uuid).Scan(&active)
		has := active > 0
		should := want[id]
		if has == should {
			continue
		}
		if should {
			meta, err := h.mtprotoNodesWithMeta(ctx, "WHERE n.id = ?", id)
			if err != nil || len(meta) == 0 {
				results = append(results, map[string]any{"kind": "mtproto", "id": id, "ok": false, "error": "节点不存在"})
				continue
			}
			tpl, _, _ := h.loadMtprotoTemplate(ctx, mapInt(meta[0], "template_id", 0))
			eff := mtprotoEffective(tpl, mtprotoNodeFromRow(meta[0]))
			secret := mtprotoGenerateSecret(eff.Domain)
			if secret == "" {
				results = append(results, map[string]any{"kind": "mtproto", "id": id, "ok": false, "error": "节点缺少有效伪装域名，无法生成 secret"})
				continue
			}
			if _, err := h.db.ExecContext(ctx, `INSERT INTO mtproto_clients
			  (node_id, uuid, email, label, secret, status, remark, created_at, updated_at)
			  VALUES (?, ?, '', '', ?, 'active', '服务授权', ?, ?)`,
				id, uuid, secret, nowMillis(), nowMillis()); err != nil {
				results = append(results, map[string]any{"kind": "mtproto", "id": id, "ok": false, "error": "创建客户端失败"})
				continue
			}
		} else {
			if _, err := h.db.ExecContext(ctx,
				`UPDATE mtproto_clients SET status = 'revoked', revoked_at = ?, updated_at = ?
				  WHERE node_id = ? AND LOWER(uuid) = ? AND status = 'active'`,
				nowMillis(), nowMillis(), id, uuid); err != nil {
				results = append(results, map[string]any{"kind": "mtproto", "id": id, "ok": false, "error": "撤销客户端失败"})
				continue
			}
		}
		changed++
		affected = append(affected, id)
		results = append(results, map[string]any{"kind": "mtproto", "id": id, "authorized": should, "ok": true})
	}
	for _, id := range affected {
		if _, _, msg := h.applyMtprotoToNode(ctx, id); msg != "" {
			results = append(results, map[string]any{"kind": "mtproto", "id": id, "ok": false, "error": msg})
		}
	}
	return changed, results, ""
}
